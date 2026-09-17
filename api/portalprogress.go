package api

import (
	"errors"
	"net/http"
	"sort"
	"sync"

	"encoding/xml"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"

	"bytes"
)

// Where one position stands, answered to the person whose position it is
// (ADR-draft-position-progress).
//
// A position carries a link into the process working on it, and that link is an
// operator's: every route that finds or opens an instance is RoleOperator, and the
// instance view shows the whole engine state of the instance — including variables
// that belong to somebody else's order where a process holds them. Widening those
// routes to the orderer would hand out an operations surface to answer a question
// about one line.
//
// So this is a different answer rather than the same answer through a wider door:
// one route, gated on owning the order, that says which step the position is
// sitting on and nothing else. The server finds the instance; the caller never
// touches the instance search.
//
// # The two variables that name a position's process
//
// They are what the fulfilment model passes when it starts one
// (api/systemprocesses/auftrag-erfuellung.bpmn), and what [Server.startReturn]
// passes when it starts a deprovisioning. Both are needed and neither is enough:
// the order id alone also matches the order's own orchestration and every other
// position of it, and a position key alone is unique only inside one order —
// "laptop" is the first position of thousands of orders.
const (
	// progressOrderVar names the order a running process belongs to.
	progressOrderVar = "orderId"
	// progressPositionVar names the position inside it (ADR-0384): the item id, or
	// the item id and the variant where one product was ordered in two shapes.
	progressPositionVar = "positionId"
)

// positionProgress is the whole answer: where the position's process is, and
// nothing about what it holds.
//
// No variables, deliberately. The process carries the recipient, the orderer and
// the order id, and none of that is this route's to hand back — the caller already
// knows their own order, and the route exists to say *where*, not *what*.
type positionProgress struct {
	// State is "active" while a process is working on this position and "none"
	// when none is.
	//
	// Two words and not three. "Finished" would be a third, and answering it means
	// walking the retained history for every call to distinguish an instance that
	// ended from one retention has since deleted — a distinction that changes
	// nothing for the reader, because whether the position was provisioned,
	// rejected or withdrawn is its own status on the same row. What this route adds
	// is the step, and a position nothing is running for has none.
	State string `json:"state"`
	// Steps are the elements the instance is sitting on right now, by the name the
	// model gives them, falling back to the BPMN id where an element is unnamed.
	//
	// A list and not one step: a process that forked is on two steps at once, and
	// naming whichever the walk reached first would be a coin toss rendered as
	// fact.
	Steps []string `json:"steps"`
}

// handleLineProgress answers where one of the caller's own positions stands.
func (s *Server) handleLineProgress(w http.ResponseWriter, r *http.Request) {
	id, ref := r.PathValue("id"), r.PathValue("position")
	p := httpapi.PrincipalFrom(r.Context())

	var (
		ord     order.Order
		found   bool
		loadErr error
	)
	s.do(func() { ord, found, loadErr = s.orderStore.Get(id) })
	// Ownership and not a role, and the same 404 reading the order itself gives:
	// whether somebody else's order exists is not something this route confirms.
	mine := p != nil && (ord.Orderer == p.UserID || ord.Recipient == p.UserID)
	switch {
	case loadErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read order: "+loadErr.Error())
		return
	case !found || !mine:
		httpapi.Error(w, http.StatusNotFound, "no order "+id)
		return
	}

	position, err := order.ResolveLine(ord, ref)
	if err != nil {
		// A conflict rather than a 404: the order is real and the caller may read
		// it. What cannot be answered is which of two positions of one product they
		// mean, and the refusal names both so the next call can say.
		httpapi.Error(w, http.StatusConflict, err.Error())
		return
	}

	out, err := s.progressOf(id, position)
	switch {
	case errors.Is(err, errLoopClosing):
		httpapi.Error(w, http.StatusServiceUnavailable, err.Error())
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read progress: "+err.Error())
	default:
		httpapi.JSON(w, http.StatusOK, out)
	}
}

