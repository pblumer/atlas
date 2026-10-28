// Package behaviourimpact holds the tests for impact.py, the check that lists which
// deployed models a behaviour change touches (ADR-0508). The check is Python, so an
// operator can run it from any machine against a server; what it reads from a model is
// held here to what the compiler reads, so the two cannot drift apart unnoticed.
package behaviourimpact

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
)

// python is the interpreter the check runs on, or a skip where there is none.
func python(t *testing.T) string {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed; impact.py cannot run here")
	}
	return py
}

// runCheck runs impact.py with args and returns its standard output and exit code.
func runCheck(t *testing.T, args ...string) ([]byte, int) {
	t.Helper()
	cmd := exec.Command(python(t), append([]string{"-I", "impact.py"}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return stdout.Bytes(), 0
	case errors.As(err, &exit):
		return stdout.Bytes(), exit.ExitCode()
	default:
		t.Fatalf("impact.py: %v\n%s", err, stderr.Bytes())
		return nil, -1
	}
}

type finding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Element  string `json:"element"`
	Message  string `json:"message"`
}

type report struct {
	Definitions []struct {
		File      string    `json:"file"`
		Key       uint64    `json:"key"`
		ProcessID string    `json:"processId"`
		Version   int32     `json:"version"`
		Running   int       `json:"running"`
		Findings  []finding `json:"findings"`
	} `json:"definitions"`
	NotChecked []json.RawMessage `json:"notChecked"`
}

func decodeReport(t *testing.T, out []byte) report {
	t.Helper()
	var r report
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("report is not JSON: %v\n%s", err, out)
	}
	return r
}

func summary(fs []finding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Severity+" "+f.Rule+" "+f.Element)
	}
	slices.Sort(out)
	return out
}

// TestEachFixtureIsReportedAsItsCommentSays: every fixture states in a comment what the
// check must say about it, and that is what it says — the positive case of each rule and
// the neighbouring case each rule must leave alone.
func TestEachFixtureIsReportedAsItsCommentSays(t *testing.T) {
	want := map[string][]string{
		"compensation-waits.bpmn":             {"changed compensation.waits cancel"},
		"compensation-unchanged.bpmn":         nil,
		"compensation-end.bpmn":               {"info compensation.waits undo"},
		"compensation-event-subprocess.bpmn":  {"changed compensation.event-subprocess rollback"},
		"compensation-own-run.bpmn":           {"changed compensation.event-subprocess undo_change"},
		"receive-outputs-read.bpmn":           {"changed receive.output-mappings wait_answer"},
		"receive-outputs-unread.bpmn":         {"review receive.output-mappings wait_exit"},
		"correlation-local.bpmn":              {"changed correlation.scope wait_approval", "review payload.scope wait_approval"},
		"correlation-own-inputs.bpmn":         {"changed correlation.scope on_cancel", "changed correlation.scope wait_reply"},
		"correlation-subprocess-results.bpmn": {"review correlation.scope wait_goods", "review payload.scope wait_goods"},
		"root-unchanged.bpmn":                 nil,
		"event-subprocess-runs.bpmn":          {"info event-subprocess.runs on_update"},
	}
	out, code := runCheck(t, "--dir", "testdata", "--json")
	if code != 0 {
		t.Fatalf("exit %d without --fail-on", code)
	}
	r := decodeReport(t, out)
	if len(r.NotChecked) != 0 {
		t.Fatalf("not checked: %s", r.NotChecked)
	}
	got := map[string][]string{}
	for _, d := range r.Definitions {
		got[d.File] = append(got[d.File], summary(d.Findings)...)
	}
	for file, w := range want {
		if g := got[file]; !slices.Equal(g, w) {
			t.Errorf("%s:\n got %q\nwant %q", file, g, w)
		}
	}
	for file := range got {
		if _, ok := want[file]; !ok {
			t.Errorf("%s has no expectation in this test", file)
		}
	}
}

