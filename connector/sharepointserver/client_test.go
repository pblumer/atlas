package sharepointserver

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/pblumer/atlas/connector/sharepointserver/catalog"
	"github.com/pblumer/atlas/job"
)

type fakeRunner struct {
	bin, stdout, stderr string
	args, env           []string
	err                 error
	block               bool
}

func (f *fakeRunner) Run(ctx context.Context, bin string, args, env []string) ([]byte, []byte, error) {
	f.bin, f.args, f.env = bin, args, env
	if f.block {
		<-ctx.Done()
		return nil, nil, ctx.Err()
	}
	return []byte(f.stdout), []byte(f.stderr), f.err
}

func (f *fakeRunner) value(name string) (string, bool) {
	for _, kv := range f.env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v, true
		}
	}
	return "", false
}

func client(t *testing.T, r *fakeRunner) *Client {
	t.Helper()
	c, err := NewClient("http://sp01.contoso.local/", "", Options{})
	if err != nil {
		t.Fatal(err)
	}
	c.run = r
	c.environ = func() []string { return []string{"PATH=/bin", "ATLAS_TOKEN=secret"} }
	return c
}

// TestTheBootstrapIsFixedAndValuesAreData: PowerShell is started with the program this
// package ships, never with anything a model authored, and the worker's own token does
// not reach it.
func TestTheBootstrapIsFixedAndValuesAreData(t *testing.T) {
	r := &fakeRunner{stdout: `{"ok":true,"value":[{"Url":"http://x"}]}`}
	c := client(t, r)
	out, err := c.Invoke(context.Background(), catalog.Invocation{Op: "get-site", Site: "alpha; Remove-SPSite", Args: map[string]any{}})
	if err != nil || len(out) != 1 {
		t.Fatalf("out = %v, err = %v", out, err)
	}
	if r.bin != "powershell.exe" || len(r.args) != 5 || r.args[3] != "-EncodedCommand" {
		t.Fatalf("started %q %v", r.bin, r.args)
	}
	raw, _ := base64.StdEncoding.DecodeString(r.args[4])
	u := make([]uint16, len(raw)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(raw[2*i:])
	}
	if string(utf16.Decode(u)) != starter {
		t.Error("the command line is not the starter")
	}
	if b, _ := r.value(bootstrapEnv); b != bootstrap {
		t.Error("the program the starter runs is not the bootstrap")
	}
	if _, ok := r.value("ATLAS_TOKEN"); ok {
		t.Error("the worker's token reached the child")
	}
	reqRaw, _ := r.value("ATLAS_SHAREPOINTSERVER_REQUEST")
	var req request
	if err := json.Unmarshal([]byte(reqRaw), &req); err != nil {
		t.Fatal(err)
	}
	if req.SiteURL != "http://sp01.contoso.local/sites/alpha; Remove-SPSite" || req.SitePath != "/sites/alpha; Remove-SPSite" || req.WebApplication != "http://sp01.contoso.local" {
		t.Errorf("request = %+v", req)
	}
}

// Windows holds at most 32,767 characters in a command line and in one environment
// variable. The command line carries the encoded starter, the environment the bootstrap.
func TestTheBootstrapFitsWhereItTravels(t *testing.T) {
	const windowsLimit = 32767
	if n := len(encodeCommand(starter)) + 100; n > windowsLimit {
		t.Errorf("command line of %d characters", n)
	}
	if n := len([]rune(bootstrap)); n > windowsLimit-len(bootstrapEnv)-1 {
		t.Errorf("the bootstrap has %d characters; an environment variable holds %d", n, windowsLimit)
	}
}

