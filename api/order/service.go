package order

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
	"github.com/pblumer/atlas/limits"
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
	// groupsOf answers which groups somebody belongs to, for the eligibility check
	// (ADR-0347). It asks about the **recipient**, who is not
	// the caller and therefore has no principal in the request — a manager ordering
	// for a new hire is the ordinary case, and checking the caller's groups would
	// refuse exactly that.
	//
	// It answers [httpapi.ErrNoSuchPrincipal] for a name nobody holds, which is a
	// different failure from a store it could not read: the first refuses the
	// order as bad input, the second as a server fault, and telling them apart is
	// what stops a misspelled recipient reading as an outage.
	groupsOf func(string) ([]string, error)
	// mayOrderForOthers answers whether this caller may place an order naming
	// somebody else as the recipient (ADR-0349).
	//
	// A separate question from mayOrderFrom, which asks whether the *shop* is
	// theirs. This asks whether the *order* may be somebody else's, and the two are
	// independent: being the audience for a catalogue says nothing about whose name
	// an order may carry.
	mayOrderForOthers func(*httpapi.Principal) bool
	// Limits are the resource budgets this service enforces. Set after
	// construction, like every other per-area service's, so that the constructor
	// does not grow a thirteenth positional argument for a value the server hands
	// to all of them the same way.
	Limits limits.Limits
	// wake tells the fulfilment process that an order moved. The variables it
	// carries are the message's start variables, which is how the orchestrator
	// learns anything beyond the order id it correlates on.
	//
	// Without a call activity the orchestrator does not wait on a child, so
	// something has to say that a line settled. The line's own provisioning
	// process reports its result, and that report publishes the message the
	// orchestrator is parked on — no polling, and no long-lived wait that exists
	// only to be woken.
	//
	// Called outside this service's loop closure: publishing runs the processor,
	// which is a visit to the loop of its own.
	wake func(message, orderID string, vars map[string]string) error
	// portalBase is the origin a notification's link is built on: the operator's
	// configured external URL, or empty when none is set. It is a function rather
	// than a string so a server that learns it later is not frozen at
	// construction, and empty is a supported answer — a model that finds it empty
	// says where to go instead of printing a link nobody can follow.
	portalBase func() string
	// grant and revoke keep the inventory in step with what fulfilment actually
	// did: a line that reaches done records a right, a line that is given back
	// removes it. See [Grant] for why only those two transitions do.
	//
	// They are functions for the same reason release is: this package needs the
	// two facts written, not the engine that writes them. Called outside this
	// service's loop closure — writing an engine fact runs the processor, which
	// is a visit to the loop of its own, and a nested Do would deadlock.
	grant  func(Grant) error
	revoke func(principal, itemID string, at int64, by string) error
	// held answers what one principal already holds, as a set of item ids. It is
	// what makes the basket's second resolution possible — an item the recipient
	// already has and may not have twice is ordered as skipped rather than
	// provisioned again.
	//
	// Called outside this service's loop closure: reading the inventory takes a
	// read view, which is taken on the loop (ADR-0239).
	held func(principal string) (map[string]bool, error)
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
	groupsOf func(string) ([]string, error),
	mayOrderForOthers func(*httpapi.Principal) bool,
	wake func(message, orderID string, vars map[string]string) error,
	portalBase func() string,
	grant func(Grant) error,
	revoke func(principal, itemID string, at int64, by string) error,
	held func(principal string) (map[string]bool, error)) *Service {
	return &Service{loop: loop, store: store, now: now,
		release: release, mayOrderFrom: mayOrderFrom, groupsOf: groupsOf,
		mayOrderForOthers: mayOrderForOthers, wake: wake, portalBase: portalBase,
		grant: grant, revoke: revoke, held: held}
}

