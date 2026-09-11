package order

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
)

// The order API area (ADR-0147).
//
// Placing an order is where the catalogue's work pays off. The release already
// says what a product is made of, in what order it is fulfilled and what needs
// approving, so this reads one release and writes lines — it never consults the
// catalogue and never recomputes a graph. That is the snapshot rule doing its
// job: what was ordered cannot change because somebody edited a product while an
// approval was pending.
//
// The single-writer boundary is the run loop, as everywhere else under api/.

// Service is the order API area.
type Service struct {
	loop  *runloop.Loop
	store *Store
	now   func() int64
	// release resolves a published release. It is a function rather than the
	// catalogue store itself so this package depends on the one thing it needs,
	// and so the caller decides where a release is read from.
	//
	// It is called *inside* this service's own run-loop closure, so it must not
	// dispatch onto the loop itself: Do is a rendezvous, and a nested one would
	// deadlock.
	release func(id string) (catalog.Release, bool, error)
	// mayOrderFrom answers whether a principal may order from a catalogue. Which
	// catalogue somebody sees is decided by their groups and its rank, which is
	// the catalogue service's question — so this package asks rather than
	// deciding, and carries no copy of that rule.
	//
	// Unlike release it is called *outside* this service's loop closure, because
	// the catalogue service dispatches onto the loop itself and a nested Do would
	// deadlock.
	mayOrderFrom func(*httpapi.Principal, string) (bool, error)
	// wake tells the fulfilment process that an order moved.
	//
	// Without a call activity the orchestrator does not wait on a child, so
	// something has to say that a line settled. The line's own provisioning
	// process reports its result, and that report publishes the message the
	// orchestrator is parked on — no polling, and no long-lived wait that exists
	// only to be woken.
	//
	// Called outside this service's loop closure: publishing runs the processor,
	// which is a visit to the loop of its own.
	wake func(message, orderID string) error
}

// The two messages that drive fulfilment, correlated on the order id.
//
// They are constants here rather than strings in the model so the two cannot
// drift apart: a name nobody publishes is a process that waits forever, and it
// fails silently.
const (
	// PlacedMessage starts the fulfilment process for a new order.
	PlacedMessage = "atlas.order.placed"
	// AdvancedMessage says a line settled, so the process asks what may start
	// next. A settled line publishes it; nothing polls.
	AdvancedMessage = "atlas.order.advanced"
)

// New builds the service.
func New(loop *runloop.Loop, store *Store, now func() int64,
	release func(id string) (catalog.Release, bool, error),
	mayOrderFrom func(*httpapi.Principal, string) (bool, error),
	wake func(message, orderID string) error) *Service {
	return &Service{loop: loop, store: store, now: now,
		release: release, mayOrderFrom: mayOrderFrom, wake: wake}
}

func newID(prefix string) (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(b), nil
}

// placeReq is what placing an order says: which release, which products, and for
// whom.
type placeReq struct {
	ReleaseID string   `json:"releaseId"`
	Items     []string `json:"items"`
	// Recipient is who the products are for, defaulting to the orderer. They
	// differ when an integration manager orders for somebody assigned to them.
	Recipient string `json:"recipient,omitempty"`
}

// HandlePlace places an order against one release.
func (s *Service) HandlePlace(w http.ResponseWriter, r *http.Request) {
	p := httpapi.PrincipalFrom(r.Context())
	if p == nil || p.UserID == "" {
		// An order is somebody's. One with no orderer has nobody to notify and
		// nobody to hold responsible for it.
		httpapi.Error(w, http.StatusForbidden, "an order needs an identity")
		return
	}

	var req placeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "malformed JSON body: "+err.Error())
		return
	}
	id, err := newID("ord")
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "mint id: "+err.Error())
		return
	}

	recipient := req.Recipient
	if recipient == "" {
		recipient = p.UserID
	}

	// Resolve the release first, and check access to its catalogue before writing
	// anything. Knowing a release id is no qualification: it appears in every
	// publish response and in every order placed against it.
	var (
		rel     catalog.Release
		found   bool
		lookErr error
	)
	s.loop.Do(func() { rel, found, lookErr = s.release(req.ReleaseID) })
	switch {
	case lookErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read release: "+lookErr.Error())
		return
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no release "+req.ReleaseID)
		return
	}

	// Off the loop: the catalogue service dispatches onto it itself, and a nested
	// Do would deadlock.
	allowed, accessErr := s.mayOrderFrom(p, rel.CatalogID)
	if accessErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "check catalogue access: "+accessErr.Error())
		return
	}
	if !allowed {
		httpapi.Error(w, http.StatusForbidden, "you cannot order from this catalogue")
		return
	}

	var (
		out   Order
		empty bool
		opErr error
	)
	s.loop.Do(func() {
		ordered := rel.Expand(req.Items)
		if len(ordered) == 0 {
			// Nothing the release carries was asked for. An order with no lines is
			// a record of nothing, and it would settle as completed the moment it
			// was created.
			empty = true
			return
		}

		out = Order{
			ID: id, ReleaseID: rel.ID, Orderer: p.UserID, Recipient: recipient,
			Lines:     linesFor(rel, ordered),
			Waves:     wavesFor(rel, ordered),
			Requires:  requiresFor(rel, ordered),
			CreatedAt: s.now(),
		}
		out.UpdatedAt = out.CreatedAt
		opErr = s.store.Save(out)
	})

	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "place order: "+opErr.Error())
	case empty:
		httpapi.Error(w, http.StatusBadRequest, "an order needs at least one product the release carries")
	default:
		// Start the fulfilment process for it. Durable first, then the side effect
		// (I2): the order stands whether or not this succeeds, and a failure here
		// is an order nothing is working on — which the placer must be told about,
		// because replacing it is what they would do next.
		if err := s.wake(PlacedMessage, out.ID); err != nil {
			httpapi.Error(w, http.StatusInternalServerError,
				"the order was placed, but fulfilment could not be started: "+err.Error())
			return
		}
		httpapi.JSON(w, http.StatusCreated, out)
	}
}

