package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Migrating instances between versions (ADR-0162), for the requests refused
// before anything is planned and the batch that meets a stopping server.

// TestMigrationRefusesARequestItCannotRead, for one instance and for a batch,
// before any instance is looked at.
func TestMigrationRefusesARequestItCannotRead(t *testing.T) {
	srv := newServerForErrors(t)
	key := processMovePathsDeploy(t, srv, "migrate-me")
	h := srv.Handler()

	code, body := recertifyHTTPPathsCall(t, h, http.MethodPost, "/api/v1/instances/1/migrate/plan", errReader{})
	if code != http.StatusBadRequest || !strings.Contains(body, "read body") {
		t.Errorf("plan, unreadable = %d %s, want 400", code, body)
	}
	code, body = recertifyHTTPPathsCall(t, h, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/migrate-instances", key),
		strings.NewReader(`{"targetProcessDefKey":`))
	if code != http.StatusBadRequest || !strings.Contains(body, "invalid JSON body") {
		t.Errorf("batch, malformed = %d %s, want 400", code, body)
	}
}

// TestMigrationABatchOnAStoppingServerIsAFault. The instances could not be
// selected, so the answer must not be "migrated 0, nothing remaining" — which is
// what a version with nothing on it looks like.
func TestMigrationABatchOnAStoppingServerIsAFault(t *testing.T) {
	srv, closeSrv := newOffLoopServer(t)
	h := srv.Handler()
	closeSrv()

	code, body := recertifyHTTPPathsCall(t, h, http.MethodPost, "/api/v1/processes/1/migrate-instances",
		strings.NewReader(`{"targetProcessDefKey":2,"reason":"move to v2"}`))
	if code != http.StatusInternalServerError || !strings.Contains(body, "migrate instances") {
		t.Errorf("batch = %d %s, want 500", code, body)
	}
}