// budgets is the effective budget set: the server's where it set one, the defaults
// otherwise, so a service built in a test bounds what a service built by the server
// bounds.
func (s *Service) budgets() limits.Limits {
	if s.Limits == (limits.Limits{}) {
		return limits.Default()
	}
	return s.Limits
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
	// Variants names the shapes of each ordered product that has more than one,
	// keyed by item id. One entry is one position: naming two is ordering the
	// product twice, which the catalogue allows exactly where it says the product
	// may be held more than once.
	//
	// Keyed by item and not a field on a line, because the caller does not send
	// lines: it sends what it chose, and an integral part carrying variants is
	// never named in Items at all. It is a separate field rather than a reserved
	// key inside Config because a variant is not an answer to a form — it is part
	// of what was ordered, it is checked against the catalogue, and it is read by
	// provisioning whether or not the product has a form.
	Variants map[string][]string `json:"variants,omitempty"`
	// Config carries the answers to each product's configuration form, keyed by
	// item id and then by the form's own field key
	// (ADR-0358).
	//
	// Keyed by item rather than flat because two products in one basket legitimately
	// ask the same question — two laptops, two cost centres — and a flat map would
	// silently keep one answer.
	Config map[string]map[string]string `json:"config,omitempty"`
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

	// And whose name this order may carry (ADR-0349).
	//
	// Checked here rather than folded into the recipient's eligibility below,
	// because the two refuse different things: eligibility asks whether *this
	// person* may have *this product*, and would happily let somebody place an
	// order in a colleague's name for something the colleague is perfectly entitled
	// to. What is wrong there is not the product — it is the name on the order.
	if recipient != p.UserID && !s.mayOrderForOthers(p) {
		httpapi.Error(w, http.StatusForbidden,
			"ordering in somebody else's name needs the operator role. An order placed "+
				"for another person puts an approval in their manager's inbox and a line "+
				"in their record, which is why it is not something every account may do")
		return
	}

	// What the recipient already holds, read before the loop is entered for the
	// same reason the catalogue gate is. An unreadable inventory refuses the order
	// rather than placing one that would provision a second copy of something:
	// ordering twice is visible in a target system and undoing it is somebody's
	// afternoon.
	has, heldErr := s.held(recipient)
	if heldErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read inventory: "+heldErr.Error())
		return
	}

	// And which groups the recipient is in, read here for the reason the two above
	// are: it needs the loop itself, and the placement below already holds it. An
	// unreadable answer refuses the order rather than placing one — a restriction
	// that fails open is not a restriction.
	recipientGroups, groupErr := s.groupsOf(recipient)
	switch {
	case errors.Is(groupErr, httpapi.ErrNoSuchPrincipal):
		// A recipient nobody holds is the caller's mistake, and it was being answered
		// as the server's. That matters beyond the status code: an order naming a
		// person who does not exist puts an approval in nobody's inbox, provisions
		// against nothing, and leaves a right attached to a string — so the refusal
		// has to be readable by whoever typed the name, not by whoever reads the
		// server log.
		httpapi.Error(w, http.StatusBadRequest, groupErr.Error())
		return
	case groupErr != nil:
		httpapi.Error(w, http.StatusInternalServerError,
			"check who the recipient is: "+groupErr.Error())
		return
	}

	var (
		out    Order
		empty  bool
		clash  *conflict
		barred *ineligible
		shut   *closed
		stray  string
		opErr  error
	)
	s.loop.Do(func() {
		// One reading of the clock for the whole placement: the window below is
		// checked against the same instant the order records as its creation. Read
		// twice, a basket placed across a boundary could be refused for a window
		// that had already opened at the moment the order says it was placed.
		at := s.now()
		ordered := rel.Expand(req.Items)
		if len(ordered) == 0 {
			// Nothing the release carries was asked for. An order with no lines is
			// a record of nothing, and it would settle as completed the moment it
			// was created.
			empty = true
			return
		}
		// Refused here rather than reported later, which is the whole point of a
		// preventive control (ADR-0342): detection means the
		// forbidden combination exists in the real world for as long as detection
		// takes, and a control that permits what it forbids and then reports it is a
		// detective control with extra steps.
		if clash = conflictIn(rel, ordered, has); clash != nil {
			return
		}
		// And who may receive it at all. Beside the conflict check rather than
		// before the catalogue gate, because it is the same kind of rule read at the
		// same moment: what the release says about this basket
		// (ADR-0347). The catalogue's audience has already
		// decided whether this shop is theirs; this decides whether this shelf is.
		if barred = ineligibleIn(rel, ordered, recipientGroups); barred != nil {
			return
		}
		// And whether it may be ordered at this moment at all. Beside the two above
		// because it is the third rule of the same kind — what the release says
		// about this basket — and it is the one nothing used to ask
		// (ADR-0397).
		if shut = closedIn(rel, ordered, at); shut != nil {
			return
		}
		// And that the answers belong to this basket. Checked here because it needs
		// the expanded list: an integral part is ordered without being asked for,
		// and it may perfectly well carry a form of its own.
		if stray = s.strayAnswers(rel, ordered, req.Config); stray != "" {
			return
		}
		// And which shape of each product was chosen. Checked here for the same
		// reason and against the same expanded list: the product carrying the
		// variants is usually an integral part, so the orderer never named it.
		if stray = unresolvedVariant(rel, ordered, req.Variants); stray != "" {
			return
		}

		out = Order{
			ID: id, ReleaseID: rel.ID, Orderer: p.UserID, Recipient: recipient,
			Lines:     linesFor(rel, ordered, has, req.Config, req.Variants),
			Waves:     wavesFor(rel, ordered),
			Requires:  requiresFor(rel, ordered),
			CreatedAt: at,
		}
		out.UpdatedAt = out.CreatedAt
		opErr = s.store.Save(out)
	})

	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "place order: "+opErr.Error())
	case empty:
		httpapi.Error(w, http.StatusBadRequest, "an order needs at least one product the release carries")
	case clash != nil:
		httpapi.Error(w, http.StatusConflict, clash.reason())
	case stray != "":
		httpapi.Error(w, http.StatusBadRequest, stray)
	case barred != nil:
		// 403 and not 409: a conflict is a state of the estate that could be
		// resolved by giving something back, and this is a statement about who the
		// recipient is. Telling the two apart is what lets a caller know whether
		// there is anything to do about it.
		httpapi.Error(w, http.StatusForbidden, barred.reason())
	case shut != nil:
		// 403 for the reason above and one of its own: nothing about the request is
		// malformed — the very same body is correct the day the window opens — so a
		// 400 would send whoever reads it looking for a mistake they did not make.
		// And there is nothing to give back, which is what a 409 would offer.
		httpapi.Error(w, http.StatusForbidden, shut.reason())
	default:
		// Start the fulfilment process for it. Durable first, then the side effect
		// (I2): the order stands whether or not this succeeds, and a failure here
		// is an order nothing is working on — which the placer must be told about,
		// because replacing it is what they would do next.
		// The orchestrator's start variables. Its model documents orderer and
		// recipient as such and never received them: the wake carried the
		// correlation key and nothing else, so both arrived null and every notice
		// the fulfilment ever sent would have been addressed to nobody.
		//
		// orderId is on that list for the same reason and was missed by that
		// correction. It is not covered by the correlation key: a message *start*
		// event's key is evaluated from the payload, so `=orderId` over a payload
		// without it resolves to nothing — the instance recorded no key and the
		// variable the whole model reads was never written. Nothing failed, because
		// FEEL propagates null: the first request was built as "/api/v1/orders/" +
		// null + "/next" and the orchestration asked for nothing, forever, with no
		// incident for anybody to find.
		//
		// portalBaseUrl is what a notification's link is built on. It is the
		// operator's configured origin or empty; a model that finds it empty says
		// where to go instead of printing a link nobody can follow.
		if err := s.wake(PlacedMessage, out.ID, PlacedVariables(out, s.portalBase())); err != nil {
			httpapi.Error(w, http.StatusInternalServerError,
				"the order was placed, but fulfilment could not be started: "+err.Error())
			return
		}
		httpapi.JSON(w, http.StatusCreated, out)
	}
}

