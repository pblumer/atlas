package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func contentTool(t *testing.T, name string) Tool {
	t.Helper()
	for _, tl := range contentTools() {
		if tl.Name == name {
			return tl
		}
	}
	t.Fatalf("%s is not a content tool", name)
	return Tool{}
}

// refreshServer answers the start of a refresh as running and its progress as
// running for the first `runningReads` reads, then done.
func refreshServer(t *testing.T, startStatus int, runningReads int32) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var reads atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Query().Get("wait") == "false":
			w.WriteHeader(startStatus)
			_, _ = w.Write([]byte(`{"catalog":"cat_1","state":"running","total":2,"done":0,"lines":[]}`))
		case r.Method == http.MethodGet:
			if reads.Add(1) <= runningReads {
				_, _ = w.Write([]byte(`{"catalog":"cat_1","state":"running","total":2,"done":1,"lines":[{"item":"a","status":"refreshed"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"catalog":"cat_1","state":"done","total":2,"done":2,"lines":[{"item":"a","status":"refreshed"},{"item":"b","status":"refreshed"}]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	t.Cleanup(ts.Close)
	return ts, &reads
}

func fastPolling(t *testing.T, wait time.Duration) {
	t.Helper()
	oldWait, oldPoll := catalogueRefreshWait, refreshPoll
	catalogueRefreshWait, refreshPoll = wait, 5*time.Millisecond
	t.Cleanup(func() { catalogueRefreshWait, refreshPoll = oldWait, oldPoll })
}

// The refresh starts in the background and is followed to its end inside the wait.
func TestTheCatalogueRefreshIsFollowedToItsEnd(t *testing.T) {
	fastPolling(t, 5*time.Second)
	ts, reads := refreshServer(t, http.StatusAccepted, 3)
	text, err := contentTool(t, "atlas_refresh_catalog_content").Handler(NewClient(ts.URL), map[string]any{"id": "cat_1"})
	if err != nil || !strings.Contains(text, `"state":"done"`) || strings.Contains(text, "still running") {
		t.Fatalf("refresh = %q, %v", text, err)
	}
	if reads.Load() != 4 {
		t.Fatalf("progress read %d times, want 4", reads.Load())
	}
}

// One already running is followed rather than reported as an error.
func TestARunningCatalogueRefreshIsFollowedNotStartedAgain(t *testing.T) {
	fastPolling(t, 5*time.Second)
	ts, _ := refreshServer(t, http.StatusConflict, 1)
	text, err := contentTool(t, "atlas_refresh_catalog_content").Handler(NewClient(ts.URL), map[string]any{"id": "cat_1"})
	if err != nil || !strings.Contains(text, `"state":"done"`) {
		t.Fatalf("refresh = %q, %v", text, err)
	}
}

// A refresh that outlasts the wait answers its progress and says to follow it.
func TestACatalogueRefreshOutlastingTheWaitSaysHowToFollowIt(t *testing.T) {
	fastPolling(t, 30*time.Millisecond)
	ts, _ := refreshServer(t, http.StatusAccepted, 1_000_000)
	text, err := contentTool(t, "atlas_refresh_catalog_content").Handler(NewClient(ts.URL), map[string]any{"id": "cat_1"})
	if err != nil || !strings.Contains(text, "still running") || !strings.Contains(text, "atlas_catalog_refresh_status") ||
		!strings.Contains(text, `"done":1`) {
		t.Fatalf("refresh = %q, %v", text, err)
	}
	status, err := contentTool(t, "atlas_catalog_refresh_status").Handler(NewClient(ts.URL), map[string]any{"id": "cat_1"})
	if err != nil || !strings.Contains(status, `"state"`) {
		t.Fatalf("status = %q, %v", status, err)
	}
}

func TestWithTimeoutLeavesTheClientAsItWas(t *testing.T) {
	c := NewClient("http://127.0.0.1:1")
	long := c.withTimeout(time.Hour)
	if long.http.Timeout != time.Hour || c.http.Timeout != 30*time.Second {
		t.Fatalf("copy = %v, original = %v", long.http.Timeout, c.http.Timeout)
	}
	if long.http.Transport != c.http.Transport {
		t.Fatal("the copy does not share the transport")
	}
}
