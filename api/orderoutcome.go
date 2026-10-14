package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/logging"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// How an action ended (ADR-0429 §3).
//
// The process that carries out an action states how it ended — completed, rejected
// or failed — and that ending is an engine fact, kept beside the entitlement
// history and published beyond Atlas under the event type the action declares. This
// is the route for a process that reports over REST; the shop send task (§4) states
// the same through the job it completes.
//
// The provision and the return are not reported here: their statuses are the
// order's own record (`POST /api/v1/orders/{id}/lines/{item}`), and the outcome
// travels with the grant or the revocation that record writes.

// outcomeSourceREST is the source an outcome reported over this route is recorded
// under.
const outcomeSourceREST = "atlas:rest"

// maxOutcomeResult bounds what an outcome may carry. An outcome is published
// beyond Atlas and kept as long as the right it changed; it says how something
// ended, not what was done.
const maxOutcomeResult = 4 << 10

// outcomeReq is the body of a report.
type outcomeReq struct {
	Outcome string          `json:"outcome"`
	Result  json.RawMessage `json:"result"`
}

// outcomeView is one recorded outcome as the routes answer it.
type outcomeView struct {
	CommandID   string          `json:"commandId"`
	Action      string          `json:"action"`
	Effect      string          `json:"effect"`
	Outcome     string          `json:"outcome"`
	EventType   string          `json:"eventType"`
	Source      string          `json:"source"`
	InstanceKey uint64          `json:"instanceKey,omitempty"`
	At          int64           `json:"at"`
	Result      json.RawMessage `json:"result,omitempty"`
	// Replayed says this report repeated one already recorded; nothing was written.
	Replayed bool `json:"replayed,omitempty"`
}

func outcomeViewOf(v model.ActionOutcomeValue) outcomeView {
	out := outcomeView{CommandID: v.CommandID, Action: v.Action, Effect: v.Effect, Outcome: v.Outcome,
		EventType: v.EventType, Source: v.Source, InstanceKey: v.InstanceKey, At: v.At}
	if v.Result != "" {
		out.Result = json.RawMessage(v.Result)
	}
	return out
}

// outcomeValue is the engine record of an order-layer outcome.
func outcomeValue(o order.Outcome, source string) model.ActionOutcomeValue {
	return model.ActionOutcomeValue{
		InstanceKey: o.InstanceKey, At: o.At, OrderID: o.OrderID, Position: o.Position,
		CommandID: o.CommandID, Source: source, Action: o.Action, Effect: o.Effect,
		Outcome: o.Outcome, EventType: o.EventType, Principal: o.Principal,
		ItemID: o.ItemID, VariantID: o.VariantID, Result: o.Result,
	}
}

// errOutcomeConflict is a second, different ending of one command.
type errOutcomeConflict struct{ prior model.ActionOutcomeValue }

func (e errOutcomeConflict) Error() string {
	return "command " + e.prior.CommandID + " already ended " + e.prior.Outcome +
		"; an action ends once"
}

// recordOutcome writes one outcome through the engine and answers what it did: the
// record as it stands, and whether this report only repeated it.
func (s *Server) recordOutcome(o order.Outcome, source string) (engine.OutcomeResult, error) {
	var res engine.OutcomeResult
	s.do(func() { s.proc.ReportActionOutcome(outcomeValue(o, source), &res) })
	if err := s.drive(); err != nil {
		return res, err
	}
	switch res.Answer {
	case engine.OutcomeRecorded, engine.OutcomeReplayed:
		return res, nil
	case engine.OutcomeConflict:
		// The order's own acts write their outcome once per attempt; a conflict there
		// is a status the order reached twice, and the first ending stands.
		if source == orderTriggerSource {
			logging.Warn(logging.OrderOutcomeConflict,
				"an order act ended differently from its recorded outcome; the first stands",
				slog.String("order", o.OrderID), slog.String("position", o.Position),
				slog.String("command", o.CommandID), slog.String("outcome", o.Outcome))
			return res, nil
		}
		return res, errOutcomeConflict{prior: res.Recorded}
	case engine.OutcomeInvalid:
		return res, errors.New("the outcome names no command or no ending")
	}
	return res, errors.New("the outcome was not processed")
}