// TestFixturesCompile: a fixture the compiler refuses would describe a model nobody can
// deploy, so what the check says about it would be about nothing.
func TestFixturesCompile(t *testing.T) {
	files, err := filepath.Glob("testdata/*.bpmn")
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := compiler.ParseAll(1, 1, bytes.NewReader(src)); err != nil {
			t.Errorf("%s does not compile: %v", f, err)
		}
	}
}

// TestFailOnSetsTheExitCode: --fail-on makes the check usable as a gate before an upgrade.
func TestFailOnSetsTheExitCode(t *testing.T) {
	for _, c := range []struct {
		file, level string
		want        int
	}{
		{"testdata/compensation-waits.bpmn", "changed", 1},
		{"testdata/receive-outputs-unread.bpmn", "changed", 0},
		{"testdata/receive-outputs-unread.bpmn", "review", 1},
		{"testdata/event-subprocess-runs.bpmn", "review", 0},
		{"testdata/root-unchanged.bpmn", "review", 0},
	} {
		if _, code := runCheck(t, "--dir", c.file, "--fail-on", c.level); code != c.want {
			t.Errorf("%s --fail-on %s: exit %d, want %d", c.file, c.level, code, c.want)
		}
	}
}

// TestTheServerSourceChecksNewestAndRunningVersions: against a server, the check reads the
// instance summary and fetches the model of the newest version of each process and of
// every version still running — and nothing else — sending the token it was given.
func TestTheServerSourceChecksNewestAndRunningVersions(t *testing.T) {
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	models := map[string][]byte{
		"11": read("root-unchanged.bpmn"),     // root_unchanged v1, nothing running: skipped
		"12": read("root-unchanged.bpmn"),     // root_unchanged v2, newest
		"21": read("compensation-waits.bpmn"), // compensation_waits v1, still running
		"22": read("compensation-waits.bpmn"), // compensation_waits v2, newest
	}
	rows := `[
	 {"processDefKey":11,"processId":"root_unchanged","version":1,"active":0,"completed":4},
	 {"processDefKey":12,"processId":"root_unchanged","version":2,"active":0,"completed":0},
	 {"processDefKey":21,"processId":"compensation_waits","version":1,"active":3,"completed":9},
	 {"processDefKey":22,"processId":"compensation_waits","version":2,"active":1,"completed":0}]`
	var mu sync.Mutex
	var fetched []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/api/v1/instances/summary":
			fmt.Fprint(w, rows)
		case strings.HasPrefix(r.URL.Path, "/api/v1/processes/") && strings.HasSuffix(r.URL.Path, "/xml"):
			key := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/processes/"), "/xml")
			mu.Lock()
			fetched = append(fetched, key)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/xml")
			w.Write(models[key])
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	out, code := runCheck(t, "--server", srv.URL, "--token", "secret", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	r := decodeReport(t, out)
	slices.Sort(fetched)
	if !slices.Equal(fetched, []string{"12", "21", "22"}) {
		t.Errorf("fetched %v, want the newest of each process and every running version", fetched)
	}
	var got []string
	for _, d := range r.Definitions {
		got = append(got, fmt.Sprintf("%d %s v%d running=%d %v", d.Key, d.ProcessID, d.Version, d.Running, summary(d.Findings)))
	}
	want := []string{
		"12 root_unchanged v2 running=0 []",
		"21 compensation_waits v1 running=3 [changed compensation.waits cancel]",
		"22 compensation_waits v2 running=1 [changed compensation.waits cancel]",
	}
	if !slices.Equal(got, want) {
		t.Errorf("report:\n got %q\nwant %q", got, want)
	}

	if _, code := runCheck(t, "--server", srv.URL, "--token", "wrong"); code != 2 {
		t.Errorf("a refused summary should exit 2, got %d", code)
	}
}

// facts is what impact.py reads from a model (its --facts output).
type facts []struct {
	File      string `json:"file"`
	Error     string `json:"error"`
	Processes []struct {
		ProcessID string                  `json:"processId"`
		Nodes     map[string]nodeFacts    `json:"nodes"`
		Messages  map[string]messageFacts `json:"messages"`
		Exprs     []exprFacts             `json:"expressions"`
	} `json:"processes"`
}

type nodeFacts struct {
	Tag               string    `json:"tag"`
	Scope             *string   `json:"scope"`
	EventSubprocess   bool      `json:"eventSubprocess"`
	Inputs            []mapping `json:"inputs"`
	Outputs           []mapping `json:"outputs"`
	Compensate        bool      `json:"compensate"`
	WaitForCompletion *string   `json:"waitForCompletion"`
	ActivityRef       *string   `json:"activityRef"`
	AttachedTo        *string   `json:"attachedTo"`
}

type mapping struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type messageFacts struct {
	CorrelationKey *string  `json:"correlationKey"`
	KeyNames       []string `json:"keyNames"`
}

type exprFacts struct {
	Element string   `json:"element"`
	Source  string   `json:"source"`
	Names   []string `json:"names"`
}

// corpus is every BPMN model in the repository, the check's fixtures included, and the
// directories IMPACT_CORPUS lists (separated as PATH is): models exported from a server,
// so an operator can hold the check's reading of their own models to the compiler's.
func corpus(t *testing.T) []string {
	t.Helper()
	root := filepath.Join("..", "..")
	var dirs []string
	for _, d := range []string{"conformance", "examples", "api/systemprocesses", "playground",
		"docs", "e2e", "postman", "scripts"} {
		if _, err := os.Stat(filepath.Join(root, d)); err == nil {
			dirs = append(dirs, filepath.Join(root, d))
		}
	}
	for _, d := range filepath.SplitList(os.Getenv("IMPACT_CORPUS")) {
		if d != "" {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// TestTheCheckReadsModelsAsTheCompilerDoes holds impact.py's reading of every model in the
// repository to the compiler's: which scope each element is in, which subprocess is an
// event subprocess, which mappings count, what a compensation event waits for and names.
// The rules are only as right as these facts.
func TestTheCheckReadsModelsAsTheCompilerDoes(t *testing.T) {
	out, code := runCheck(t, append([]string{"--facts", "--dir"}, corpus(t)...)...)
	if code != 0 {
		t.Fatalf("--facts exit %d", code)
	}
	var fs facts
	if err := json.Unmarshal(out, &fs); err != nil {
		t.Fatalf("facts are not JSON: %v", err)
	}
	compiled, compared := 0, 0
	for _, f := range fs {
		if f.Error != "" {
			continue // not well-formed XML: the compiler refuses it too
		}
		src, err := os.ReadFile(f.File)
		if err != nil {
			t.Fatal(err)
		}
		deps, err := compiler.ParseAll(1, 1, bytes.NewReader(src))
		if err != nil {
			continue // a model the compiler refuses on purpose (a negative fixture)
		}
		compiled++
		for _, d := range deps {
			cp := d.Process
			var p *struct {
				ProcessID string                  `json:"processId"`
				Nodes     map[string]nodeFacts    `json:"nodes"`
				Messages  map[string]messageFacts `json:"messages"`
				Exprs     []exprFacts             `json:"expressions"`
			}
			for i := range f.Processes {
				if f.Processes[i].ProcessID == cp.ProcessId() {
					p = &f.Processes[i]
				}
			}
			if p == nil {
				t.Errorf("%s: the check does not read process %q", f.File, cp.ProcessId())
				continue
			}
			for id, n := range p.Nodes {
				idx, ok := cp.ElementIndexOf(id)
				if !ok {
					continue // an element the compiler folds away (a compensation boundary, say)
				}
				compared++
				node := cp.Node(idx)
				wantScope := ""
				if node.FlowScope >= 0 {
					wantScope = cp.ElementBpmnId(node.FlowScope)
				}
				if got := deref(n.Scope); got != wantScope {
					t.Errorf("%s %s: scope %q, compiler %q", f.File, id, got, wantScope)
				}
				if n.Tag == "subProcess" && n.EventSubprocess != cp.IsEventSubProcess(idx) {
					t.Errorf("%s %s: event subprocess %v, compiler %v", f.File, id, n.EventSubprocess, cp.IsEventSubProcess(idx))
				}
				if int32(len(n.Inputs)) != node.IOInCount || int32(len(n.Outputs)) != node.IOOutCount {
					t.Errorf("%s %s: %d inputs and %d outputs, compiler %d and %d",
						f.File, id, len(n.Inputs), len(n.Outputs), node.IOInCount, node.IOOutCount)
				}
				isThrow := node.Type == compiler.TypeCompensationThrowEvent || node.Type == compiler.TypeCompensationEndEvent
				if want := n.Compensate && (n.Tag == "intermediateThrowEvent" || n.Tag == "endEvent"); want != isThrow {
					t.Errorf("%s %s: compensation event %v, compiler %v", f.File, id, want, isThrow)
				}
				if isThrow {
					d := cp.CompensationThrow(node.Detail)
					if waits := strings.TrimSpace(deref(n.WaitForCompletion)) != "false"; waits != d.WaitForCompletion {
						t.Errorf("%s %s: waits %v, compiler %v", f.File, id, waits, d.WaitForCompletion)
					}
					if got, want := deref(n.ActivityRef), cp.ElementBpmnId(d.ActivityRef); got != want {
						t.Errorf("%s %s: activityRef %q, compiler %q", f.File, id, got, want)
					}
				}
			}
		}
	}
	t.Logf("compared %d elements in %d models", compared, compiled)
	if compiled < 50 || compared < 500 {
		t.Fatalf("compared %d elements in %d models; the corpus is not where this test looks", compared, compiled)
	}
}

// TestTheCheckReadsFEELNamesAsTheCompilerDoes holds impact.py's reading of a FEEL
// expression — which variables it reads — to the FEEL compiler's, over every expression in
// every model in the repository and every message's correlation key. The rules decide on
// those names: a key that reads a locally declared variable, a payload name someone reads.
func TestTheCheckReadsFEELNamesAsTheCompilerDoes(t *testing.T) {
	out, code := runCheck(t, append([]string{"--facts", "--dir"}, corpus(t)...)...)
	if code != 0 {
		t.Fatalf("--facts exit %d", code)
	}
	var fs facts
	if err := json.Unmarshal(out, &fs); err != nil {
		t.Fatalf("facts are not JSON: %v", err)
	}
	checked := 0
	check := func(file, where, source string, names []string) {
		text := strings.TrimSpace(source)
		text = strings.TrimSpace(strings.TrimPrefix(text, "="))
		if text == "" {
			return
		}
		if expr.CheckCallsError(text) != nil {
			return // a call that can only be null: deploy refuses it (ADR-0388), so its reading is moot
		}
		c, err := expr.CompileAuto(text)
		if err != nil {
			return // not FEEL the engine compiles: nothing to agree with
		}
		checked++
		want := slices.Clone(c.Inputs())
		slices.Sort(want)
		got := slices.Clone(names)
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("%s %s: %q reads %q, FEEL compiler %q", file, where, source, got, want)
		}
	}
	for _, f := range fs {
		for _, p := range f.Processes {
			for _, e := range p.Exprs {
				check(f.File, e.Element, e.Source, e.Names)
			}
			for id, m := range p.Messages {
				if m.CorrelationKey != nil {
					check(f.File, "message "+id, *m.CorrelationKey, m.KeyNames)
				}
			}
		}
	}
	t.Logf("compared %d expressions", checked)
	if checked < 200 {
		t.Fatalf("checked %d expressions; the corpus is not where this test looks", checked)
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
