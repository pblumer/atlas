package panorama

import (
	"strings"
	"sync"
	"testing"
)

// observed builds a one-observation document at a moment.
func observed(at int64, state string) ObservationDocument {
	return ObservationDocument{
		ObservedAt: at,
		Observations: []Observation{{
			ElementID: "e-app", Key: KeyApplicationID, Value: "app-1",
			State: state, Severity: severityOf(state), Reason: "because " + state,
		}},
	}
}

// TestJournalRecordsTransitionsRatherThanSamples is the constraint ADR-0189 states
// in its own words — "Panorama remains a correlation surface, not a time-series or
// log database" — turned into behaviour. A hundred identical readings produce
// nothing; one change produces one entry. That is also the only shape anybody
// reads: nobody scrolls a graph of "healthy, healthy, healthy".
func TestJournalRecordsTransitionsRatherThanSamples(t *testing.T) {
	j := NewJournal()

	// The first read records nothing. Everything would otherwise arrive as a
	// transition from nothing, and a journal whose first page is always a full
	// inventory teaches a reader to skip the first page.
	j.Record("m1", observed(100, StateHealthy))
	if got := j.Document("m1"); len(got.Entries) != 0 {
		t.Fatalf("the first read journalled %+v", got.Entries)
	}

	for at := int64(110); at < 200; at += 10 {
		j.Record("m1", observed(at, StateHealthy))
	}
	if got := j.Document("m1"); len(got.Entries) != 0 {
		t.Fatalf("nine identical readings journalled %+v", got.Entries)
	}

	j.Record("m1", observed(200, StateDegraded))
	doc := j.Document("m1")
	if len(doc.Entries) != 1 {
		t.Fatalf("one change journalled %d entries: %+v", len(doc.Entries), doc.Entries)
	}
	entry := doc.Entries[0]
	// Both directions travel: healthy → degraded and degraded → healthy are the
	// same transition from opposite sides, and one is somebody's incident while the
	// other is somebody's fix.
	if entry.From != StateHealthy || entry.To != StateDegraded || entry.At != 200 {
		t.Errorf("entry = %+v", entry)
	}
	if !strings.Contains(entry.Reason, StateDegraded) {
		t.Errorf("reason = %q, want the new state's own sentence", entry.Reason)
	}
	// Nothing has been dropped, so the document speaks from when the journal
	// started watching — not from its one entry. The ninety quiet seconds before
	// the change are a real answer, and dating the document from the entry would
	// throw them away.
	if doc.Since != 100 {
		t.Errorf("since = %d, want the moment this model was first read", doc.Since)
	}
}

// TestJournalPublishesWhatItCannotSee. A history that hides its limits is worse
// than no history: a reader has to know whether "no changes" means "nothing
// happened" or "nobody looked".
func TestJournalPublishesWhatItCannotSee(t *testing.T) {
	doc := NewJournal().Document("m1")
	if len(doc.Limits) != 3 {
		t.Fatalf("limits = %+v, want all three", doc.Limits)
	}
	joined := ""
	for _, limit := range doc.Limits {
		if limit.Reason == "" {
			t.Errorf("limit %q has no reason", limit.Limit)
		}
		joined += limit.Limit + " " + limit.Reason
	}
	for _, must := range []string{"nothing polls", "restart", "dropped"} {
		if !strings.Contains(joined, must) {
			t.Errorf("the limits do not mention %q: %+v", must, doc.Limits)
		}
	}
	// An empty journal answers with an empty list rather than a null: the renderer
	// iterates it, and `since: 0` is what says it can speak for nothing yet.
	if doc.Entries == nil || doc.Since != 0 {
		t.Errorf("empty journal = %+v", doc)
	}
}

// TestJournalEnrichesTheObservationItRecorded: "degraded" and "degraded since
// nine this morning" are different findings, and the second is the one somebody
// acts on. Putting it on the observation saves the panel a second request.
func TestJournalEnrichesTheObservationItRecorded(t *testing.T) {
	j := NewJournal()
	j.Record("m1", observed(100, StateHealthy))
	enriched := j.Record("m1", observed(200, StateDegraded))

	if got := enriched.Observations[0]; got.ChangedAt != 200 || got.PreviousState != StateHealthy {
		t.Fatalf("enriched observation = %+v", got)
	}
	// A value nothing has been seen to change carries neither field — which is not
	// the same as nothing having changed, and is exactly why the limits are
	// published beside it.
	fresh := NewJournal().Record("m2", observed(100, StateHealthy))
	if fresh.Observations[0].ChangedAt != 0 || fresh.Observations[0].PreviousState != "" {
		t.Errorf("an unwitnessed value claims a change: %+v", fresh.Observations[0])
	}
}

