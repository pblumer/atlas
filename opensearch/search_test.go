package opensearch_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/opensearch"
)

// TestSearchPostsToTheIndexWithCredentials is the wire format: a POST of the query
// body to <index>/_search, JSON content type, basic auth when configured.
func TestSearchPostsToTheIndexWithCredentials(t *testing.T) {
	var gotPath, gotCT, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotCT, gotAuth = r.URL.Path, r.Header.Get("Content-Type"), r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		io.WriteString(w, `{"aggregations":{}}`)
	}))
	defer srv.Close()

	client := opensearch.NewHTTPClient(opensearch.Config{
		URL: srv.URL, Username: "u", Password: "p",
	})
	body, err := client.Search(context.Background(), "atlas-events", []byte(`{"size":0}`))
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if gotPath != "/atlas-events/_search" {
		t.Errorf("path = %q", gotPath)
	}
	if gotCT != "application/json" || gotAuth == "" {
		t.Errorf("content-type = %q, auth = %q", gotCT, gotAuth)
	}
	if gotBody != `{"size":0}` {
		t.Errorf("body = %q, want the caller's query verbatim", gotBody)
	}
	if string(body) != `{"aggregations":{}}` {
		t.Errorf("body = %q", body)
	}

	// An unnamed index falls back to the one the exporter writes: reading somewhere
	// other than where Atlas wrote would answer about a different history.
	if _, err := client.Search(context.Background(), "", []byte(`{}`)); err != nil {
		t.Fatalf("search with default index: %v", err)
	}
	if gotPath != "/"+opensearch.DefaultIndex+"/_search" {
		t.Errorf("default index path = %q", gotPath)
	}
}

// TestSearchSeparatesRefusedFromDownFromEmpty. The three send an operator to three
// different places — the credentials, the network, and nowhere at all — so the
// client must not flatten them into one error.
func TestSearchSeparatesRefusedFromDownFromEmpty(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   func(body []byte, err error) string
	}{
		{"unauthorized", http.StatusUnauthorized, func(_ []byte, err error) string {
			if !errors.Is(err, opensearch.ErrSearchRefused) {
				return "401 did not report a refusal"
			}
			return ""
		}},
		{"forbidden", http.StatusForbidden, func(_ []byte, err error) string {
			if !errors.Is(err, opensearch.ErrSearchRefused) {
				return "403 did not report a refusal"
			}
			return ""
		}},
		// An index that is not there yet is not a fault. Nothing has been exported, or
		// it was rotated away; either way the honest answer is an empty one.
		{"missing index", http.StatusNotFound, func(body []byte, err error) string {
			if err != nil || body != nil {
				return "404 was reported as a failure rather than as nothing to show"
			}
			return ""
		}},
		{"server error", http.StatusInternalServerError, func(_ []byte, err error) string {
			if err == nil || errors.Is(err, opensearch.ErrSearchRefused) {
				return "500 was not reported as a plain failure"
			}
			return ""
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()
			body, err := opensearch.NewHTTPClient(opensearch.Config{URL: srv.URL}).
				Search(context.Background(), "i", []byte(`{}`))
			if complaint := tc.want(body, err); complaint != "" {
				t.Errorf("%s (err = %v)", complaint, err)
			}
		})
	}
}

// TestSearchNeverNamesTheCluster. A context answer is readable by anyone who may
// read the model, and the address of the log store is infrastructure they were not
// necessarily told about. A transport error must carry the fault without carrying
// the endpoint (ADR-0189 §6).
func TestSearchNeverNamesTheCluster(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now

	_, err := opensearch.NewHTTPClient(opensearch.Config{URL: url}).
		Search(context.Background(), "i", []byte(`{}`))
	if err == nil {
		t.Fatal("a dead cluster answered")
	}
	host := strings.TrimPrefix(url, "http://")
	if strings.Contains(err.Error(), host) || strings.Contains(err.Error(), url) {
		t.Errorf("the error names the cluster: %v", err)
	}
}

// TestSearchBoundsTheResponse. The queries this serves are aggregations with no
// documents requested, so a correct answer is small. A large body means a cluster
// answering something other than what was asked, and reading it all into memory to
// find that out is the failure this prevents.
func TestSearchBoundsTheResponse(t *testing.T) {
	var size int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, strings.Repeat("x", size))
	}))
	defer srv.Close()
	client := opensearch.NewHTTPClient(opensearch.Config{URL: srv.URL})

	size = 1 << 20 // exactly at the bound: a correct answer, not a truncated one
	body, err := client.Search(context.Background(), "i", []byte(`{}`))
	if err != nil || len(body) != size {
		t.Fatalf("a body exactly at the bound was rejected: %d bytes, %v", len(body), err)
	}

	size = (1 << 20) + 1
	if _, err := client.Search(context.Background(), "i", []byte(`{}`)); err == nil {
		t.Error("a body past the bound was read in full")
	}
}

// TestSearchHonoursTheCallersDeadline. The bound on how long somebody else's
// cluster may hold up a page of ours belongs to the caller, not to this package.
func TestSearchHonoursTheCallersDeadline(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := opensearch.NewHTTPClient(opensearch.Config{URL: srv.URL}).
		Search(ctx, "i", []byte(`{}`)); err == nil {
		t.Error("a cancelled search waited on the cluster")
	}
}
