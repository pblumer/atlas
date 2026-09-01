package panorama

import (
	"sort"
	"sync"
)

// Desired versus observed, over time (ADR-0189 P5).
//
// P4 answered "what is this architecture doing" at one instant. The question left
// is "what changed, and when" — and the answer has to be given without Panorama
// becoming something it must not be. The record is explicit: it "remains a
// correlation surface, not a time-series or log database". A store of samples is
// exactly the thing that sentence forbids, so this stores none.
//
// What it stores instead is **transitions**. When a bound value's observation
// state changes between two reads, that change is recorded once, with both states
// and the moment it was noticed. A hundred identical readings produce nothing; one
// release going stale produces one entry. That is the shape of "over time" that a
// correlation surface can honestly hold, and it is also the only shape somebody
// actually reads — nobody scrolls a graph of "healthy, healthy, healthy".
//
// Three limits come with it, and all three are published rather than documented,
// because a history that hides what it cannot see is worse than no history:
//
//   - It sees only what was looked at. Observations are computed when somebody
//     asks for them; nothing polls. A state that changed and changed back between
//     two views leaves no trace, and this journal says so rather than implying it
//     watched continuously. Continuous history is what Prometheus is for, which is
//     the record's own answer.
//   - It does not survive a restart. This is runtime state, like the worker
//     registry and the peer cache — never written to the log, never rebuilt by
//     applyToState (I4/I6). An architecture fact belongs in the model; when a
//     transient one was noticed does not.
//   - It is bounded. Past the bound the oldest entries go, and the document says
//     from which moment it can still speak.

// DriftContractVersion is the shape of the document below.
const DriftContractVersion = 1

const (
	// maxDriftPerModel is how many transitions one model's journal holds. It is a
	// reading surface — somebody asking "what changed since this morning" — not an
	// audit trail, and two hundred changes is already more than anybody reads in
	// one sitting.
	maxDriftPerModel = 200

	// maxDriftModels bounds how many models are journalled at once, so a server
	// with a large model library cannot accumulate an unbounded number of rings.
	// The least recently written journal is dropped, because a model nobody has
	// opened in a while is the one whose history is least likely to be wanted.
	maxDriftModels = 64
)

// DriftEntry is one recorded change of one bound value's state.
type DriftEntry struct {
	ElementID string `json:"elementId"`
	Key       string `json:"key"`
	Value     string `json:"value"`
	// From is the state this value was last seen in, and To the state it is in
	// now. Both travel because the direction is the finding: healthy → degraded and
	// degraded → healthy are the same transition seen from opposite sides, and one
	// of them is somebody's incident and the other is somebody's fix.
	From string `json:"from"`
	To   string `json:"to"`
	// Reason is the new state's own sentence, kept so an entry read a week later
	// still says what happened rather than only that something did.
	Reason string `json:"reason,omitempty"`
	At     int64  `json:"at"`
}

// DriftLimit is one thing this journal cannot see, with the reason.
type DriftLimit struct {
	Limit  string `json:"limit"`
	Reason string `json:"reason"`
}

// driftLimits is what every journal document publishes about itself. It is a fixed
// list because these are properties of the design rather than of a request: a
// reader deciding whether "no changes" means "nothing happened" needs all three.
var driftLimits = []DriftLimit{
	{
		Limit: "only what was looked at",
		Reason: "Observations are computed when somebody asks for them; nothing polls. A " +
			"state that changed and changed back between two views leaves no trace here. " +
			"Continuous history is what a metrics store is for.",
	},
	{
		Limit: "not durable",
		Reason: "This is runtime state and a restart empties it. When a transient fact was " +
			"noticed is not an architecture fact, so it is never written to the log.",
	},
	{
		Limit:  "bounded",
		Reason: "The most recent changes are kept and older ones are dropped; `since` says from when this can speak.",
	},
}

// DriftDocument is what a caller asking "what changed" gets.
type DriftDocument struct {
	ContractVersion int          `json:"contractVersion"`
	Entries         []DriftEntry `json:"entries"`
	// Since is the moment from which this document can speak, or zero when this
	// model has never been read. It is the honest bound on every question asked of
	// it: nothing before it can be answered, whether or not anything happened.
	//
	// While nothing has been dropped that is when the journal *started watching*,
	// not the oldest entry it holds — a quiet hour is a real answer, and dating the
	// document from its first entry would throw that hour away. Once entries have
	// been dropped it is the oldest retained entry instead, because everything
	// before that one genuinely is gone.
	Since int64 `json:"since"`
	// Truncated reports that entries were dropped to stay inside the bound, so an
	// empty stretch is not read as a quiet one.
	Truncated bool         `json:"truncated"`
	Limits    []DriftLimit `json:"limits"`
}