// TestJournalSpeaksFromWhenItStartedWatching. "Nobody looked" and "somebody looked
// and nothing changed" are different answers, and `since` is the only field that
// tells them apart: a model that has been read carries the moment that reading
// began even when it has journalled nothing at all.
func TestJournalSpeaksFromWhenItStartedWatching(t *testing.T) {
	j := NewJournal()
	if got := j.Document("m1").Since; got != 0 {
		t.Errorf("an unread model speaks from %d, want zero — nobody has looked", got)
	}

	j.Record("m1", observed(500, StateHealthy))
	j.Record("m1", observed(900, StateHealthy))
	doc := j.Document("m1")
	if len(doc.Entries) != 0 {
		t.Fatalf("nothing changed and the journal holds %+v", doc.Entries)
	}
	if doc.Since != 500 {
		t.Errorf("since = %d, want the first read — the quiet stretch is a real answer", doc.Since)
	}
}

// TestJournalIsBoundedAndSaysSo. Past the bound the oldest entries go, and the
// document reports both that they went and from when it can still speak —
// otherwise an empty stretch reads as a quiet one.
func TestJournalIsBoundedAndSaysSo(t *testing.T) {
	j := NewJournal()
	j.Record("m1", observed(0, StateHealthy))
	for i := range maxDriftPerModel + 40 {
		state := StateHealthy
		if i%2 == 0 {
			state = StateDegraded
		}
		j.Record("m1", observed(int64(i+1), state))
	}

	doc := j.Document("m1")
	if len(doc.Entries) != maxDriftPerModel {
		t.Fatalf("journal holds %d entries, want it bounded at %d", len(doc.Entries), maxDriftPerModel)
	}
	if !doc.Truncated {
		t.Error("entries were dropped and the document does not say so")
	}
	// Newest first: that is the order somebody asking "what changed" reads in.
	if doc.Entries[0].At <= doc.Entries[len(doc.Entries)-1].At {
		t.Errorf("entries are not newest first: %d then %d",
			doc.Entries[0].At, doc.Entries[len(doc.Entries)-1].At)
	}
	// Once entries have been dropped, everything before the oldest retained one
	// genuinely is gone, so that is what the document can speak from — the moment
	// the journal started is no longer an honest bound.
	if doc.Since != doc.Entries[len(doc.Entries)-1].At {
		t.Errorf("since = %d, want the oldest retained entry's moment", doc.Since)
	}
}

// TestJournalForgetsTheLeastRecentlyReadModel bounds how many models are held at
// once, so a large model library cannot accumulate rings without limit. The least
// recently written is dropped: a model nobody has opened in a while is the one
// whose history is least likely to be wanted.
func TestJournalForgetsTheLeastRecentlyReadModel(t *testing.T) {
	j := NewJournal()
	for i := range maxDriftModels + 10 {
		id := string(rune('a'+i%26)) + strings.Repeat("x", i)
		j.Record(id, observed(1, StateHealthy))
		j.Record(id, observed(2, StateDegraded))
	}

	j.mu.Lock()
	held := len(j.entries)
	j.mu.Unlock()
	if held > maxDriftModels {
		t.Fatalf("%d model journals held, want at most %d", held, maxDriftModels)
	}
	// The most recent one is still there; the first is gone.
	newest := string(rune('a'+(maxDriftModels+9)%26)) + strings.Repeat("x", maxDriftModels+9)
	if len(j.Document(newest).Entries) == 0 {
		t.Error("the most recently read model's journal was dropped")
	}
	if len(j.Document("a").Entries) != 0 {
		t.Error("the least recently read model's journal survived the bound")
	}
}

// TestJournalIsSafeUnderConcurrentReads. Observations are computed off the run
// loop — a document can wait on peer servers — so this is written from request
// goroutines. The -race build is what checks it; the assertion is that it also
// stays correct.
func TestJournalIsSafeUnderConcurrentReads(t *testing.T) {
	j := NewJournal()
	j.Record("m1", observed(1, StateHealthy))

	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			state := StateHealthy
			if i%2 == 0 {
				state = StateDegraded
			}
			j.Record("m1", observed(int64(i+2), state))
			j.Document("m1")
		}(i)
	}
	wg.Wait()

	// Every recorded transition is between two of the states actually reported;
	// concurrent reads must not splice one value's history into another's.
	for _, entry := range j.Document("m1").Entries {
		if entry.From == entry.To {
			t.Errorf("a transition to the same state was journalled: %+v", entry)
		}
		if entry.From != StateHealthy && entry.From != StateDegraded {
			t.Errorf("entry from an unreported state: %+v", entry)
		}
	}
}

// TestANilJournalIsInert: a service built without one still serves observations
// and answers the drift route with an empty, self-describing document rather than
// panicking or pretending to have history.
func TestANilJournalIsInert(t *testing.T) {
	var j *Journal
	doc := j.Record("m1", observed(1, StateHealthy))
	if doc.Observations[0].ChangedAt != 0 {
		t.Errorf("a nil journal enriched an observation: %+v", doc.Observations[0])
	}
	if got := j.Document("m1"); len(got.Entries) != 0 || len(got.Limits) != 3 {
		t.Errorf("a nil journal's document = %+v", got)
	}
}
