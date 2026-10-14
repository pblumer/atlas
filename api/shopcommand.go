package api

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// The shop command task, served in-process (ADR-0429 §4, §10 decision 1).
//
// A process that does not carry a position issues one of its actions — an HR leaver
// process returning a mailbox, a maintenance process resetting a password. A process
// has no caller in the sense the order act checks, so it acts in the name of its
// application: the product must list that application among those that may command
// it, and only an action an operator or a system may ask for can be issued so, never
// one that is the customer's alone. The list is read from the product's newest
// release when the task runs, so taking an application off it stops it at once.
//
// The task goes through the order act — the same checks of the position and the same
// in-process trigger or delivery — which waits on the run loop. That is why it is a
// job type of its own, registered with job.Runner.HandleOffLoop: a drive that holds the
// loop leaves it to the next round that does not.
//
// The command id is the job's key, which survives the job's retries, so a retried
// task asks again under the same trigger and is answered with the first instance
// instead of asking twice.

// shopCommandID is the command id a shop command task asks under.
func shopCommandID(jobKey uint64) string { return "task-" + strconv.FormatUint(jobKey, 10) }

// shopCommandHandler builds the job handler for shop command tasks.
func (s *Server) shopCommandHandler(rd state.Reader) job.CompletingHandler {
	return func(j job.Job) (job.Completion, error) {
		ei, ok, err := rd.GetElementInstance(j.ElementInstanceKey)
		if err != nil {
			return job.Completion{}, err
		}
		if !ok {
			return job.Completion{}, nil // the task is gone; nothing to ask
		}
		cp := s.processLookup(ei.ProcessDefKey)
		if cp == nil {
			return job.Completion{}, fmt.Errorf("shop command: no compiled process for def %d", ei.ProcessDefKey)
		}
		detail, err := cp.ConnectorTaskOf(ei.ElementId)
		if err != nil {
			return job.Completion{}, fmt.Errorf("shop command: %w", err)
		}
		scope, err := state.VisibleVariablesMap(rd, j.ElementInstanceKey)
		if err != nil {
			return job.Completion{}, fmt.Errorf("shop command: read variables: %w", err)
		}
		orderID := strings.TrimSpace(userResolve(detail.ShopOrder, ei.ProcessInstanceKey, scope))
		ref := strings.TrimSpace(userResolve(detail.ShopPosition, ei.ProcessInstanceKey, scope))
		app, err := s.applicationOf(ei.ProcessDefKey)
		if err != nil {
			return job.Completion{}, fmt.Errorf("shop command: %w", err)
		}
		commandID, err := s.commandLine(app, orderID, ref, detail.ShopProduct, detail.ShopAction, shopCommandID(j.Key))
		if err != nil {
			return job.Completion{}, fmt.Errorf("shop command: %w", err)
		}
		var outputs []model.VariableValue
		if detail.ShopResultVar != "" {
			outputs = append(outputs, model.VariableValue{Name: detail.ShopResultVar, Kind: model.VarString, Text: commandID})
		}
		return job.Completion{Outputs: outputs}, nil
	}
}

// applicationOf is the portable key of the application the process definition was
// deployed into, or "" for one deployed into none.
func (s *Server) applicationOf(defKey uint64) (string, error) {
	var (
		key string
		err error
	)
	s.do(func() {
		d := s.deployments[defKey]
		if d == nil || d.ProjectID == "" {
			return
		}
		app, found, getErr := s.projects.Get(d.ProjectID)
		if getErr != nil || !found {
			err = getErr
			return
		}
		key, err = s.applicationKeyFor(app)
	})
	return key, err
}