// Journal records what changed between one observation document and the next.
//
// It carries its own lock rather than living on the run loop: observations are
// computed off the loop (a document can wait on peer servers), so this is written
// from request goroutines and putting it behind the single writer would mean
// holding that writer across a network call.
type Journal struct {
	mu sync.Mutex
	// state is the last observed state per model, keyed by the bound value, and is
	// what a new document is compared against.
	state map[string]map[string]string
	// entries is each model's ring of transitions, oldest first.
	entries map[string][]DriftEntry
	// truncated remembers that a ring dropped something, which survives the
	// dropping itself.
	truncated map[string]bool
	// started is when each model was first read, which is what the journal can
	// honestly speak from until it has had to drop anything.
	started map[string]int64
	// touched orders models for eviction: the least recently written journal is the
	// one dropped when there are too many.
	touched map[string]int64
	seq     int64
}

func NewJournal() *Journal {
	return &Journal{
		state:     map[string]map[string]string{},
		entries:   map[string][]DriftEntry{},
		truncated: map[string]bool{},
		started:   map[string]int64{},
		touched:   map[string]int64{},
	}
}

// driftKey identifies one bound value across reads. The element is part of it
// because the same Atlas resource may be bound by several elements, and a change
// is a change *of that element's* reading of it.
func driftKey(o Observation) string { return o.ElementID + "\x00" + o.Key + "\x00" + o.Value }

// Record folds one observation document into the journal and returns the document
// enriched with what it now knows about each value's last change.
//
// The first read of a model records nothing. That is deliberate: everything would
// otherwise arrive as a transition from nothing, and a journal whose first page is
// always a full inventory teaches a reader to skip the first page.
func (j *Journal) Record(modelID string, doc ObservationDocument) ObservationDocument {
	if j == nil {
		return doc
	}
	j.mu.Lock()
	defer j.mu.Unlock()

	j.seq++
	j.touched[modelID] = j.seq
	if _, seen := j.started[modelID]; !seen {
		j.started[modelID] = doc.ObservedAt
	}
	previous, known := j.state[modelID]
	// current is what this read saw, and at is the observation each key came from,
	// so a transition can be journalled with the reason belonging to *that* value
	// rather than to whichever one the document happened to list first.
	current := make(map[string]string, len(doc.Observations))
	at := make(map[string]Observation, len(doc.Observations))
	for _, o := range doc.Observations {
		current[driftKey(o)] = o.State
		at[driftKey(o)] = o
	}

	if known {
		// Sorted, so several changes noticed in one read are journalled in a stable
		// order rather than in map order — two servers reading the same landscape
		// should produce the same page.
		for _, key := range sortedKeys(current) {
			was, seen := previous[key]
			if !seen || was == current[key] {
				continue
			}
			o := at[key]
			j.append(modelID, DriftEntry{
				ElementID: o.ElementID, Key: o.Key, Value: o.Value,
				From: was, To: o.State, Reason: o.Reason, At: doc.ObservedAt,
			})
		}
	}
	j.state[modelID] = current
	j.evict()

	// Enrich in place: an observation now says when its state last changed, so the
	// panel can put "since 09:12" beside a finding without a second request.
	last := map[string]DriftEntry{}
	for _, entry := range j.entries[modelID] {
		last[entry.ElementID+"\x00"+entry.Key+"\x00"+entry.Value] = entry
	}
	for i := range doc.Observations {
		if entry, ok := last[driftKey(doc.Observations[i])]; ok {
			doc.Observations[i].ChangedAt = entry.At
			doc.Observations[i].PreviousState = entry.From
		}
	}
	return doc
}

// append adds one entry to a model's ring, dropping the oldest past the bound.
// Caller holds the lock.
func (j *Journal) append(modelID string, entry DriftEntry) {
	ring := append(j.entries[modelID], entry)
	if len(ring) > maxDriftPerModel {
		ring = ring[len(ring)-maxDriftPerModel:]
		j.truncated[modelID] = true
	}
	j.entries[modelID] = ring
}

// evict drops the least recently written journals once there are too many. Caller
// holds the lock.
func (j *Journal) evict() {
	if len(j.entries) <= maxDriftModels && len(j.state) <= maxDriftModels {
		return
	}
	ids := make([]string, 0, len(j.touched))
	for id := range j.touched {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(a, b int) bool { return j.touched[ids[a]] < j.touched[ids[b]] })
	for _, id := range ids[:max(0, len(ids)-maxDriftModels)] {
		delete(j.entries, id)
		delete(j.state, id)
		delete(j.truncated, id)
		delete(j.started, id)
		delete(j.touched, id)
	}
}

// Document returns one model's journal, newest first — which is the order somebody
// asking "what changed" reads in.
func (j *Journal) Document(modelID string) DriftDocument {
	doc := DriftDocument{
		ContractVersion: DriftContractVersion,
		Entries:         []DriftEntry{},
		Limits:          driftLimits,
	}
	if j == nil {
		return doc
	}
	j.mu.Lock()
	defer j.mu.Unlock()

	ring := j.entries[modelID]
	for i := len(ring) - 1; i >= 0; i-- {
		doc.Entries = append(doc.Entries, ring[i])
	}
	doc.Truncated = j.truncated[modelID]
	switch {
	case doc.Truncated && len(ring) > 0:
		doc.Since = ring[0].At
	default:
		doc.Since = j.started[modelID]
	}
	return doc
}