// progressOf finds the process working on one position and says where it is.
//
// Two phases, and the split is not cosmetic: the walk runs against a read view,
// and resolving the names needs the deployed document, which lives on the run
// loop. Dispatching onto the loop while holding a view keeps a snapshot open
// across a rendezvous, which is what [Server.readOffLoop] asks callers not to do.
// So the view is closed first, and the ids it found are turned into names after.
func (s *Server) progressOf(orderID, position string) (positionProgress, error) {
	var (
		defKey uint64
		ids    []string
		found  bool
	)
	err := s.readOffLoop(func(rv *state.ReadView, defs defIndex) error {
		instKey, ok, err := positionInstance(rv, orderID, position)
		if err != nil || !ok {
			return err
		}
		found = true
		ids, defKey, err = liveElementsOf(rv, defs, instKey)
		return err
	})
	if err != nil {
		return positionProgress{}, err
	}
	if !found {
		return positionProgress{State: "none", Steps: []string{}}, nil
	}
	names := s.elementNamesOf(defKey)
	steps := make([]string, 0, len(ids))
	for _, id := range ids {
		if name := names[id]; name != "" {
			steps = append(steps, name)
			continue
		}
		// An unnamed element is answered with its id rather than dropped. It says
		// less than a name and it is still where the position is.
		steps = append(steps, id)
	}
	sort.Strings(steps)
	return positionProgress{State: "active", Steps: steps}, nil
}

// errFoundInstance ends the walk at the first match. A sentinel and not a bool,
// because the walk is driven by the store and a callback can only stop it by
// failing.
var errFoundInstance = errors.New("instance found")

// positionInstance finds the running process of one position.
//
// Live instances only, and that is the whole cost argument. A position's process
// is whichever process the product named, so there is no definition to scope the
// search to and no value index to seek in — the alternative is the content walk
// the operator search makes, over the history as well, which is the walk that
// listing caps at 200 rows. Bounding it to the active family bounds it by the work
// actually in flight rather than by everything the store has ever run, and a
// finished instance has no step to report anyway.
func positionInstance(rv *state.ReadView, orderID, position string) (uint64, bool, error) {
	var hit uint64
	err := rv.ActiveProcessInstances(func(key uint64, _ *model.ProcessInstanceValue) error {
		var rightOrder, rightPosition bool
		if err := rv.VariablesOfScope(key, func(v *model.VariableValue) error {
			// Strings, because that is what both writers write. A model that stored
			// the order id as something else is not one this can match, and guessing
			// at a rendering would match things that are not the order.
			if v.Kind != model.VarString {
				return nil
			}
			switch v.Name {
			case progressOrderVar:
				rightOrder = v.Text == orderID
			case progressPositionVar:
				rightPosition = v.Text == position
			}
			return nil
		}); err != nil {
			return err
		}
		if rightOrder && rightPosition {
			hit = key
			return errFoundInstance
		}
		return nil
	})
	if errors.Is(err, errFoundInstance) {
		return hit, true, nil
	}
	if err != nil {
		return 0, false, err
	}
	return 0, false, nil
}

// liveElementsOf lists the BPMN ids the instance is sitting on, and the definition
// they belong to.
//
// It walks the instance's own element index, so it costs the tokens that instance
// holds rather than anything that grows with the server.
//
// What is left out is what holds no token of its own: a subprocess scope and a
// multi-instance body are containers for the elements inside them, an armed event
// subprocess start is a trigger waiting for something that may never happen, and a
// boundary event is attached beside its host rather than being where the work is.
// Reporting any of them would answer "where are you" with the room rather than
// with the desk.
func liveElementsOf(rv *state.ReadView, defs defIndex, instKey uint64) ([]string, uint64, error) {
	var defKey uint64
	seen := map[string]bool{}
	ids := []string{}
	err := rv.ElementInstancesOfProcess(instKey, func(elKey uint64) error {
		ei, ok, err := rv.GetElementInstance(elKey)
		if err != nil || !ok {
			return err
		}
		if !isStep(ei) {
			return nil
		}
		d, ok := defs[ei.ProcessDefKey]
		if !ok || d.cp == nil {
			return nil
		}
		defKey = ei.ProcessDefKey
		id := d.cp.ElementBpmnId(ei.ElementId)
		if id == "" || seen[id] {
			// One element twice is one step: a multi-instance activity running ten
			// iterations is not ten places the position is.
			return nil
		}
		seen[id] = true
		ids = append(ids, id)
		return nil
	})
	return ids, defKey, err
}

