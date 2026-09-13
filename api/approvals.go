package api

import (
	"net/http"
	"strconv"

	"github.com/pblumer/atlas/api/brandimage"
	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/api/taskfolder"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// The approver's own surface (ADR-0311).
//
// An approval is a user task like any other, and the Console can work it. That is
// the wrong place for most of the people who get one. The approver is a fixed
// integration manager, a named group, or — the common case — the line manager the
// directory resolved, who approves perhaps four times a year and for whom a tool
// with Deployments, Instances and Incidents in it is a tool they will ask a
// colleague to use for them.
//
// So there is a page, and this is what it reads. One call answers everything it
// shows: which approvals are the caller's, what each one decides, and the brand
// of the catalogue the order came from.
//
// # Why the join happens here
//
// The chain is task → order → release → catalogue, and the approver may walk none
// of it themselves. They are not the orderer, so the order is not theirs to read;
// they are not the catalogue's audience, so neither is the catalogue. Each of
// those refusals is correct and none of them should be relaxed — a customer's
// brand is not shown to other customers, which is the whole point of the logo
// gate. What is legitimate is the *answer*: somebody holding the approval may see
// what they are approving. So the join is done on the server, under the one right
// the approver actually has, and nothing else opens.

// approvalOrderVar is the variable every portal approval carries: the order it
// belongs to. An instance without it is not deciding an order, whatever it is
// called.
const approvalOrderVar = "orderId"

// approvalResp is one open approval: the task, what it decides, and how the
// catalogue it belongs to looks.
type approvalResp struct {
	Task taskResp `json:"task"`
	// OrderID and ItemID name the line this approval decides; Recipient and Orderer
	// are principal ids, never names — see ADR-0314.
	OrderID   string `json:"orderId"`
	ItemID    string `json:"itemId"`
	VariantID string `json:"variantId,omitempty"`
	Recipient string `json:"recipient,omitempty"`
	Orderer   string `json:"orderer,omitempty"`
	// Texts is the ordered product's name per language, as the release froze it. An
	// approver deciding "vpn-zugang" is reading an id; this is the same product in
	// words somebody chose.
	Texts map[string]string `json:"texts,omitempty"`
	// CatalogID, CatalogTexts and Languages place the order: which catalogue, called
	// what, offered in which languages.
	CatalogID    string            `json:"catalogId,omitempty"`
	CatalogTexts map[string]string `json:"catalogTexts,omitempty"`
	Languages    []string          `json:"languages,omitempty"`
	// Theme is the catalogue's brand. The mark is not here — it is bytes, served
	// from this approval's own logo route under this same gate.
	Theme catalog.Theme `json:"theme"`
	// Assignment is where this approval has been, when a deadline or a person has
	// moved it (escalation.go). Absent while it is still with whoever it started
	// with, which is the ordinary case — and its absence is therefore the answer
	// "nobody has had to chase this".
	Assignment *order.Assignment `json:"assignment,omitempty"`
}

// handleListApprovals answers the approval page: every open approval the caller
// holds, newest first, with what it decides and the brand it wears.
//
// It walks the open user tasks off the run loop, because that walk grows with the
// open-task population and the single writer must not do it (ADR-0239). The
// enrichment is bounded by what survives the walk — the tasks this one person
// holds — not by the population.
func (s *Server) handleListApprovals(w http.ResponseWriter, r *http.Request) {
	pr := httpapi.PrincipalFrom(r.Context())

	var before uint64
	if v := r.URL.Query().Get("before"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			httpapi.Error(w, http.StatusBadRequest, "invalid before cursor (want a job key)")
			return
		}
		before = n
	}

	// Two passes. The first keeps the tasks this caller holds, which needs nothing
	// but the compiled assignment metadata the walk already produced; the second
	// asks each survivor's instance what order it is deciding. Reading variables
	// inside the walk would mean a store read per open task on the server.
	type candidate struct {
		key uint64
		tr  taskResp
	}
	var held []candidate
	var nextCursor uint64
	budgetHit, err := s.visitOpenTasks(before, false, func(jobKey uint64, tr taskResp, _ taskfolder.Task) bool {
		nextCursor = jobKey
		if s.holdsApproval(pr, tr) {
			held = append(held, candidate{key: jobKey, tr: tr})
		}
		return true
	})
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list approvals: "+err.Error())
		return
	}

	out := []approvalResp{}
	readErr := s.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
		for _, c := range held {
			a, ok, err := s.approvalOf(rv, c.tr)
			if err != nil {
				return err
			}
			if ok {
				out = append(out, a)
			}
		}
		return nil
	})
	if readErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read approvals: "+readErr.Error())
		return
	}
	if budgetHit {
		w.Header().Set("X-Tasks-Truncated", "true")
		w.Header().Set("X-Tasks-Next-Cursor", strconv.FormatUint(nextCursor, 10))
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// holdsApproval is the listing's filter: a task addressed to this caller. It is
// deliberately stricter than the inbox, which shows every open task to anybody
// signed in — this page is one person's approvals, and an unaddressed task is
// nobody's approval.
func (s *Server) holdsApproval(pr *httpapi.Principal, tr taskResp) bool {
	if !s.authEnabled {
		return true
	}
	if pr == nil {
		return false
	}
	if tr.Assignee == "" && tr.CandidateGroups == "" {
		return false
	}
	return s.holdsTask(pr, tr.Assignee, tr.CandidateGroups)
}

// approvalOf turns one held task into an approval, or reports that it is not one.
//
// What makes a task an approval is not its name or its process id but the order:
// the instance carries an orderId and an itemId, the order has that line, and the
// line says this process is what decides it. That is general on purpose — an
// installation that approves through its own model binds a product to it by name
// (see [order.Line.ApprovalProcess]) and lands here without a change.
func (s *Server) approvalOf(rv *state.ReadView, tr taskResp) (approvalResp, bool, error) {
	vars := map[string]string{}
	err := rv.VisibleVariablesOfScope(tr.ProcessInstanceKey, func(v *model.VariableValue) error {
		switch v.Name {
		case approvalOrderVar, "itemId", "variantId", "recipient", "orderer":
			if s, ok := nativeVar(v).(string); ok {
				vars[v.Name] = s
			}
		}
		return nil
	})
	if err != nil || vars[approvalOrderVar] == "" || vars["itemId"] == "" {
		return approvalResp{}, false, err
	}

	ord, ok, err := s.orderStore.Get(vars[approvalOrderVar])
	if err != nil || !ok {
		return approvalResp{}, false, err
	}
	var line order.Line
	for _, l := range ord.Lines {
		if l.ItemID == vars["itemId"] {
			line = l
			break
		}
	}
	// The order has to agree that this process decides this line. Without that an
	// ordinary user task on an order's own process — a provisioning step that asks
	// somebody to do something by hand — would arrive here dressed as an approval.
	if line.ItemID == "" || line.ApprovalProcess() != tr.ProcessID {
		return approvalResp{}, false, nil
	}

	a := approvalResp{
		Task: tr, OrderID: ord.ID, ItemID: line.ItemID, VariantID: vars["variantId"],
		Recipient: vars["recipient"], Orderer: vars["orderer"],
	}
	if as, ok := ord.AssignmentFor(line.ItemID); ok {
		a.Assignment = &as
	}
	rel, ok, err := s.catalogStore.Release(ord.ReleaseID)
	if err != nil {
		return approvalResp{}, false, err
	}
	if ok {
		for _, it := range rel.Items {
			if it.ID == line.ItemID {
				a.Texts = it.Texts
				break
			}
		}
		a.CatalogID = rel.CatalogID
		cat, found, cErr := s.catalogStore.Catalog(rel.CatalogID)
		if cErr != nil {
			return approvalResp{}, false, cErr
		}
		if found {
			a.CatalogTexts, a.Languages, a.Theme = cat.Texts, cat.Languages, cat.Theme
		}
	}
	return a, true, nil
}

// handleApprovalLogo serves the brand mark of the catalogue an approval's order
// came from, or 404 when there is none.
//
// It exists because the catalogue's own logo route cannot serve it: that one asks
// for the catalogue's read right, and an approver is not the catalogue's audience
// — a line manager approves a request for a customer group they are not in.
// Opening the catalogue route to them would open one customer's mark to everybody
// who ever holds a task. So the mark travels under the gate the approver actually
// passes, which is the task they hold, and nothing else widens.
func (s *Server) handleApprovalLogo(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid task key")
		return
	}
	auth, authErr := s.mayWorkTask(r, key)
	if s.refuseTaskWork(w, auth, authErr) {
		return
	}

	var (
		tr    taskResp
		found bool
	)
	s.do(func() {
		jv, ok, gErr := s.store.GetJob(key)
		if gErr != nil || !ok {
			err = gErr
			return
		}
		tr, found = s.enrichTask(key, jv), true
	})
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read task: "+err.Error())
		return
	}
	if !found {
		httpapi.Error(w, http.StatusNotFound, "no open task with that key")
		return
	}

	var a approvalResp
	isApproval := false
	if readErr := s.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
		var rErr error
		a, isApproval, rErr = s.approvalOf(rv, tr)
		return rErr
	}); readErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read approval: "+readErr.Error())
		return
	}
	if !isApproval || a.CatalogID == "" {
		httpapi.Error(w, http.StatusNotFound, "that task decides no catalogue order")
		return
	}

	data, ct, has, logoErr := s.catalogStore.Logo(a.CatalogID)
	switch {
	case logoErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read logo: "+logoErr.Error())
	case !has:
		httpapi.Error(w, http.StatusNotFound, "that catalogue has no logo")
	default:
		brandimage.Serve(w, ct, data)
	}
}
