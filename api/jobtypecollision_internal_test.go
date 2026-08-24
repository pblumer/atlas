package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/api/sidecar"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/jobtype"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// A store whose job-type table has names on indices a built-in has since taken is a
// condition an operator has to be able to see. The startup warning and the offline
// command both say it, but neither reaches someone whose server runs in a container
// on a machine they would rather not shell into — so the view says it too.

type collisionResp struct {
	Collisions []struct {
		Name     string `json:"name"`
		Index    int32  `json:"index"`
		NowMeans string `json:"nowMeans"`
	} `json:"jobTypeCollisions"`
}

// serverWithSeededJobTypes builds a server whose job-type table was written before
// this build's reserved range reached the given indices.
func serverWithSeededJobTypes(t *testing.T, entries ...jobtype.Entry) *Server {
	t.Helper()
	dir := t.TempDir()
	if len(entries) > 0 {
		st, err := sidecar.NewStore(filepath.Join(dir, "jobtypes"), "job types",
			func(e jobtype.Entry) string { return e.Name })
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		for _, e := range entries {
			if err := st.Save(e); err != nil {
				t.Fatalf("Save(%+v): %v", e, err)
			}
		}
	}
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
	srv, err := New(proc, store, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		srv.Close()
		_ = store.Close()
		_ = log.Close()
	})
	return srv
}

func collisions(t *testing.T, srv *Server) collisionResp {
	t.Helper()
	code, raw := serveInternal(t, srv, http.MethodGet, "/api/v1/workers", "", "")
	if code != http.StatusOK {
		t.Fatalf("GET /workers: status=%d body=%s", code, raw)
	}
	var out collisionResp
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode workers: %v (%s)", err, raw)
	}
	return out
}

// The payoff: the condition is answerable from the browser, with the name and what
// its index means now — which is what says where the parked jobs would go.
func TestTheViewNamesAJobTypeOnANowReservedIndex(t *testing.T) {
	// Index 0 is reserved in every build, so this stands for any name issued before
	// the range grew over it.
	srv := serverWithSeededJobTypes(t, jobtype.Entry{Name: "send-email", Index: 0})

	got := collisions(t, srv)
	if len(got.Collisions) != 1 {
		t.Fatalf("collisions = %+v, want the one seeded name", got.Collisions)
	}
	c := got.Collisions[0]
	if c.Name != "send-email" || c.Index != 0 {
		t.Errorf("collision = %+v, want send-email at 0", c)
	}
	if c.NowMeans != compiler.ReservedJobTypes()[0] {
		t.Errorf("nowMeans = %q, want the built-in holding index 0", c.NowMeans)
	}
}

// A healthy store says nothing, and says it as an empty list rather than a null: the
// console iterates this, and a card that appeared on every server would train an
// operator to ignore it.
func TestAHealthyStoreReportsNoJobTypeCollisions(t *testing.T) {
	srv := serverWithSeededJobTypes(t)

	code, raw := serveInternal(t, srv, http.MethodGet, "/api/v1/workers", "", "")
	if code != http.StatusOK {
		t.Fatalf("GET /workers: status=%d body=%s", code, raw)
	}
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode workers: %v", err)
	}
	if string(resp["jobTypeCollisions"]) != "[]" {
		t.Errorf("jobTypeCollisions = %s, want []", resp["jobTypeCollisions"])
	}
}

// A stored name above the reserved range is an ordinary assignment and must not be
// reported — that is every healthy store's normal state.
func TestAnOrdinaryDynamicAssignmentIsNotReported(t *testing.T) {
	srv := serverWithSeededJobTypes(t,
		jobtype.Entry{Name: "send-email", Index: compiler.FirstDynamicJobTypeIndex()})

	if got := collisions(t, srv); len(got.Collisions) != 0 {
		t.Errorf("collisions = %+v, want none", got.Collisions)
	}
}
