package webscrape_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/webscrape"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

type fixedClock struct{ t int64 }

func (c *fixedClock) Now() int64 { c.t++; return c.t }

// recordingClient captures the scrape requests a connector task makes and returns a
// canned list of extracted values.
type recordingClient struct {
	requests []webscrape.Request
	values   []string
}

func (r *recordingClient) Scrape(_ context.Context, req webscrape.Request) ([]string, error) {
	r.requests = append(r.requests, req)
	return r.values, nil
}

type erroringClient struct{}

func (erroringClient) Scrape(context.Context, webscrape.Request) ([]string, error) {
	return nil, context.DeadlineExceeded
}

const wsDefKey = 77

func openStore(t *testing.T) (*wal.Log, *state.Store) {
	t.Helper()
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { store.Close(); log.Close() })
	return log, store
}

func mustActiveProcs(t *testing.T, s *state.Store) int {
	t.Helper()
	n, err := s.ActiveProcessInstanceCount()
	if err != nil {
		t.Fatalf("ActiveProcessInstanceCount: %v", err)
	}
	return n
}

func soleInstanceKey(t *testing.T, s *state.Store) uint64 {
	t.Helper()
	var keys []uint64
	if err := s.ActiveProcessInstances(func(k uint64, _ *model.ProcessInstanceValue) error {
		keys = append(keys, k)
		return nil
	}); err != nil {
		t.Fatalf("ActiveProcessInstances: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("live instances = %d, want exactly 1", len(keys))
	}
	return keys[0]
}

func readVar(t *testing.T, s *state.Store, scope uint64, name string) *model.VariableValue {
	t.Helper()
	var found *model.VariableValue
	if err := s.VariablesOfScope(scope, func(v *model.VariableValue) error {
		if v.Name == name {
			cp := *v
			found = &cp
		}
		return nil
	}); err != nil {
		t.Fatalf("VariablesOfScope: %v", err)
	}
	return found
}

// wsThenWaitProcess: Start → scrape task (writes resultVar) → plain service task
// (parks, so the instance stays alive) → End, so a test can read the written result
// variable before the instance ends.
func wsThenWaitProcess(t *testing.T, cfg compiler.WebScrapeConfig) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(wsDefKey, "scrape", 1)
	start := b.AddStartEvent()
	scrape := b.AddWebScrapeConnectorTask(cfg)
	wait := b.AddServiceTask("wait", 3)
	end := b.AddEndEvent()
	b.Connect(start, scrape)
	b.Connect(scrape, wait)
	b.Connect(wait, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ConnectorTask(cp.Node(scrape).Detail).JobType
}

func drive(t *testing.T, cp *compiler.CompiledProcess, jobType int32, client webscrape.Client, store *state.Store, log *wal.Log, vars ...model.VariableValue) error {
	t.Helper()
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, func(rd state.Reader) job.OutputHandler {
		return webscrape.Handler(store, func(uint64) *compiler.CompiledProcess { return cp }, client)
	})
	p.CreateInstance(cp.Key, vars...)
	return runner.Drive()
}

func compileExpr(t *testing.T, src string) *expr.Compiled {
	t.Helper()
	e, err := expr.CompileAuto(src)
	if err != nil {
		t.Fatalf("compile %q: %v", src, err)
	}
	return e
}

// TestWebScrapeConnectorWritesArray is the vertical slice end to end: a scrape task
// creates a job, the in-process worker fetches the page through the client, and the
// extracted values are written into the result variable as a JSON array; the token
// advances (parking on a following task so the written variable is still readable).
func TestWebScrapeConnectorWritesArray(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := wsThenWaitProcess(t, compiler.WebScrapeConfig{
		Url:      compiler.RestExpr{Literal: "https://example.com/news"},
		Selector: compiler.RestExpr{Literal: ".headline"},
		Result:   "headlines",
		Retries:  3,
	})
	if jobType != compiler.WebScrapeJobTypeIndex {
		t.Fatalf("scrape job type index = %d, want the reserved %d", jobType, compiler.WebScrapeJobTypeIndex)
	}
	rc := &recordingClient{values: []string{"Alpha", "Beta"}}
	if err := drive(t, cp, jobType, rc, store, log); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if len(rc.requests) != 1 {
		t.Fatalf("requests made = %d, want 1", len(rc.requests))
	}
	if req := rc.requests[0]; req.URL != "https://example.com/news" || req.Selector != ".headline" {
		t.Errorf("request = %+v, want the model url/selector", req)
	}
	scope := soleInstanceKey(t, store)
	got := readVar(t, store, scope, "headlines")
	if got == nil || got.Kind != model.VarJSON {
		t.Fatalf("result variable = %+v, want a structured VarJSON array", got)
	}
	if got.Text != `["Alpha","Beta"]` {
		t.Errorf("result variable text = %q, want the JSON array", got.Text)
	}
}

