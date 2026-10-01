package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/engine"
)

// The directed trigger route (ADR-0425).
//
// POST /api/v1/processes/{processId}/triggers/{message} starts the newest deployed
// version of one process at one of its message start events, and answers: the
// instance it started, the instance an earlier delivery of the same trigger started,
// or why it started nothing. It is how a system outside Atlas enters a process at a
// trigger — an HR system reporting a leaver, a ticket asking for a change — where
// POST /api/v1/messages broadcasts by name and answers "published" whether or not
// anything started.
//
// A start event a published catalogue binds as a product's operation is refused
// here, whoever asks. Revoking a product goes through the order and the inventory,
// which record it first; a trigger straight into the process would leave the
// inventory saying somebody holds a right the process has already removed.

type triggerReq struct {
	// TriggerID is the sender's own id for this delivery, required: a retry with the
	// same id answers with the first instance instead of starting another.
	TriggerID string `json:"triggerId"`
	// Source narrows the receipt below the caller, for a caller that relays several
	// systems. The caller's identity always comes first, so one caller can never
	// replay — or read the answer to — another's triggers.
	Source    string         `json:"source"`
	Variables map[string]any `json:"variables"`
}

type triggerResp struct {
	InstanceKey uint64 `json:"instanceKey"`
	Replayed    bool   `json:"replayed,omitempty"`
}

func (s *Server) handleTrigger(w http.ResponseWriter, r *http.Request) {
	processID, message := r.PathValue("processId"), r.PathValue("message")
	body, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().ModelUpload))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var req triggerReq
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if len(body) > 0 {
		if err := dec.Decode(&req); err != nil {
			httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
	}
	if strings.TrimSpace(req.TriggerID) == "" {
		httpapi.Error(w, http.StatusBadRequest, "triggerId is required: it is what makes a "+
			"retry answer with the first instance instead of starting a second")
		return
	}
	caller := principalID(r)
	if caller == "" {
		caller = "anonymous"
	}
	source := "caller:" + caller
	if src := strings.TrimSpace(req.Source); src != "" {
		source += "/" + src
	}
	vars, err := startVarsFromMap(req.Variables)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	var (
		key   uint64
		found bool
		owner string
		opErr error
	)
	s.do(func() {
		if d := s.latestDeploymentOf(processID); d != nil {
			key, found = d.Key, true
		}
		owner, opErr = s.catalogOwnerOfEntry(processID, message)
	})
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read catalogue: "+opErr.Error())
		return
	case owner != "":
		httpapi.Error(w, http.StatusConflict, "start event "+message+" of "+processID+" is the "+
			"product "+owner+"'s operation in the catalogue; it is started through the order and "+
			"the inventory, which record it first, and never by a trigger from outside")
		return
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no deployed process with id "+processID)
		return
	}
	if err := s.encipherStartVars(key, vars); err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.fireTrigger(key, message, source, req.TriggerID, vars)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "trigger: "+err.Error())
		return
	}
	switch res.Outcome {
	case engine.TriggerCreated:
		httpapi.JSON(w, http.StatusCreated, triggerResp{InstanceKey: res.InstanceKey})
	case engine.TriggerReplayed:
		httpapi.JSON(w, http.StatusOK, triggerResp{InstanceKey: res.InstanceKey, Replayed: true})
	case engine.TriggerNoSuchStart:
		httpapi.Error(w, http.StatusNotFound, triggerRefusal(res, processID, message))
	default:
		httpapi.Error(w, http.StatusConflict, triggerRefusal(res, processID, message))
	}
}

// catalogOwnerOfEntry names the product whose operation this start event is, or ""
// when no product binds it. A product that is not active binds nothing an order can
// reach, but it is counted all the same: a draft is about to, and a withdrawn one
// still has holders being returned through it. Reads the catalogue store, so it runs
// on the run loop.
func (s *Server) catalogOwnerOfEntry(processID, message string) (string, error) {
	if s.catalogStore == nil {
		return "", nil
	}
	items, err := s.catalogStore.Items()
	if err != nil {
		return "", err
	}
	for _, it := range items {
		if it.LifecycleProcess != processID {
			continue
		}
		if _, owned := it.OwnsMessage(message); owned {
			return it.ID, nil
		}
	}
	return "", nil
}

// catalogOwnerOfName names the product and the action whose message this is, in any
// lifecycle form, or "" when no product owns it. An inbound watch may not publish such
// a name: a Worker's event would drive the product's lifecycle around the order, and
// the inventory would go on saying what the process had already changed (ADR-0425 §8,
// ADR-0429 §1). Reads the catalogue store, so it runs on the run loop.
func (s *Server) catalogOwnerOfName(message string) (item, action string, err error) {
	if s.catalogStore == nil || strings.TrimSpace(message) == "" {
		return "", "", nil
	}
	items, err := s.catalogStore.Items()
	if err != nil {
		return "", "", err
	}
	for _, it := range items {
		if key, owned := it.OwnsMessage(message); owned {
			return it.ID, key, nil
		}
	}
	return "", "", nil
}

// catalogOwnedRefusal is the 409 a watch meets when a product's action owns its name.
// It names the product and the action, unlike the claim refusal of ADR-0205: a product
// is catalogue data the watch's author can read, and the name is what makes it
// actionable.
func catalogOwnedRefusal(w http.ResponseWriter, message, item, action string) {
	httpapi.JSON(w, http.StatusConflict, map[string]any{
		"error":       "the message name belongs to a catalogue product",
		"messageName": message,
		"details": "Product " + item + "'s action " + action + " starts or waits at this message. " +
			"A Worker's event must not drive a product's lifecycle around the order; publish " +
			"under a different name, or have the system ask the order for the action.",
	})
}
