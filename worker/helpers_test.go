package worker_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func post(t *testing.T, ts *httptest.Server, path, body, contentType string) []byte {
	t.Helper()
	resp, err := http.Post(ts.URL+path, contentType, strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s: status=%d body=%s", path, resp.StatusCode, out)
	}
	return out
}

func get(t *testing.T, ts *httptest.Server, path string) []byte {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status=%d body=%s", path, resp.StatusCode, out)
	}
	return out
}

func runningInstances(t *testing.T, ts *httptest.Server) int {
	t.Helper()
	var insts []struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(get(t, ts, "/api/v1/instances"), &insts); err != nil {
		t.Fatalf("decode instances: %v", err)
	}
	n := 0
	for _, in := range insts {
		if in.State == "active" {
			n++
		}
	}
	return n
}

type activatableJob struct {
	Key     uint64 `json:"key"`
	Retries int32  `json:"retries"`
}

func instanceJobs(t *testing.T, ts *httptest.Server) []activatableJob {
	t.Helper()
	var insts []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(get(t, ts, "/api/v1/instances"), &insts); err != nil {
		t.Fatalf("decode instances: %v", err)
	}
	if len(insts) == 0 {
		t.Fatal("no instances")
	}
	var jobs []activatableJob
	if err := json.Unmarshal(get(t, ts, "/api/v1/instances/"+itoa(insts[0].Key)+"/jobs"), &jobs); err != nil {
		t.Fatalf("decode jobs: %v", err)
	}
	return jobs
}

func itoa(v uint64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func lookPath(name string) (string, error) { return exec.LookPath(name) }

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
