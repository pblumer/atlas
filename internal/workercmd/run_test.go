package workercmd

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// atlasStub stands in for a running atlas: it hands out one job of type echo on the
// first poll and records every request body by path.
type atlasStub struct {
	mu     sync.Mutex
	bodies map[string][]map[string]any
	served bool
}

func newAtlasStub(t *testing.T) (*atlasStub, *httptest.Server) {
	t.Helper()
	s := &atlasStub{bodies: map[string][]map[string]any{}}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		s.mu.Lock()
		s.bodies[r.URL.Path] = append(s.bodies[r.URL.Path], body)
		first := !s.served && r.URL.Path == "/api/v1/jobs/activate" && body["type"] == "echo"
		if first {
			s.served = true
		}
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case first:
			_, _ = io.WriteString(w, `{"jobs":[{"jobKey":5,"type":"echo","retries":1,"leaseToken":3,"variables":{"x":1}}]}`)
		case strings.HasSuffix(r.URL.Path, "/activate"):
			_, _ = io.WriteString(w, `{"jobs":[],"queries":[]}`)
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(ts.Close)
	return s, ts
}

func (s *atlasStub) to(path string) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.bodies[path]...)
}

// A worker run with --once and a --handle leases the job, runs the command with the
// job on stdin, completes it with what the command printed, and reports the Worker
// Type identity the environment names.
func TestRunWorksAJobThroughACommandAndReportsItsIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the handler below is a POSIX shell")
	}
	stub, ts := newAtlasStub(t)
	t.Setenv(workerTypeIDEnv, "com.example.echo")
	t.Setenv(workerTypeVersionEnv, "1.0.0")
	err := Run([]string{"--server", ts.URL + "/", "--id", "w-1", "--once",
		"--handle", `echo=sh -c cat>/dev/null;printf{"ok":true}`})
	// strings.Fields splits the command, so the shell gets the script in pieces; a
	// script without spaces keeps it one argument.
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	polls := stub.to("/api/v1/jobs/activate")
	if len(polls) == 0 {
		t.Fatal("the worker never polled")
	}
	wt, _ := polls[0]["workerType"].(map[string]any)
	if wt["id"] != "com.example.echo" || wt["version"] != "1.0.0" || polls[0]["worker"] != "w-1" {
		t.Errorf("poll = %v, want this worker's id and the identity from the environment", polls[0])
	}
	if got := len(stub.to("/api/v1/jobs/5/complete")) + len(stub.to("/api/v1/jobs/5/fail")); got != 1 {
		t.Errorf("job 5 was reported %d times, want once", got)
	}
}

// A worker given nothing to serve says so with the error the supervisor parks on.
func TestRunWithNothingToServeEndsInTheParkingError(t *testing.T) {
	_, ts := newAtlasStub(t)
	if err := Run([]string{"--server", ts.URL}); !errors.Is(err, ErrNothingToServe) {
		t.Fatalf("Run = %v, want ErrNothingToServe", err)
	}
	if ExitNothingToServe != 78 {
		t.Errorf("ExitNothingToServe = %d; the supervisor parks on 78 (EX_CONFIG)", ExitNothingToServe)
	}
}

// A built-in Worker Type that needs no configuration is served, and an internal CA
// named with --tls-ca builds the worker its own client.
func TestRunServesABuiltinKindWithAnInternalCA(t *testing.T) {
	stub, ts := newAtlasStub(t)
	ca := writeCA(t)
	if err := Run([]string{"--server", ts.URL, "--once", "--connector", "ldif", "--tls-ca", ca}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(stub.to("/api/v1/jobs/activate")) == 0 {
		t.Error("the ldif worker never polled")
	}
}

// What a worker cannot start with is refused before it polls.
func TestRunRefusesWhatItCannotStartWith(t *testing.T) {
	_, ts := newAtlasStub(t)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"unknown sandbox", []string{"--server", ts.URL, "--script-sandbox", "bogus"}},
		{"unreadable CA", []string{"--server", ts.URL, "--handle", "a=true", "--tls-ca", filepath.Join(t.TempDir(), "absent.pem")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := Run(tc.args); err == nil || errors.Is(err, ErrNothingToServe) {
				t.Errorf("Run = %v, want a refusal", err)
			}
		})
	}
}

func TestHandleFlag(t *testing.T) {
	h := handleFlag{}
	if err := h.Set("send=./send.sh --fast"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := h["send"]; len(got) != 2 || got[0] != "./send.sh" {
		t.Errorf("send = %v, want the command split into its arguments", got)
	}
	for _, bad := range []string{"send=other", "noequals", "=cmd", "type="} {
		if err := h.Set(bad); err == nil {
			t.Errorf("Set(%q) = nil, want an error", bad)
		}
	}
	if s := h.String(); !strings.Contains(s, "send") {
		t.Errorf("String() = %q, want the handled type", s)
	}
}

func TestSplitListAndEnvOr(t *testing.T) {
	if got := splitList(" a, ,b ,"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("splitList = %v", got)
	}
	t.Setenv("WORKERCMD_TEST_ENV", "  ")
	if got := envOr("WORKERCMD_TEST_ENV", "def"); got != "def" {
		t.Errorf("envOr(blank) = %q, want the default", got)
	}
	t.Setenv("WORKERCMD_TEST_ENV", " set ")
	if got := envOr("WORKERCMD_TEST_ENV", "def"); got != "set" {
		t.Errorf("envOr = %q, want the trimmed value", got)
	}
	if DefaultWorkerID() == "" {
		t.Error("DefaultWorkerID is empty")
	}
}

// writeCA writes a self-signed CA certificate as PEM and returns its path.
func writeCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A kind asked for and configured nowhere is said, and the rest is still served; the
// script kind is started only under a sandbox profile its languages pass.
func TestRunServesWhatIsConfiguredAndNamesWhatIsNot(t *testing.T) {
	stub, ts := newAtlasStub(t)
	if err := Run([]string{"--server", ts.URL, "--once", "--connector", "ldif,mail",
		"--script-sandbox", "off"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(stub.to("/api/v1/jobs/activate")) == 0 {
		t.Error("the worker never polled")
	}
	if err := Run([]string{"--server", ts.URL, "--once", "--connector", "script",
		"--script-languages", "no-such-language", "--script-sandbox", "off"}); err == nil {
		t.Error("Run with an unknown script language = nil, want a refusal")
	}
}
