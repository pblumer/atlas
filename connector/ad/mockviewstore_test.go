package ad_test

import (
	"testing"

	"github.com/pblumer/atlas/connector/ad"
)

// The server side of the same feature: what an Atlas keeps of the snapshots its mock
// workers report, and shows in the Console. It is runtime state and nothing else —
// memory, no event, no log, gone on restart — for the reason the preview outbox is
// (ADR-0150): none of it ever existed anywhere but in a worker's memory either.

// A worker reports its whole directory each time, so the newest snapshot replaces the
// last one rather than accumulating. Two workers are two directories.
func TestMockViewKeepsTheNewestSnapshotPerWorker(t *testing.T) {
	v := ad.NewMockView(0)
	v.Put(ad.MockSnapshot{Worker: "ad-1", Seeded: 1})
	v.Put(ad.MockSnapshot{Worker: "ad-1", Seeded: 2})
	v.Put(ad.MockSnapshot{Worker: "ad-2", Seeded: 9})

	got := v.Snapshots()
	if len(got) != 2 {
		t.Fatalf("snapshots = %d, want one per worker", len(got))
	}
	// Sorted by worker, so a polling view does not reshuffle under the reader.
	if got[0].Worker != "ad-1" || got[1].Worker != "ad-2" {
		t.Fatalf("workers = %q, %q; want them sorted", got[0].Worker, got[1].Worker)
	}
	if got[0].Seeded != 2 {
		t.Errorf("ad-1 seeded = %d, want the newest report's 2", got[0].Seeded)
	}
}

// The arrival time is the view's to assign, not the reporter's: a worker whose clock
// is wrong — or which chose to say it reported in the future — must not be able to
// make its snapshot look fresher than another's.
func TestMockViewStampsTheArrivalTimeItself(t *testing.T) {
	now := int64(1_700_000_000_000_000_000)
	v := ad.NewMockViewClock(0, func() int64 { return now })
	v.Put(ad.MockSnapshot{Worker: "ad-1", At: 42})

	got := v.Snapshots()
	if len(got) != 1 || got[0].At != now {
		t.Fatalf("at = %v, want the view's own clock %d", got, now)
	}
}

// A snapshot from a worker that did not name itself is still worth keeping — an
// external worker is configured by hand and may not have been given an id — so it is
// filed under a name that says so rather than dropped.
func TestMockViewNamesAnAnonymousReporter(t *testing.T) {
	v := ad.NewMockView(0)
	v.Put(ad.MockSnapshot{Worker: "  "})

	got := v.Snapshots()
	if len(got) != 1 || got[0].Worker == "" {
		t.Fatalf("snapshots = %v, want one filed under a stand-in name", got)
	}
}

// Capacity is a bound on workers, and the least recently heard from is dropped: a
// server whose workers are restarted often must not grow a directory per dead child.
func TestMockViewDropsTheStalestWorkerAtCapacity(t *testing.T) {
	tick := int64(0)
	v := ad.NewMockViewClock(2, func() int64 { tick++; return tick })
	v.Put(ad.MockSnapshot{Worker: "old"})
	v.Put(ad.MockSnapshot{Worker: "middle"})
	v.Put(ad.MockSnapshot{Worker: "new"})

	got := v.Snapshots()
	if len(got) != 2 {
		t.Fatalf("snapshots = %d, want the capacity of 2", len(got))
	}
	for _, s := range got {
		if s.Worker == "old" {
			t.Fatalf("kept the stalest worker: %v", got)
		}
	}
}

// A worker that reports again is heard from again, so it is not the *first* report
// that decides who is dropped. Otherwise a busy worker registered early would be
// evicted while an idle one that never reports again survives.
func TestMockViewCapacityFollowsTheNewestReport(t *testing.T) {
	tick := int64(0)
	v := ad.NewMockViewClock(2, func() int64 { tick++; return tick })
	v.Put(ad.MockSnapshot{Worker: "busy"})
	v.Put(ad.MockSnapshot{Worker: "idle"})
	v.Put(ad.MockSnapshot{Worker: "busy", Seeded: 1})
	v.Put(ad.MockSnapshot{Worker: "third"})

	kept := map[string]bool{}
	for _, s := range v.Snapshots() {
		kept[s.Worker] = true
	}
	if !kept["busy"] || !kept["third"] || kept["idle"] {
		t.Fatalf("kept %v, want busy and third — idle is the stalest report", kept)
	}
}
