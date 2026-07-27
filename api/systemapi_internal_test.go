package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// newSystemServer boots a server over a temp dir with the given options and an
// HTTP harness, for exercising the System endpoints (/logs, /info).
func newSystemServer(t *testing.T, opts ...Option) (*Server, deployTestHarness) {
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
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	srv, err := New(proc, store, dir, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { srv.Close(); _ = store.Close(); _ = log.Close() })
	return srv, deployTestHarness{t, srv.Handler()}
}

// TestLogBuffer covers the ring: multi-line writes, the newest-N cap, no-trailing
// -newline input, and the default size.
func TestLogBuffer(t *testing.T) {
	b := NewLogBuffer(3)
	b.Write([]byte("a\nb\n"))
	b.Write([]byte("c\n"))
	b.Write([]byte("d\n")) // "a" now falls off the back
	got := b.Lines()
	if strings.Join(got, ",") != "b,c,d" {
		t.Fatalf("lines = %v, want [b c d]", got)
	}
	// A write without a trailing newline is still one line.
	b2 := NewLogBuffer(0) // default size
	if b2.max != 1000 {
		t.Errorf("default max = %d, want 1000", b2.max)
	}
	b2.Write([]byte("no-newline"))
	if l := b2.Lines(); len(l) != 1 || l[0] != "no-newline" {
		t.Errorf("lines = %v, want [no-newline]", l)
	}
}

// TestLogsEndpoint: the /api/v1/logs endpoint returns the buffered lines, and
// reports an empty list when no buffer was wired.
func TestLogsEndpoint(t *testing.T) {
	buf := NewLogBuffer(10)
	buf.Write([]byte("powershell script worker enabled (pwsh found on PATH)\n"))
	srv, x := newSystemServer(t, WithLogBuffer(buf))
	_ = srv

	code, body := x.do(http.MethodGet, "/api/v1/logs", "")
	if code != http.StatusOK {
		t.Fatalf("GET /logs = %d: %s", code, body)
	}
	var resp struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Lines) != 1 || !strings.Contains(resp.Lines[0], "powershell script worker enabled") {
		t.Fatalf("lines = %v, want the worker startup line", resp.Lines)
	}

	// No buffer wired → empty list, not an error.
	_, x2 := newSystemServer(t)
	code, body = x2.do(http.MethodGet, "/api/v1/logs", "")
	if code != http.StatusOK {
		t.Fatalf("GET /logs (no buffer) = %d: %s", code, body)
	}
	if err := json.Unmarshal(body, &resp); err != nil || resp.Lines == nil {
		t.Fatalf("no-buffer lines = %v (err %v), want empty non-nil array", resp.Lines, err)
	}
}

// TestRunScript covers the script tester endpoint: a run against an enabled
// language returns its result; a disabled language, an unsupported language, and an
// empty script are reported as ok:false with a helpful message (not HTTP errors).
func TestRunScript(t *testing.T) {
	srv, x := newSystemServer(t, WithScriptWorker(compiler.PwshJobTypeIndex, stubExec{result: "Hallo Anna"}))
	_ = srv

	// Enabled language → runs and returns the result.
	code, body := x.do(http.MethodPost, "/api/v1/scripts/run", `{"language":"powershell","source":"x","variables":{"Vorname":"Anna"}}`)
	if code != http.StatusOK {
		t.Fatalf("run = %d: %s", code, body)
	}
	var resp runScriptResp
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.OK || resp.Result != "Hallo Anna" {
		t.Fatalf("run resp = %+v, want ok with result Hallo Anna", resp)
	}

	// A language with no registered worker is reported as not enabled.
	_, body = x.do(http.MethodPost, "/api/v1/scripts/run", `{"language":"python","source":"x"}`)
	_ = json.Unmarshal(body, &resp)
	if resp.OK || !strings.Contains(resp.Error, "not enabled") {
		t.Fatalf("disabled language resp = %+v, want not-enabled error", resp)
	}

	// An unsupported language is rejected.
	_, body = x.do(http.MethodPost, "/api/v1/scripts/run", `{"language":"ruby","source":"x"}`)
	_ = json.Unmarshal(body, &resp)
	if resp.OK || !strings.Contains(resp.Error, "unsupported") {
		t.Fatalf("unsupported language resp = %+v, want unsupported error", resp)
	}

	// An empty script is rejected before invoking the interpreter.
	_, body = x.do(http.MethodPost, "/api/v1/scripts/run", `{"language":"powershell","source":"   "}`)
	_ = json.Unmarshal(body, &resp)
	if resp.OK || !strings.Contains(resp.Error, "empty") {
		t.Fatalf("empty script resp = %+v, want empty-script error", resp)
	}
}

// TestInfoIncludesBuildMetadata: /api/v1/info reports the Go toolchain version (and
// the VCS fields, which are present when built from a checkout), so the web UI can
// show which build is running.
func TestInfoIncludesBuildMetadata(t *testing.T) {
	_, x := newSystemServer(t)
	code, body := x.do(http.MethodGet, "/api/v1/info", "")
	if code != http.StatusOK {
		t.Fatalf("GET /info = %d: %s", code, body)
	}
	var resp infoResp
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Product != "Atlas" {
		t.Errorf("product = %q, want Atlas", resp.Product)
	}
	if resp.Go == "" {
		t.Error("info.go (Go toolchain version) is empty, want it populated")
	}
}
