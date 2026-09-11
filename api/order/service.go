package order

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"

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
}

// New builds the service.
func New(loop *runloop.Loop, store *Store, now func() int64,
	release func(id string) (catalog.Release, bool, error),
	mayOrderFrom func(*httpapi.Principal, string) (bool, error)) *Service {
	return &Service{loop: loop, store: store, now: now,
		release: release, mayOrderFrom: mayOrderFrom}
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
			Lines:     linesFor(ordered),
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
		httpapi.JSON(w, http.StatusCreated, out)
	}
}

// linesFor turns the resolved product ids into pending lines.
func linesFor(ordered []string) []Line {
	out := make([]Line, len(ordered))
	for i, id := range ordered {
		out[i] = Line{ItemID: id, Status: StatusPending}
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