// linesFor turns the resolved product ids into pending lines, each carrying the
// processes its product is bound to as the release froze them.
func linesFor(rel catalog.Release, ordered []string) []Line {
	bound := make(map[string]catalog.Item, len(rel.Items))
	for _, it := range rel.Items {
		bound[it.ID] = it
	}
	out := make([]Line, len(ordered))
	for i, id := range ordered {
		it := bound[id]
		out[i] = Line{ItemID: id, Status: StatusPending,
			ProvisionProcess:   it.ProvisionProcess,
			DeprovisionProcess: it.DeprovisionProcess,
			Approval: Approval{
				Kind: string(it.Approval.Kind),
				Ref:  it.Approval.Ref,
			}}
	}
	return out
}

// wavesFor copies the release's schedule, keeping only what this order carries
// and dropping rounds that empty out — a wave of nothing would have fulfilment
// wait for an empty round.
func wavesFor(rel catalog.Release, ordered []string) [][]string {
	in := map[string]bool{}
	for _, id := range ordered {
		in[id] = true
	}
	var out [][]string
	for _, wave := range rel.Waves {
		var kept []string
		for _, id := range wave {
			if in[id] {
				kept = append(kept, id)
			}
		}
		if len(kept) > 0 {
			out = append(out, kept)
		}
	}
	return out
}