// PlacedVariables is what the fulfilment orchestration is started with.
//
// One function rather than a literal at the call site, because there is a second
// caller: the repair that starts an orchestration again for an order whose own one
// was lost or could never work (api/fulfilmentrepair.go). Two literals would be two
// payloads, and the one that drifted would produce an orchestration that runs and
// quietly does nothing — which is exactly the defect this list was widened for.
//
// portalBaseUrl is what a notification's link is built on: the operator's configured
// origin or empty. A model that finds it empty says where to go instead of printing
// a link nobody can follow.
func PlacedVariables(o Order, portalBase string) map[string]string {
	return map[string]string{
		"orderId":       o.ID,
		"orderer":       o.Orderer,
		"recipient":     o.Recipient,
		"portalBaseUrl": portalBase,
	}
}

// FulfilmentProcess is the id of the model that works an order
// (api/systemprocesses/auftrag-erfuellung.bpmn). Named here so the Go side has one
// spelling of it; the model carries its own, and the two are held together by the
// tests that start it.
const FulfilmentProcess = "atlas-auftrag-erfuellung"

// unresolvedVariant reports what is wrong with the variants this order names, or
// "" when nothing is.
//
// Three things are wrong, and they are three different mistakes:
//
//   - a product in the order that offers variants and was given none. The order
//     would be stored, provisioned, and reach the target system saying "a phone"
//     — which is not a thing anybody can hand over. There is no default, and
//     picking the first would be the ordering the catalogue deliberately does not
//     have (see catalog.Variant).
//   - a variant the product does not offer. Stored, it would reach provisioning as
//     a string that looks like an answer and names nothing.
//   - a variant for a product that offers none, or that this order does not carry.
//     Almost always a stale basket, and the honest answer is to say so rather than
//     to drop it silently.
//
// Checked against the expanded order rather than against what the caller asked
// for, because the product carrying the variants is frequently an integral part:
// somebody orders a bundle and the colour belongs to the phone inside it.
func unresolvedVariant(rel catalog.Release, ordered []string,
	chosen map[string][]string) string {

	carried := make(map[string]catalog.Item, len(ordered))
	for _, id := range ordered {
		for _, it := range rel.Items {
			if it.ID == id {
				carried[id] = it
				break
			}
		}
	}
	// Sorted throughout, so one bad basket is refused with one sentence every time.
	named := make([]string, 0, len(chosen))
	for id := range chosen {
		named = append(named, id)
	}
	sort.Strings(named)
	for _, id := range named {
		it, inOrder := carried[id]
		shapes := chosen[id]
		switch {
		case !inOrder:
			return fmt.Sprintf("the order names a variant for %q, which is not in it. "+
				"Remove it or order the product it belongs to", id)
		case len(it.Variants) == 0:
			return fmt.Sprintf("%q comes in one shape and the order names a variant "+
				"for it. Nothing would read it, so it is refused rather than stored", id)
		case len(shapes) > 1 && !it.MultipleAllowed:
			return fmt.Sprintf("%q may be held once and the order asks for %d of them. "+
				"Order one shape of it, or say in the catalogue that it may be held more "+
				"than once", id, len(shapes))
		}
		seen := map[string]bool{}
		for _, v := range shapes {
			switch {
			case !offersVariant(it, v):
				return fmt.Sprintf("%q does not come in %q", id, v)
			case seen[v]:
				// Two identical positions could not be told apart afterwards, and
				// "two black phones" is a quantity — which this catalogue does not
				// have. Refused rather than quietly merged into one.
				return fmt.Sprintf("the order asks for %q in %q twice, and two positions "+
					"of one shape cannot be told apart", id, v)
			}
			seen[v] = true
		}
	}
	missing := make([]string, 0, len(ordered))
	for _, id := range ordered {
		if it, ok := carried[id]; ok && len(it.Variants) > 0 && len(chosen[id]) == 0 {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		return fmt.Sprintf("%q comes in more than one shape and the order does not say "+
			"which. An order that does not name the variant cannot be provisioned",
			missing[0])
	}
	return ""
}

// offersVariant reports whether an item carries the named variant.
func offersVariant(it catalog.Item, id string) bool {
	for _, v := range it.Variants {
		if v.ID == id {
			return true
		}
	}
	return false
}

// linesFor turns the resolved product ids into lines, each carrying the processes
// its product is bound to as the release froze them.
//
// A line starts pending unless the recipient already holds the item and the item
// says it may not be held twice, in which case it starts skipped — the basket's
// second resolution (ADR-0312).
//
// Skipped rather than dropped, on purpose. The request was made and the record
// should say so: "you asked for this and already had it" is a different sentence
// from silence, and a reader of the order months later can tell the two apart.
// Skipped also counts as satisfied, so a line that requires this one is not left
// waiting for something nobody is going to provision.
func linesFor(rel catalog.Release, ordered []string, held map[string]bool,
	config map[string]map[string]string, variants map[string][]string) []Line {
	bound := make(map[string]catalog.Item, len(rel.Items))
	for _, it := range rel.Items {
		bound[it.ID] = it
	}
	// Which of these arrived because something else in the order always carries it.
	// Computed from the release rather than from what the caller asked for: the
	// question is whether *this order* carries it as a part, and an id that was
	// both chosen and carried is carried — the whole is in the basket either way.
	inOrder := make(map[string]bool, len(ordered))
	for _, id := range ordered {
		inOrder[id] = true
	}
	carried := map[string]bool{}
	for _, id := range ordered {
		for _, part := range rel.Includes[id] {
			if inOrder[part] {
				carried[part] = true
			}
		}
	}
	out := make([]Line, 0, len(ordered))
	for _, id := range ordered {
		it := bound[id]
		status := StatusPending
		if held[id] && !it.MultipleAllowed {
			status = StatusSkipped
		}
		// One line per shape that was asked for, and one line where no shape was:
		// a position is a product *and* the shape of it, and a product with no
		// variants has exactly one shape — the unnamed one.
		shapes := append([]string(nil), variants[id]...)
		sort.Strings(shapes)
		if len(shapes) == 0 {
			shapes = []string{""}
		}
		for _, shape := range shapes {
			out = append(out, Line{ItemID: id, VariantID: shape, Status: status,
				ProvisionProcess:   it.ProvisionProcess,
				DeprovisionProcess: it.DeprovisionProcess,
				MaxDays:            it.MaxDays,
				// The form's id travels with the line beside the answers, so a reader of
				// the order months later knows which set of questions these answers were
				// given to — the answers alone are a map of keys nobody can interpret.
				Price:      it.Price,
				ConfigForm: it.ConfigForm,
				Config:     copyAnswers(config[id]),
				Integral:   carried[id],
				Includes:   partsOf(rel, id, inOrder),
				Approval: Approval{
					Kind: string(it.Approval.Kind),
					Ref:  it.Approval.Ref,
				}})
		}
	}
	return out
}

// strayAnswers reports why a basket's configuration answers do not belong to it,
// or "" when they do (ADR-0358).
//
// Two refusals, both because the alternative is an order that silently loses
// something somebody typed:
//
//   - answers for a product this order does not carry. Almost always a stale
//     basket — the product was taken out and its answers were not — and the
//     honest response is to say so rather than to drop them.
//   - answers for a product that asks no questions. The catalogue does not know
//     what to do with them and no process will read them, so storing them would
//     put data in the record that nothing can interpret.
//
// It deliberately does *not* refuse a product whose form went unanswered. Whether
// a field is required is the form's own statement, rendered by the form runtime,
// and re-deciding it here would be a second copy of a rule that already exists —
// wrong the first time somebody marks a field optional.
func (s *Service) strayAnswers(rel catalog.Release, ordered []string,
	config map[string]map[string]string) string {
	if len(config) == 0 {
		return ""
	}
	inBasket := make(map[string]catalog.Item, len(ordered))
	for _, id := range ordered {
		for _, it := range rel.Items {
			if it.ID == id {
				inBasket[id] = it
				break
			}
		}
	}
	// Sorted, so the same bad basket is refused with the same sentence every time.
	ids := make([]string, 0, len(config))
	for id := range config {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if len(config[id]) == 0 {
			continue
		}
		it, carried := inBasket[id]
		switch {
		case int32(len(config[id])) > s.budgets().OrderLineAnswers:
			return fmt.Sprintf("%q carries %d details and no more than %d are kept for "+
				"one line. A form with that many questions is a process wearing a form's "+
				"clothes", id, len(config[id]), s.budgets().OrderLineAnswers)
		case !carried:
			return fmt.Sprintf("the order carries details for %q, which is not in it. "+
				"Remove them or order the product they belong to", id)
		case it.ConfigForm == "":
			return fmt.Sprintf("%q asks for no details and the order carries some. "+
				"Nothing would read them, so they are refused rather than stored", id)
		}
	}
	return ""
}

// partsOf names the parts this line always carries that this order also has, so a
// refusal can say what carries the part it will not take back. Sorted and copied,
// because the release's slice must not reach into an order.
func partsOf(rel catalog.Release, id string, inOrder map[string]bool) []string {
	var out []string
	for _, part := range rel.Includes[id] {
		if inOrder[part] {
			out = append(out, part)
		}
	}
	sort.Strings(out)
	return out
}

// copyAnswers is the line's own map, so the request body cannot be edited into an
// order after it has been written.
func copyAnswers(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
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
			out = append(out, readyLine{ID: l.Key(), Line: l, ApprovalProcess: l.ApprovalProcess()})
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
	// ID is what this position is called, for a process that has to report an
	// outcome against it. It is the item id wherever the order carries one position
	// of the product, so a process built against itemId keeps working
	// (ADR-0384).
	ID string `json:"id"`
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
		// The inventory is brought in step before the process is woken, for the
		// same reason the order is saved before either: what somebody holds is a
		// fact, and the wake is only a prompt to go and look. A model woken first
		// could ask what the recipient holds and be told the truth from one moment
		// ago. A failure here is the caller's to retry, like the wake below —
		// granting the same right twice writes the same record.
		if invErr := s.recordInventory(got, item, req.Status); invErr != nil {
			httpapi.Error(w, http.StatusInternalServerError,
				"the outcome was recorded, but the inventory could not be updated: "+invErr.Error())
			return
		}
		// The outcome is durable before anything is woken (I2). If the wake then
		// fails, the order has moved and nothing is coming to move it again, so
		// this is an error rather than a 200 — the reporter is the one thing that
		// can retry, and recording the same outcome twice changes nothing.
		if err := s.wake(AdvancedMessage, id, nil); err != nil {
			httpapi.Error(w, http.StatusInternalServerError,
				"the outcome was recorded, but the fulfilment process could not be woken: "+err.Error())
			return
		}
		httpapi.JSON(w, http.StatusOK, got)
	}
}