// isStep reports whether one live element instance is somewhere a reader would
// call a step.
func isStep(ei *model.ElementInstanceValue) bool {
	if ei.MultiInstance == 1 {
		return false // the body that seeds the iterations, not an iteration
	}
	switch compiler.BpmnType(ei.BpmnElementType) {
	case compiler.TypeSubProcess, compiler.TypeEventSubProcessStart, compiler.TypeBoundaryEvent:
		return false
	}
	return true
}

// elementNameCache holds one deployed definition's element names.
//
// Keyed by definition key, which an installation never reissues (ADR-0339), so an
// entry can never describe a different model than the one it was parsed from. It
// is never invalidated and it is never evicted: the names of a deployed document
// do not change, and the number of deployments is what bounds it.
//
// Its own mutex rather than the run loop, deliberately: parsing a document is
// work that grows with the document, and the single writer must not wait on it
// (I3). What happens on the loop is the pointer copy that hands the bytes over.
type elementNameCache struct {
	mu    sync.RWMutex
	byDef map[uint64]map[string]string
}

// elementNamesOf answers the definition's BPMN id → name map, parsing the
// deployed document once.
//
// The name is read from the document rather than from the compiled process
// because the compiler keeps a name only for user tasks — it is what a task
// inbox needs — and the step somebody's order is sitting on is as often a
// service task or a catch event.
func (s *Server) elementNamesOf(defKey uint64) map[string]string {
	if defKey == 0 {
		return nil
	}
	s.elementNames.mu.RLock()
	names, ok := s.elementNames.byDef[defKey]
	s.elementNames.mu.RUnlock()
	if ok {
		return names
	}
	var raw []byte
	s.do(func() {
		if d, ok := s.deployments[defKey]; ok {
			raw = d.xml
		}
	})
	names = elementNamesIn(raw)
	s.elementNames.mu.Lock()
	if s.elementNames.byDef == nil {
		s.elementNames.byDef = map[uint64]map[string]string{}
	}
	s.elementNames.byDef[defKey] = names
	s.elementNames.mu.Unlock()
	return names
}

// elementNamesIn reads every id/name pair a BPMN document states.
//
// Deliberately indiscriminate: it records the pair wherever both attributes are on
// one element, rather than listing the element types that may carry a name. The
// lookup is by an id the engine is actually sitting on, so an extra pair — a
// message, a signal — can never be returned by mistake, whereas a list of types
// would silently lose whichever type nobody thought of.
//
// A document that will not parse yields no names, and the steps fall back to their
// ids. That is the honest outcome: this runs on a model the compiler already
// accepted, so a failure here says the reader disagrees with the compiler about
// the bytes, and inventing names from that disagreement would be worse than
// showing ids.
func elementNamesIn(raw []byte) map[string]string {
	out := map[string]string{}
	if len(raw) == 0 {
		return out
	}
	dec := xml.NewDecoder(bytes.NewReader(raw))
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		t, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		var id, name string
		for _, a := range t.Attr {
			switch a.Name.Local {
			case "id":
				id = a.Value
			case "name":
				name = a.Value
			}
		}
		if id != "" && name != "" && out[id] == "" {
			out[id] = name
		}
	}
	return out
}
