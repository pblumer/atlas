package promquery_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/pblumer/atlas/promquery"
)

func TestConfigEnabled(t *testing.T) {
	if (promquery.Config{}).Enabled() {
		t.Error("an empty config is enabled")
	}
	if (promquery.Config{URL: "   "}).Enabled() {
		t.Error("a blank URL is enabled")
	}
	if !(promquery.Config{URL: "http://prom:9090"}).Enabled() {
		t.Error("a configured URL is disabled")
	}
}

// matrix is a store's reply for one series.
const matrix = `{"status":"success","data":{"resultType":"matrix","result":[
  {"metric":{},"values":[[1700000000,"3"],[1700000060,"4.5"]]}]}}`

// TestQueryRangePostsTheQueryAndDecodesTheSeries is the wire format and the decode:
// the expression and the range go as a form body, and Prometheus's
// [number, "string"] pairs come back as samples.
func TestQueryRangePostsTheQueryAndDecodesTheSeries(t *testing.T) {
	var gotPath, gotAuth string
	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		io.WriteString(w, matrix)
	}))
	defer srv.Close()

	client := promquery.NewHTTPClient(promquery.Config{URL: srv.URL, Username: "u", Password: "p"})
	samples, err := client.QueryRange(context.Background(), `sum(rate(atlas_x[5m]))`, 1_700_000_000, 1_700_000_060, 60)
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if gotPath != "/api/v1/query_range" || gotAuth == "" {
		t.Errorf("path = %q, auth = %q", gotPath, gotAuth)
	}
	if gotForm.Get("query") != `sum(rate(atlas_x[5m]))` {
		t.Errorf("query = %q, want the caller's expression verbatim", gotForm.Get("query"))
	}
	if gotForm.Get("start") != "1700000000" || gotForm.Get("end") != "1700000060" ||
		gotForm.Get("step") != "60" {
		t.Errorf("range = %v", gotForm)
	}
	if len(samples) != 2 || samples[0].At != 1_700_000_000 || samples[0].Value != 3 ||
		samples[1].Value != 4.5 {
		t.Errorf("samples = %+v", samples)
	}
}

// TestQueryRangeTreatsNoSeriesAsAnAnswer. A query that matched nothing is the store
// saying it holds no such series — not a failure — and the difference is what the
// caller renders as empty rather than as an outage.
func TestQueryRangeTreatsNoSeriesAsAnAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"status":"success","data":{"resultType":"matrix","result":[]}}`)
	}))
	defer srv.Close()

	samples, err := promquery.NewHTTPClient(promquery.Config{URL: srv.URL}).
		QueryRange(context.Background(), "up", 1, 2, 1)
	if err != nil || len(samples) != 0 {
		t.Errorf("samples = %+v, err = %v; want an empty answer and no failure", samples, err)
	}
}

// TestQueryRangeSeparatesRefusedFromDownFromRejected. Prometheus reports its own
// failures in the body with a 200, so a client that only checked the status code
// would read an error as an empty landscape.
func TestQueryRangeSeparatesRefusedFromDownFromRejected(t *testing.T) {
	t.Run("refused", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer srv.Close()
		_, err := promquery.NewHTTPClient(promquery.Config{URL: srv.URL}).
			QueryRange(context.Background(), "up", 1, 2, 1)
		if !errors.Is(err, promquery.ErrQueryRefused) {
			t.Errorf("err = %v, want a refusal", err)
		}
	})

	t.Run("error in a 200 body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			io.WriteString(w, `{"status":"error","errorType":"bad_data","error":"parse error"}`)
		}))
		defer srv.Close()
		_, err := promquery.NewHTTPClient(promquery.Config{URL: srv.URL}).
			QueryRange(context.Background(), "((", 1, 2, 1)
		if err == nil {
			t.Fatal("an error body was read as success")
		}
		// The store's own sentence travels, so an operator can fix a query rather
		// than guess at one.
		if !strings.Contains(err.Error(), "parse error") {
			t.Errorf("err = %v, want the store's own reason", err)
		}
	})

	t.Run("server error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}))
		defer srv.Close()
		_, err := promquery.NewHTTPClient(promquery.Config{URL: srv.URL}).
			QueryRange(context.Background(), "up", 1, 2, 1)
		if err == nil || errors.Is(err, promquery.ErrQueryRefused) {
			t.Errorf("err = %v, want a plain failure", err)
		}
	})
}

// TestQueryRangeNeverNamesTheStore. A context answer is readable by anyone who may
// read the model, and the store's address is infrastructure they were not
// necessarily told about (ADR-0189 §6).
func TestQueryRangeNeverNamesTheStore(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	addr := srv.URL
	srv.Close()

	_, err := promquery.NewHTTPClient(promquery.Config{URL: addr}).
		QueryRange(context.Background(), "up", 1, 2, 1)
	if err == nil {
		t.Fatal("a dead store answered")
	}
	if strings.Contains(err.Error(), strings.TrimPrefix(addr, "http://")) {
		t.Errorf("the error names the store: %v", err)
	}
}

// TestQueryRangeSkipsUnreadableSamples. NaN is what Prometheus returns for a step it
// cannot evaluate — a real reading of "nothing here" — so the step is dropped
// rather than failing the series it sits in.
func TestQueryRangeSkipsUnreadableSamples(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"status":"success","data":{"resultType":"matrix","result":[
		  {"metric":{},"values":[[1,"NaN"],[2,"7"]]}]}}`)
	}))
	defer srv.Close()

	samples, err := promquery.NewHTTPClient(promquery.Config{URL: srv.URL}).
		QueryRange(context.Background(), "up", 1, 2, 1)
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if len(samples) != 1 || samples[0].Value != 7 {
		t.Errorf("samples = %+v, want the unreadable step dropped and the rest kept", samples)
	}
}