// recordInventory tells the inventory what one settled line changed about the
// rights its recipient holds.
//
// The moment it records is the order's own UpdatedAt, which [Apply] has just set
// from the server clock, so the order and the right it produced never disagree
// about when it started.
//
// Every other outcome returns nothing to do, and does so by naming no case rather
// than by listing the ones it ignores: a status added later has to be considered
// here on purpose, and a switch that fell through a default would quietly decide
// for it.
func (s *Service) recordInventory(o Order, ref string, status LineStatus) error {
	key, err := ResolveLine(o, ref)
	if err != nil {
		return err
	}
	// The right is about the product and the shape of it, so both are read off the
	// position rather than taken from what the caller named: a caller may name a
	// product where the order carries one position of it, and the grant still has
	// to say which variant was handed over.
	var line Line
	for _, l := range o.Lines {
		if l.Key() == key {
			line = l
			break
		}
	}
	itemID := line.ItemID
	switch status {
	case StatusDone:
		variant, maxDays := line.VariantID, line.MaxDays
		// The end, computed here because this is where the release's ceiling and
		// the moment the right began are both in hand. Zero days is a right that
		// does not end, and stays the ordinary case.
		var until int64
		if maxDays > 0 {
			until = o.UpdatedAt + int64(maxDays)*int64(24*time.Hour)
		}
		return s.grant(Grant{
			Principal: o.Recipient, ItemID: itemID, VariantID: variant,
			OrderID: o.ID, At: o.UpdatedAt, Until: until,
		})
	case StatusReturned:
		// The moment is the order's, not a fresh clock reading: it is the same
		// frozen, command-time value the grant above uses, so the pair of history
		// timestamps comes from one source. The actor is whoever asked for the
		// return, carried on the line since then — what completed it is a
		// deprovisioning process, and naming that as the decider would attribute a
		// decision to a robot.
		return s.revoke(o.Recipient, itemID, o.UpdatedAt, line.ReturnedBy)
	}
	return nil
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
		if err := s.wake(AdvancedMessage, id, nil); err != nil {
			httpapi.Error(w, http.StatusInternalServerError,
				"the decision was recorded, but the fulfilment process could not be woken: "+err.Error())
			return
		}
		httpapi.JSON(w, http.StatusOK, got)
	}
}