func TestInvokeReportsEveryWayItCanFail(t *testing.T) {
	inv := catalog.Invocation{Op: "get-site", Site: "a", Args: map[string]any{}}
	for _, tc := range []struct {
		name string
		r    *fakeRunner
		want string
	}{
		{"not json", &fakeRunner{stdout: "hello"}, "not the bootstrap's answer: hello"},
		{"bad json", &fakeRunner{stdout: "{nope"}, "not the bootstrap's answer"},
		{"no pwsh", &fakeRunner{err: errors.New("not found"), stderr: "boom"}, "could not run the call"},
		{"no value", &fakeRunner{stdout: `{"ok":true}`}, `without a "value" list`},
		{"no reason", &fakeRunner{stdout: `{"ok":false}`}, "without saying why"},
		{"plain", &fakeRunner{stdout: `{"ok":false,"error":{"message":"nein"}}`}, "failed: nein"},
	} {
		if _, err := client(t, tc.r).Invoke(context.Background(), inv); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	c := client(t, &fakeRunner{block: true})
	c.timeout = time.Millisecond
	if _, err := c.Invoke(context.Background(), inv); err == nil || !strings.Contains(err.Error(), "did not finish") {
		t.Errorf("timeout: %v", err)
	}
	long := &fakeRunner{stdout: "text " + strings.Repeat("x", 2<<20)}
	if _, err := client(t, long).Invoke(context.Background(), inv); err == nil || !strings.Contains(err.Error(), "…") {
		t.Errorf("a long quote is cut: %v", err)
	}
}

func TestARefusalIsABPMNErrorOnlyWhenCaught(t *testing.T) {
	reg := NewRegistry()
	r := &fakeRunner{stdout: `{"ok":false,"error":{"code":"SHAREPOINT_SERVER_SITE_EXISTS","message":"da"}}`}
	reg.Register("f", client(t, r))
	j := Job{Connector: "f", Operation: "get-site", Parameters: map[string]any{"Site": "a"}, ResultVariable: "s", ErrorCodes: []string{CodeSiteExists}}
	_, err := Run(context.Background(), j, reg)
	var be *job.BPMNError
	if !errors.As(err, &be) || be.Code != CodeSiteExists {
		t.Fatalf("err = %#v", err)
	}
	j.ErrorCodes = nil
	if _, err := Run(context.Background(), j, reg); errors.As(err, &be) || err == nil {
		t.Errorf("uncaught refusal should stay a failure: %#v", err)
	}
	if len(Codes) != 7 {
		t.Errorf("codes changed without the handbook: %d", len(Codes))
	}
}

func TestRunShapesTheResult(t *testing.T) {
	reg := NewRegistry()
	r := &fakeRunner{}
	reg.Register("f", client(t, r))
	run := func(j Job, stdout string) (map[string]any, error) {
		r.stdout = stdout
		return Run(context.Background(), j, reg)
	}
	get := Job{Connector: "f", Operation: "get-site", Parameters: map[string]any{"Site": "a"}, ResultVariable: "s"}
	if vars, err := run(get, `{"ok":true,"value":[]}`); err != nil || vars["s"] != nil {
		t.Errorf("no site is null: %v %v", vars, err)
	}
	if vars, err := run(get, `{"ok":true,"value":[{"Url":"u"}]}`); err != nil || vars["s"].(map[string]any)["Url"] != "u" {
		t.Errorf("one site: %v %v", vars, err)
	}
	get.ResultVariable = ""
	if vars, _ := run(get, `{"ok":true,"value":[{}]}`); vars != nil {
		t.Errorf("no variable named: %v", vars)
	}
	list := Job{Connector: "f", Operation: "list-sites", ResultVariable: "l", Parameters: map[string]any{"Limit": json.Number("1")}}
	if vars, err := run(list, `{"ok":true,"value":[]}`); err != nil || len(vars["l"].([]any)) != 0 {
		t.Errorf("empty list: %v %v", vars, err)
	}
	if _, err := run(list, `{"ok":true,"value":[{},{}]}`); err == nil || !strings.Contains(err.Error(), "more than the limit") {
		t.Errorf("over the limit: %v", err)
	}
	if vars, err := run(Job{Connector: "f", Operation: "delete-site", Parameters: map[string]any{"Site": "a"}}, `{"ok":true,"value":[]}`); err != nil || vars != nil {
		t.Errorf("delete: %v %v", vars, err)
	}
	if _, err := Run(context.Background(), Job{Connector: "x", Operation: "get-site"}, reg); err == nil {
		t.Error("an unknown Worker fails")
	}
	if _, err := Run(context.Background(), Job{Connector: "f", Operation: "nope"}, reg); err == nil || !strings.Contains(err.Error(), "unknown operation") {
		t.Errorf("unknown op: %v", err)
	}
	if _, err := Run(context.Background(), Job{Connector: "f", Operation: "get-site"}, reg); err == nil {
		t.Error("a missing parameter fails before the farm")
	}
	if _, err := run(get, `{"ok":false}`); err == nil {
		t.Error("a failed call fails")
	}
}

func TestNewClientChecksItsConfiguration(t *testing.T) {
	for _, tc := range []struct{ endpoint, raw, want string }{
		{"", "", "web application"},
		{"ftp://x", "", "web application"},
		{"http://x/sites", "", "web application"},
		{"http://x", "{x", "syntax error"},
		{"http://x", "{", "expected shape"},
		{"http://x", `{"password":"p"}`, "no credential"},
		{"http://x", `[1]`, "expected shape"},
		{"http://x", `{"managedPath":"a/b"}`, "one path segment"},
		{"http://x", `{"timeoutSeconds":-1}`, "negative"},
	} {
		if _, err := NewClient(tc.endpoint, tc.raw, Options{}); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q %q: %v", tc.endpoint, tc.raw, err)
		}
	}
	c, err := NewClient("http://x", `{"managedPath":"/teams/","timeoutSeconds":5}`, Options{PowerShell: "pwsh"})
	if err != nil || c.path != "teams" || c.timeout != 5*time.Second || c.powershell != "pwsh" {
		t.Fatalf("c = %+v, err = %v", c, err)
	}
	if err := CheckBundle("http://x", ""); err != nil {
		t.Error(err)
	}
}

func TestCappedKeepsTheFirstBytes(t *testing.T) {
	c := &capped{max: 4}
	if n, _ := c.Write([]byte("ab")); n != 2 || c.over {
		t.Error("under the cap")
	}
	c.Write([]byte("cdef"))
	if c.String() != "abcd" || !c.over {
		t.Errorf("got %q", c.String())
	}
	c2 := &capped{max: 1}
	c2.Write([]byte("a"))
	c2.Write([]byte("b"))
	if !c2.over {
		t.Error("over after the cap")
	}
}

// TestExecRunnerRunsAProgram exercises the real runner against the test binary's host
// shell; on Windows the harness below stands in.
func TestExecRunnerRunsAProgram(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no sh on Windows")
	}
	out, _, err := execRunner{}.Run(context.Background(), "sh", []string{"-c", "echo hi"}, nil)
	if err != nil || strings.TrimSpace(string(out)) != "hi" {
		t.Errorf("out = %q, err = %v", out, err)
	}
	over := execRunner{}
	if _, _, err := over.Run(context.Background(), "sh", []string{"-c", "exit 3"}, nil); err == nil {
		t.Error("a failing program fails")
	}
}