// TestWebScrapeConnectorFeel is the end-to-end fx slice: the worker evaluates FEEL
// url/selector values over the instance's variables at call time, and passes the
// authored attribute through to the client.
func TestWebScrapeConnectorFeel(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := wsThenWaitProcess(t, compiler.WebScrapeConfig{
		Url:       compiler.RestExpr{Expr: compileExpr(t, `"https://example.com/" + slug`)},
		Selector:  compiler.RestExpr{Expr: compileExpr(t, `sel`)},
		Attribute: "href",
		Result:    "links",
		Retries:   3,
	})
	rc := &recordingClient{values: []string{"/x"}}
	if err := drive(t, cp, jobType, rc, store, log,
		model.VariableValue{Name: "slug", Kind: model.VarString, Text: "world"},
		model.VariableValue{Name: "sel", Kind: model.VarString, Text: "a.link"},
	); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if len(rc.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(rc.requests))
	}
	req := rc.requests[0]
	if req.URL != "https://example.com/world" || req.Selector != "a.link" {
		t.Errorf("request url/selector = %q / %q, want the FEEL-computed values", req.URL, req.Selector)
	}
	if req.Attribute != "href" {
		t.Errorf("request attribute = %q, want href", req.Attribute)
	}
}

// TestWebScrapeConnectorClientError proves a fetch error fails the job (retried, then
// an incident) rather than completing it.
func TestWebScrapeConnectorClientError(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := wsThenWaitProcess(t, compiler.WebScrapeConfig{
		Url:      compiler.RestExpr{Literal: "https://example.com"},
		Selector: compiler.RestExpr{Literal: ".x"},
		Result:   "r",
		Retries:  3,
	})
	if err := drive(t, cp, jobType, erroringClient{}, store, log); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Fatalf("after client error: active=%d, want 1 (job never completed)", pi)
	}
}

// TestWebScrapeConnectorNoCompiledProcess covers the worker's guard for a job whose
// definition can't be resolved: the handler errors, leaving the job pending.
func TestWebScrapeConnectorNoCompiledProcess(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := wsThenWaitProcess(t, compiler.WebScrapeConfig{
		Url:      compiler.RestExpr{Literal: "https://example.com"},
		Selector: compiler.RestExpr{Literal: ".x"},
		Result:   "r",
		Retries:  3,
	})
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, func(rd state.Reader) job.OutputHandler {
		return webscrape.Handler(store, func(uint64) *compiler.CompiledProcess { return nil }, &recordingClient{})
	})
	p.CreateInstance(cp.Key)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Errorf("after unresolvable definition: active=%d, want 1 (job never completed)", pi)
	}
}

// TestWebScrapeConnectorNoResultVariable proves a task with no result variable
// discards the extracted values and the instance still finishes.
func TestWebScrapeConnectorNoResultVariable(t *testing.T) {
	log, store := openStore(t)
	b := compiler.NewBuilder(wsDefKey, "scrape", 1)
	start := b.AddStartEvent()
	scrape := b.AddWebScrapeConnectorTask(compiler.WebScrapeConfig{
		Url:      compiler.RestExpr{Literal: "https://example.com"},
		Selector: compiler.RestExpr{Literal: ".x"},
		Result:   "", // no result variable
		Retries:  3,
	})
	end := b.AddEndEvent()
	b.Connect(start, scrape)
	b.Connect(scrape, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ConnectorTask(cp.Node(scrape).Detail).JobType
	rc := &recordingClient{values: []string{"a", "b"}}
	if err := drive(t, cp, jobType, rc, store, log); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if len(rc.requests) != 1 {
		t.Fatalf("requests = %d, want 1 (the scrape still runs)", len(rc.requests))
	}
	if pi := mustActiveProcs(t, store); pi != 0 {
		t.Fatalf("after Drive: active=%d, want 0 (instance finished)", pi)
	}
}

// TestWebScrapeHandlerElementInstanceGone covers the handler's guard for a job whose
// element instance has already completed: it is a no-op, not an error.
func TestWebScrapeHandlerElementInstanceGone(t *testing.T) {
	_, store := openStore(t)
	h := webscrape.Handler(store, func(uint64) *compiler.CompiledProcess { return nil }, &recordingClient{})
	out, err := h(job.Job{ElementInstanceKey: 424242})
	if err != nil || out != nil {
		t.Fatalf("handler for a vanished element instance: out=%v err=%v, want nil,nil", out, err)
	}
}

// TestWebScrapeConnectorSeesInputMappedLocal proves the worker resolves its FEEL url
// and selector up the scope chain, so an input-mapped local is visible to them beside
// what the task inherits (ADR-0068).
func TestWebScrapeConnectorSeesInputMappedLocal(t *testing.T) {
	log, store := openStore(t)
	compile := func(src string) *expr.Compiled {
		e, err := expr.CompileAuto(src)
		if err != nil {
			t.Fatalf("compile %q: %v", src, err)
		}
		return e
	}
	b := compiler.NewBuilder(wsDefKey, "news", 1)
	start := b.AddStartEvent()
	call := b.AddWebScrapeConnectorTask(compiler.WebScrapeConfig{
		Url:      compiler.RestExpr{Expr: compile(`page`)},
		Selector: compiler.RestExpr{Literal: "h1"},
		Retries:  3,
	})
	b.AddInputMapping(call, "page", compile(`"https://example.com/" + slug`))
	end := b.AddEndEvent()
	b.Connect(start, call)
	b.Connect(call, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ConnectorTask(cp.Node(call).Detail).JobType

	rc := &recordingClient{values: []string{"Headline"}}
	if err := drive(t, cp, jobType, rc, store, log,
		model.VariableValue{Name: "slug", Kind: model.VarString, Text: "news"}); err != nil {
		t.Fatalf("Drive: %v", err)
	}

	if len(rc.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(rc.requests))
	}
	if got := rc.requests[0].URL; got != "https://example.com/news" {
		t.Errorf("url = %q, want the input-mapped local", got)
	}
}