// conflict is one incompatible pair an order would create
// (ADR-0342).
//
// It names both items, never one. A conflict is a fact about a pair, and no rule
// can say which half is wrong — the person needs one of them to do their job, and
// which one is a question about the job rather than about the catalogue.
type conflict struct {
	// Ordered is the item this order asked for. Other is what it is incompatible
	// with, and Held says whether the recipient already has that other half or is
	// asking for it in the same basket.
	Ordered string
	Other   string
	Held    bool
}

// reason is the sentence the placer reads.
func (c conflict) reason() string {
	if c.Held {
		return fmt.Sprintf("%q cannot be held together with %q, which the recipient already "+
			"has. Neither is wrong on its own — the catalogue says the combination is. Giving "+
			"up whichever is no longer needed is a return or an access review, and both record "+
			"who decided", c.Ordered, c.Other)
	}
	return fmt.Sprintf("%q and %q cannot be held together, and this order asks for both. "+
		"Neither is wrong on its own — the catalogue says the combination is", c.Ordered, c.Other)
}

// conflictIn finds the first incompatibility an order would create, against what the
// recipient holds and against the rest of the same basket.
//
// The first rather than all of them, deliberately: an order is refused whole, so a
// second conflict changes nothing about the outcome and a list of them reads as a
// bigger problem than the one that has to be solved. The next placement finds the
// next one.
//
// Deterministic, because the refusal is a message somebody may quote back: the
// ordered items are walked in the order the release expanded them, and each item's
// exclusions are already sorted by [catalog.Publish].
func conflictIn(rel catalog.Release, ordered []string, held map[string]bool) *conflict {
	if len(rel.Excludes) == 0 {
		return nil
	}
	asking := make(map[string]bool, len(ordered))
	for _, id := range ordered {
		asking[id] = true
	}
	for _, id := range ordered {
		for _, other := range rel.Excludes[id] {
			switch {
			case held[other]:
				return &conflict{Ordered: id, Other: other, Held: true}
			case asking[other]:
				// Both halves in one basket. Reported once rather than twice: the
				// walk would meet the same pair again from the other side, and two
				// refusals about one pair is one refusal too many.
				if id < other {
					return &conflict{Ordered: id, Other: other}
				}
			}
		}
	}
	return nil
}
