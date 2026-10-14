package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Which watches feed a message name (ADR-0235): a watch whose worker is gone, two
// workers publishing one name, a Jira watch that names no cursor field, and the
// stores the answer is read from failing.

// messageSourcesPathsList reads the route.
func messageSourcesPathsList(t *testing.T, s *Server) (int, []messageSourceView, string) {
	t.Helper()
	code, body := recertifyHTTPPathsCall(t, s.Handler(), http.MethodGet, "/api/v1/message-sources", nil)
	var out []messageSourceView
	if code == http.StatusOK {
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("decode: %v (%s)", err, body)
		}
	}
	return code, out, body
}

// TestMessageSourcesOneNameFedTwiceAndAWatchWithNoWorker. Both workers publishing
// "order.created" are listed, by worker name; the watch whose worker was deleted
// publishes nothing and is not listed as feeding anything.
func TestMessageSourcesOneNameFedTwiceAndAWatchWithNoWorker(t *testing.T) {
	srv := newServerForErrors(t)
	var err error
	srv.do(func() {
		for _, c := range []connector{
			{ID: "w-zeta", Name: "zeta", Kind: connectorKindJira, Enabled: true, CreatedAt: 1},
			{ID: "w-alpha", Name: "alpha", Kind: connectorKindJira, Enabled: true, CreatedAt: 2},
		} {
			if err = srv.connectors.Save(c); err != nil {
				return
			}
		}
		for _, sub := range []inboundSubscription{
			{ID: "s1", ConnectorID: "w-zeta", MessageName: "order.created", JQL: "project = Z", Enabled: true},
			{ID: "s2", ConnectorID: "w-alpha", MessageName: "order.created", JQL: "project = A", Enabled: true},
			{ID: "s3", ConnectorID: "w-deleted", MessageName: "order.cancelled", Enabled: true},
		} {
			if err = srv.inboundSubs.Save(sub); err != nil {
				return
			}
		}
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	code, got, body := messageSourcesPathsList(t, srv)
	if code != http.StatusOK {
		t.Fatalf("list = %d %s", code, body)
	}
	if len(got) != 2 || got[0].ConnectorName != "alpha" || got[1].ConnectorName != "zeta" {
		t.Fatalf("sources = %+v, want alpha then zeta and no orphaned watch", got)
	}
	// Neither watch names the field it follows, and the description says which one
	// it falls back to — the one the worker actually uses.
	if got[0].Description != "JQL project = A (on created)" {
		t.Errorf("description = %q", got[0].Description)
	}
}

// TestMessageSourcesAStoreThatCannotBeReadIsAFault. "Nothing feeds this name" from a
// store that could not be read would send an author looking for a fault in the
// model.
func TestMessageSourcesAStoreThatCannotBeReadIsAFault(t *testing.T) {
	for _, tc := range []struct {
		name string
		dir  func(*Server) string
	}{
		{"the watches", func(s *Server) string { return s.inboundSubs.Dir() }},
		{"the workers", func(s *Server) string { return s.connectors.Dir() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServerForErrors(t)
			recertifyHTTPPathsBreakDir(t, tc.dir(srv))
			if code, _, body := messageSourcesPathsList(t, srv); code != http.StatusInternalServerError ||
				!strings.Contains(body, "list message sources") {
				t.Errorf("list = %d %s, want 500", code, body)
			}
		})
	}
}