// requiresFor copies the preconditions, keeping only edges between lines this
// order carries. A precondition on something not ordered is not this order's to
// wait for: whether the recipient already holds it is a question the item's own
// provisioning process asks.
func requiresFor(rel catalog.Release, ordered []string) map[string][]string {
	in := map[string]bool{}
	for _, id := range ordered {
		in[id] = true
	}
	out := map[string][]string{}
	for item, needs := range rel.Requires {
		if !in[item] {
			continue
		}
		var kept []string
		for _, need := range needs {
			if in[need] {
				kept = append(kept, need)
			}
		}
		if len(kept) > 0 {
			sort.Strings(kept)
			out[item] = kept
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// HandleGet reads one order. An order somebody else placed and is not the
// recipient of reads as absent rather than forbidden: its existence is not
// theirs to learn.
func (s *Service) HandleGet(w http.ResponseWriter, r *http.Request) {
	p := httpapi.PrincipalFrom(r.Context())
	id := r.PathValue("id")

	var (
		got     Order
		found   bool
		loadErr error
	)
	s.loop.Do(func() { got, found, loadErr = s.store.Get(id) })

	mine := p != nil && (got.Orderer == p.UserID || got.Recipient == p.UserID)
	switch {
	case loadErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read order: "+loadErr.Error())
	case !found || !mine:
		httpapi.Error(w, http.StatusNotFound, "no order "+id)
	default:
		httpapi.JSON(w, http.StatusOK, got)
	}
}

// HandleList lists the caller's own orders, newest first.
func (s *Service) HandleList(w http.ResponseWriter, r *http.Request) {
	p := httpapi.PrincipalFrom(r.Context())
	if p == nil {
		httpapi.JSON(w, http.StatusOK, []Order{})
		return
	}
	out := []Order{}
	var loadErr error
	s.loop.Do(func() { out, loadErr = s.store.For(p.UserID) })
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list orders: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// HandleNext reports which of an order's lines may be started now, each with the
// process that provisions it and the variant that was chosen — everything an
// orchestrator needs to act, from one call.
//
// This and [Service.HandleReport] are the orchestrator's two calls, and they are
// operator work rather than the orderer's: nobody reports the result of their own
// provisioning, and an operator works orders that are not theirs. The route's
// role says so; there is no object gate here, deliberately.
func (s *Service) HandleNext(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var (
		got     Order
		found   bool
		loadErr error
	)
	s.loop.Do(func() { got, found, loadErr = s.store.Get(id) })
	switch {
	case loadErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read order: "+loadErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no order "+id)
	default:
		ready := Ready(got)
		out := make([]readyLine, 0, len(ready))
		for _, l := range ready {
			out = append(out, readyLine{Line: l, ApprovalProcess: l.ApprovalProcess()})
		}
		httpapi.JSON(w, http.StatusOK, out)
	}
}

// readyLine is a line as the fulfilment process reads it: everything the order
// stored, plus the process that decides it.
//
// The process is added here rather than stored on the line because it is resolved
// now — see [Line.ApprovalProcess]. Embedding flattens the JSON, so the
// fulfilment model reads one object with one more field and not a nested one.
type readyLine struct {
	Line
	// ApprovalProcess is empty for a line that needs no approval, which is how the
	// model tells the two apart. It is written unconditionally, without omitempty:
	// a FEEL expression comparing an absent key is comparing against null, and the
	// model should be asking whether the string is empty.
	ApprovalProcess string `json:"approvalProcess"`
}

// reportReq is one line's provisioning outcome.
type reportReq struct {
	Status LineStatus `json:"status"`
}

// HandleReport records what came back for one line and propagates it.
func (s *Service) HandleReport(w http.ResponseWriter, r *http.Request) {
	id, item := r.PathValue("id"), r.PathValue("item")

	var req reportReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "malformed JSON body: "+err.Error())
		return
	}

	var (
		got      Order
		found    bool
		applyErr error
		opErr    error
	)
	s.loop.Do(func() {
		if got, found, opErr = s.store.Get(id); opErr != nil || !found {
			return
		}
		var next Order
		if next, applyErr = Apply(got, item, req.Status, s.now()); applyErr != nil {
			return
		}
		got = next
		opErr = s.store.Save(got)
	})

	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "record outcome: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no order "+id)
	case applyErr != nil:
		// A line the order does not carry is absent; anything else about the
		// request is the caller's mistake.
		if strings.Contains(applyErr.Error(), "carries no line") {
			httpapi.Error(w, http.StatusNotFound, applyErr.Error())
			return
		}
		httpapi.Error(w, http.StatusBadRequest, applyErr.Error())
	default:
		// The outcome is durable before anything is woken (I2). If the wake then
		// fails, the order has moved and nothing is coming to move it again, so
		// this is an error rather than a 200 — the reporter is the one thing that
		// can retry, and recording the same outcome twice changes nothing.
		if err := s.wake(AdvancedMessage, id); err != nil {
			httpapi.Error(w, http.StatusInternalServerError,
				"the outcome was recorded, but the fulfilment process could not be woken: "+err.Error())
			return
		}
		httpapi.JSON(w, http.StatusOK, got)
	}
}

// decideReq is an approver's refusal: who decided, and in their own words why.
type decideReq struct {
	By     string `json:"by"`
	Reason string `json:"reason"`
}

// HandleDecide records that an approver refused a line.
//
// It exists because [Service.HandleReport] deliberately will not take a
// rejection: that is a decision with an author, not a provisioning outcome, and
// a second way in would make the author optional. Approving needs no call at all
// — a line that was approved simply goes on to be provisioned, and its outcome
// arrives through the ordinary report.
func (s *Service) HandleDecide(w http.ResponseWriter, r *http.Request) {
	id, item := r.PathValue("id"), r.PathValue("item")

	var req decideReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "malformed JSON body: "+err.Error())
		return
	}

	var (
		got       Order
		found     bool
		lineOK    bool
		decideErr error
		opErr     error
	)
	s.loop.Do(func() {
		if got, found, opErr = s.store.Get(id); opErr != nil || !found {
			return
		}
		lines := make([]Line, len(got.Lines))
		copy(lines, got.Lines)
		for i := range lines {
			if lines[i].ItemID != item {
				continue
			}
			lineOK = true
			var next Line
			if next, decideErr = Reject(lines[i], req.By, s.now(), req.Reason); decideErr != nil {
				return
			}
			lines[i] = next
			break
		}
		if !lineOK || decideErr != nil {
			return
		}
		got.Lines = Propagate(lines, got.Requires)
		got.UpdatedAt = s.now()
		opErr = s.store.Save(got)
	})

	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "record decision: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no order "+id)
	case !lineOK:
		httpapi.Error(w, http.StatusNotFound, "order "+id+" carries no line for "+item)
	case decideErr != nil:
		httpapi.Error(w, http.StatusBadRequest, decideErr.Error())
	default:
		// A refusal settles a line exactly as a provisioning outcome does, so what
		// waited on it has to be told.
		if err := s.wake(AdvancedMessage, id); err != nil {
			httpapi.Error(w, http.StatusInternalServerError,
				"the decision was recorded, but the fulfilment process could not be woken: "+err.Error())
			return
		}
		httpapi.JSON(w, http.StatusOK, got)
	}
}
