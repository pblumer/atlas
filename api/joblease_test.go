package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// ADR-0007 (programme F), from the HTTP side: leasing a job over the API.
//
// The lease mechanics belong to the engine and are tested there. What is tested here is
// the surface a worker actually talks to — that a lease is granted once, refused while
// someone else holds it, and reported with the deadline the engine froze rather than one
// the handler recomputed.

// activate posts an activation for a job key and returns the status and decoded body.
func activate(t *testing.T, f *readyFixture, key uint64, body string) (int, map[string]any) {
	t.Helper()
	code, raw := f.x.do(http.MethodPost, "/api/v1/jobs/"+u64(key)+"/activate", body)
	var out map[string]any
	if len(raw) > 0 && strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
	}
	return code, out
}

// TestActivatingAnUnknownJobIs404: a worker racing a completion, a cancellation, or a
// boundary event asks for a job that is already gone. That is ordinary, and it must be
// distinguishable from "the server broke".
func TestActivatingAnUnknownJobIs404(t *testing.T) {
	f := newReadyFixture(t, true)
	code, _ := activate(t, f, 999999, `{"worker":"w1"}`)
	if code != http.StatusNotFound {
		t.Fatalf("POST activate on a missing job = %d, want 404", code)
	}
}

// TestActivatingRejectsAnUnreasonableLease. A lease is the only thing that returns a job
// from a worker that died, so one longer than a day is almost always a unit mix-up — and
// accepting it parks the job for that long.
func TestActivatingRejectsAnUnreasonableLease(t *testing.T) {
	f := newReadyFixture(t, true)
	code, _ := activate(t, f, 1, `{"worker":"w1","leaseMs":864000000}`) // 10 days
	if code != http.StatusBadRequest {
		t.Fatalf("POST activate with a 10-day lease = %d, want 400", code)
	}
}

// TestActivatingRejectsAMalformedBody keeps a typo from silently becoming a default
// lease under an anonymous worker.
func TestActivatingRejectsAMalformedBody(t *testing.T) {
	f := newReadyFixture(t, true)
	code, _ := activate(t, f, 1, `{"worker":`)
	if code != http.StatusBadRequest {
		t.Fatalf("POST activate with malformed JSON = %d, want 400", code)
	}
}

// TestLeasingAJobGrantsItOnceAndRefusesTheSecondWorker is the property the whole slice
// exists for: two workers pulling the same work do not both get it. The first is granted
// the lease with the deadline the engine froze; the second is told, plainly, that
// someone else holds it.
func TestLeasingAJobGrantsItOnceAndRefusesTheSecondWorker(t *testing.T) {
	h := newCompactionHarness(t, t.TempDir())
	h.deploy()
	h.create(1)

	var keys []uint64
	if err := h.store.AllActivatableJobs(func(k uint64) error {
		keys = append(keys, k)
		return nil
	}); err != nil {
		t.Fatalf("AllActivatableJobs: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("activatable jobs = %d, want 1", len(keys))
	}
	path := "/api/v1/jobs/" + u64(keys[0]) + "/activate"

	code, raw := h.x.do(http.MethodPost, path, `{"worker":"worker-1","leaseMs":60000}`)
	if code != http.StatusOK {
		t.Fatalf("first activation = %d body=%s, want 200", code, raw)
	}
	var granted map[string]any
	if err := json.Unmarshal(raw, &granted); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if granted["worker"] != "worker-1" {
		t.Errorf("worker = %v, want worker-1", granted["worker"])
	}
	// The deadline is the engine's, read back from committed state — a worker that
	// cannot trust it cannot know when its lease runs out.
	expiry, _ := granted["leaseExpiresAt"].(float64)
	if expiry <= 0 {
		t.Errorf("leaseExpiresAt = %v, want the instant the lease runs out", granted["leaseExpiresAt"])
	}

	// The job is now held: it is off the index a second worker would be served from.
	var still []uint64
	if err := h.store.AllActivatableJobs(func(k uint64) error {
		still = append(still, k)
		return nil
	}); err != nil {
		t.Fatalf("AllActivatableJobs: %v", err)
	}
	if len(still) != 0 {
		t.Errorf("activatable jobs = %v while leased, want none", still)
	}

	code, raw = h.x.do(http.MethodPost, path, `{"worker":"worker-2","leaseMs":60000}`)
	if code != http.StatusConflict {
		t.Fatalf("second activation = %d body=%s, want 409", code, raw)
	}
	if !strings.Contains(string(raw), "holds this job") {
		t.Errorf("409 body = %s, want it to say another worker holds the job", raw)
	}
}

// TestALeasedJobStillCompletes: leasing changes who is offered the job, not what
// finishing it does.
func TestALeasedJobStillCompletes(t *testing.T) {
	h := newCompactionHarness(t, t.TempDir())
	h.deploy()
	h.create(1)

	var keys []uint64
	if err := h.store.AllActivatableJobs(func(k uint64) error {
		keys = append(keys, k)
		return nil
	}); err != nil {
		t.Fatalf("AllActivatableJobs: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("activatable jobs = %d, want 1", len(keys))
	}
	if code, raw := h.x.do(http.MethodPost, "/api/v1/jobs/"+u64(keys[0])+"/activate",
		`{"worker":"worker-1"}`); code != http.StatusOK {
		t.Fatalf("activate = %d body=%s", code, raw)
	}
	if code, raw := h.x.do(http.MethodPost, "/api/v1/jobs/"+u64(keys[0])+"/complete", "{}"); code != http.StatusOK {
		t.Fatalf("complete a leased job = %d body=%s, want 200", code, raw)
	}
}

// u64 formats a job key for a URL without pulling strconv into every call site.
func u64(v uint64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