// handleReportOutcome records how one command of a position ended.
func (s *Server) handleReportOutcome(w http.ResponseWriter, r *http.Request) {
	var req outcomeReq
	if !readActionBody(w, r, maxOutcomeResult+1024, &req) {
		return
	}
	outcome := strings.TrimSpace(req.Outcome)
	if !catalog.KnownOutcome(outcome) {
		httpapi.Error(w, http.StatusBadRequest, "outcome must be one of completed, rejected, failed")
		return
	}
	result, msg := boundedResult(req.Result)
	if msg != "" {
		httpapi.Error(w, http.StatusBadRequest, msg)
		return
	}
	commandID := r.PathValue("commandId")
	h, status, msg := s.lineFor(httpapi.PrincipalFrom(r.Context()), r.PathValue("id"), r.PathValue("item"))
	if status != http.StatusOK {
		httpapi.Error(w, status, msg)
		return
	}
	line := h.line
	inst, ok := line.Command(commandID)
	if !ok {
		httpapi.Error(w, http.StatusNotFound, "position "+h.position+" took no command "+commandID)
		return
	}
	a, ok := line.ActionNamed(inst.Operation)
	switch {
	case !ok:
		httpapi.Error(w, http.StatusConflict, "command "+commandID+" asked for "+inst.Operation+
			", which the line does not declare as an action")
		return
	case a.Effect == catalog.EffectProvision || a.Effect == catalog.EffectDeprovision:
		httpapi.Error(w, http.StatusConflict, "the "+a.Effect+" of a position is reported through "+
			"POST /api/v1/orders/"+h.ord.ID+"/lines/"+h.position+", which records the right it changes")
		return
	}
	res, err := s.recordOutcome(order.Outcome{
		CommandID: commandID, Action: a.Key, Effect: a.Effect, Outcome: outcome,
		EventType: order.EventTypeOf(line.ItemID, a, outcome),
		OrderID:   h.ord.ID, Position: h.position, Principal: h.ord.Recipient,
		ItemID: line.ItemID, VariantID: line.VariantID, InstanceKey: inst.Key,
		At: s.now(), Result: result,
	}, outcomeSourceREST)
	var conflict errOutcomeConflict
	switch {
	case errors.As(err, &conflict):
		httpapi.Error(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "record the outcome: "+err.Error())
		return
	}
	view := outcomeViewOf(res.Recorded)
	view.Replayed = res.Answer == engine.OutcomeReplayed
	httpapi.JSON(w, http.StatusOK, view)
}

// boundedResult checks what an outcome carries: a JSON object of scalars, within
// the bound. It answers the compacted text, or why not.
func boundedResult(raw json.RawMessage) (string, string) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return "", ""
	}
	if len(raw) > maxOutcomeResult {
		return "", "result is larger than 4 KiB; an outcome says how something ended, not what was done"
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return "", "result must be a JSON object"
	}
	for name, v := range fields {
		switch v.(type) {
		case string, float64, bool, nil:
		default:
			return "", "result field " + name + " is not a scalar; an outcome carries scalars an action declares"
		}
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return "", "result must be a JSON object"
	}
	return buf.String(), ""
}

// handleLineOutcomes lists how the commands of one position ended, newest
// command id last, read off the loop.
func (s *Server) handleLineOutcomes(w http.ResponseWriter, r *http.Request) {
	h, status, msg := s.lineFor(httpapi.PrincipalFrom(r.Context()), r.PathValue("id"), r.PathValue("item"))
	if status != http.StatusOK {
		httpapi.Error(w, status, msg)
		return
	}
	out := []outcomeView{}
	err := s.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
		return rv.ActionOutcomesOf(h.ord.ID, h.position, func(v *model.ActionOutcomeValue) error {
			out = append(out, outcomeViewOf(*v))
			return nil
		})
	})
	switch {
	case errors.Is(err, errLoopClosing):
		httpapi.Error(w, http.StatusServiceUnavailable, err.Error())
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read outcomes: "+err.Error())
	default:
		httpapi.JSON(w, http.StatusOK, map[string]any{"position": h.position, "outcomes": out})
	}
}