// TestQueryRangeBoundsTheResponse. Every query here is an aggregate over a bounded
// number of steps, so a large body means a store answering something other than
// what was asked.
func TestQueryRangeBoundsTheResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, strings.Repeat("x", (1<<20)+1))
	}))
	defer srv.Close()

	if _, err := promquery.NewHTTPClient(promquery.Config{URL: srv.URL}).
		QueryRange(context.Background(), "up", 1, 2, 1); err == nil {
		t.Error("a body past the bound was read in full")
	}
}

// TestEscapeLabelValue. Every value matched on is operator configuration rather
// than user input, but a quote in a base URL would still produce a query meaning
// something other than intended, and a malformed one is the better failure.
func TestEscapeLabelValue(t *testing.T) {
	for in, want := range map[string]string{
		`plain.host`:   `plain.host`,
		`a"b`:          `a\"b`,
		`a\b`:          `a\\b`,
		"a\nb":         `a\nb`,
		`"} or up{x="`: `\"} or up{x=\"`,
	} {
		if got := promquery.EscapeLabelValue(in); got != want {
			t.Errorf("EscapeLabelValue(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestQueryRangeRejectsAnUnreadableAnswer. A store behind a proxy answers HTML, and
// a client that shrugged at it would report an empty landscape instead of a lookup
// that did not work.
func TestQueryRangeRejectsAnUnreadableAnswer(t *testing.T) {
	for name, body := range map[string]string{
		"not json":           `<html>proxy error</html>`,
		"bad timestamp":      `{"status":"success","data":{"result":[{"values":[["nope","1"]]}]}}`,
		"bad value type":     `{"status":"success","data":{"result":[{"values":[[1,2]]}]}}`,
		"unparseable number": `{"status":"success","data":{"result":[{"values":[[1,"twelve"]]}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				io.WriteString(w, body)
			}))
			defer srv.Close()
			if _, err := promquery.NewHTTPClient(promquery.Config{URL: srv.URL}).
				QueryRange(context.Background(), "up", 1, 2, 1); err == nil {
				t.Error("an unreadable answer was accepted as data")
			}
		})
	}
}

// TestQueryRangeRefusesAStepOfZero. The step reaches Prometheus as a duration, and
// zero is a query it rejects — worse, one that would look like the store having
// nothing rather than like a request that was never valid.
func TestQueryRangeRefusesAStepOfZero(t *testing.T) {
	var gotStep string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		gotStep = form.Get("step")
		io.WriteString(w, matrix)
	}))
	defer srv.Close()

	if _, err := promquery.NewHTTPClient(promquery.Config{URL: srv.URL}).
		QueryRange(context.Background(), "up", 1, 2, 0); err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if gotStep != "1" {
		t.Errorf("step = %q, want a zero step floored to something the store accepts", gotStep)
	}
}

// TestQueryRangeReportsAnUnusableEndpoint rather than panicking on it: a base URL is
// operator configuration, and a typo in one has to say so at the first query.
func TestQueryRangeReportsAnUnusableEndpoint(t *testing.T) {
	if _, err := promquery.NewHTTPClient(promquery.Config{URL: "http://\x7f/"}).
		QueryRange(context.Background(), "up", 1, 2, 1); err == nil {
		t.Error("an unusable endpoint produced no error")
	}
}