// commandLine asks position ref of order orderID for the action key of product, in
// the name of application app, under commandID. It answers the command id the action
// runs under — commandID, or for a return the order's own attempt id — or why it was
// refused.
func (s *Server) commandLine(app, orderID, ref, product, key, commandID string) (string, error) {
	if app == "" {
		return "", errors.New("the process belongs to no application; only an application a product " +
			"lists may command it")
	}
	if orderID == "" || ref == "" {
		return "", fmt.Errorf("the task resolved order %q and position %q; it needs both", orderID, ref)
	}
	var (
		h         heldLine
		found     bool
		listed    bool
		refusal   string
		opErr     error
		unchanged bool
		attempt   string
	)
	s.do(func() {
		o, ok, err := s.orderStore.Get(orderID)
		if err != nil || !ok {
			opErr = err
			return
		}
		found = true
		position, err := order.ResolveLine(o, ref)
		if err != nil {
			refusal = err.Error()
			return
		}
		for _, l := range o.Lines {
			if l.Key() == position {
				h = heldLine{ord: o, line: l, position: position}
			}
		}
		if h.line.ItemID != product {
			refusal = "position " + position + " of order " + orderID + " holds " + h.line.ItemID +
				", not " + product
			return
		}
		if listed, opErr = s.commandedBy(o.ReleaseID, product, app); opErr != nil || !listed {
			return
		}
		if a, ok := h.line.ActionNamed(key); ok && a.Effect == catalog.EffectDeprovision {
			unchanged, attempt = returnUnderWay(h.line, orderID, position)
		}
	})
	switch {
	case opErr != nil:
		return "", fmt.Errorf("read order %s: %w", orderID, opErr)
	case !found:
		return "", fmt.Errorf("no order %s", orderID)
	case refusal != "":
		return "", errors.New(refusal)
	case !listed:
		return "", fmt.Errorf("product %s does not list application %s among those that may command it", product, app)
	}
	a, ok := h.line.ActionNamed(key)
	switch {
	case !ok:
		return "", fmt.Errorf("product %s declares no action %s for this position", product, key)
	case a.Effect == catalog.EffectProvision:
		return "", errors.New("the provision action is the order's own: it runs when the position is " +
			"fulfilled and is not commanded")
	case !a.TriggeredBy(catalog.TriggerOperator) && !a.TriggeredBy(catalog.TriggerSystem):
		return "", fmt.Errorf("action %s of product %s is asked for by %s; a process commands only what "+
			"an operator or a system may ask for", key, product, strings.Join(a.Triggers, ", "))
	case a.Effect == catalog.EffectDeprovision && unchanged:
		// Already given back, or on its way: what the command wants is under way. A
		// retried task lands here too, after its first attempt started the return.
		return attempt, nil
	case a.Effect == catalog.EffectDeprovision:
		return s.commandReturn(app, orderID, h.position)
	case h.line.Status != order.StatusDone:
		return "", fmt.Errorf("position %s is %s; an action is asked only of a held right", h.position, h.line.Status)
	}
	if _, status, msg := s.fireAction(h, orderID, key, commandID, "asked by application "+app, nil); status != http.StatusOK {
		return "", errors.New(msg)
	}
	return commandID, nil
}

// commandedBy reports whether the newest release of the catalogue order release
// releaseID belongs to lists application app among those that may command product.
// Runs on the loop.
func (s *Server) commandedBy(releaseID, product, app string) (bool, error) {
	rel, ok, err := s.catalogStore.Release(releaseID)
	if err != nil || !ok {
		return false, err
	}
	rels, err := s.catalogStore.ReleasesOf(rel.CatalogID)
	if err != nil || len(rels) == 0 {
		return false, err
	}
	for _, it := range rels[0].Items {
		if it.ID == product {
			return slices.Contains(it.CommandedBy, app), nil
		}
	}
	return false, nil
}

// returnUnderWay reports whether line is already given back or on its way back, and
// the attempt id its newest return runs under.
func returnUnderWay(line order.Line, orderID, position string) (bool, string) {
	if line.Status != order.StatusReturning && line.Status != order.StatusReturned {
		return false, ""
	}
	return true, positionTriggerID(orderID, position, catalog.OpDeprovision, line.StartsOf(catalog.OpDeprovision))
}

// commandReturn gives back position of order orderID the way the return route does:
// the line says a return is under way before anything runs, then the frozen
// deprovisioning starts.
func (s *Server) commandReturn(app, orderID, position string) (string, error) {
	var (
		out     order.Order
		binding catalog.Binding
		opErr   error
	)
	at := s.now()
	s.do(func() {
		ord, ok, err := s.orderStore.Get(orderID)
		switch {
		case err != nil:
			opErr = err
			return
		case !ok:
			opErr = errors.New("no order " + orderID)
			return
		}
		binding = order.ReturnBindingOf(ord, position)
		if out, opErr = order.Returning(ord, position, at, "application:"+app); opErr != nil {
			return
		}
		opErr = s.orderStore.Save(out)
	})
	if opErr != nil {
		return "", opErr
	}
	return s.startReturnAttempt(binding, orderID, position, out, "asked by application "+app)
}
