package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/limits"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// The recertification routes (ADR-draft-access-recertification).
//
// Opening a campaign is a population-sized read of the inventory, off the loop,
// exactly as a reconciliation is — and affordable for the same reason turned
// around: it happens once per campaign rather than once per decision.
//
// Reading and deciding are separated by who may do them. A campaign is opened and
// closed by whoever runs the estate; a row is decided by the person it was
// addressed to, who is an ordinary user and very often nobody else's idea of an
// administrator.

// handleOpenRecertification builds a campaign from the inventory.
func (s *Server) handleOpenRecertification(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().Recertify))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var msg recertifyOpen
	if err := json.Unmarshal(body, &msg); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	msg.Name = strings.TrimSpace(msg.Name)
	if msg.Name == "" {
		httpapi.Error(w, http.StatusBadRequest,
			"name this campaign (\"name\"). It is what somebody cites a year later when they ask "+
				"who certified a right and when; a campaign identified only by an id is one "+
				"nobody can refer to")
		return
	}

	by := principalID(r)
	now := time.Now().Unix()

	var (
		in     recertifyInput
		ran    bool
		runErr error
	)
	s.do(func() {
		ran = true
		if in.Users, runErr = s.users.LoadAll(); runErr != nil {
			return
		}
		// The open findings, so a row can say that the right it asks about is
		// currently contested. Read here rather than at render time, because what is
		// being recorded is what the reviewer was shown.
		open, err := s.discrepancies.open()
		if err != nil {
			runErr = err
			return
		}
		in.Disputes = make(map[string]discrepancyRecord, len(open))
		for _, d := range open {
			in.Disputes[disputeKey(d.Principal, d.ItemID)] = d
		}
	})
	if !ran {
		httpapi.Error(w, http.StatusServiceUnavailable, "recertify: this server is shutting down")
		return
	}
	if runErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "recertify: "+runErr.Error())
		return
	}

	// The inventory, whole, off the loop — [state.queries.Entitlements], the walk
	// ADR-0334 added. "Who holds what" cannot be answered from the entitlement key
	// any other way, and holding the single writer for the length of it would stop
	// process execution while a campaign is built.
	if err := s.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
		return rv.Entitlements(func(v *model.EntitlementValue) error {
			in.Held = append(in.Held, *v)
			return nil
		})
	}); err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "recertify: read the inventory: "+err.Error())
		return
	}

	cmp := buildCampaign(msg, in, by, now)
	if n := len(cmp.Rows); n > int(s.budgets().RecertifyRows) {
		// Refused whole rather than truncated. Half a campaign is the shape of
		// failure this whole line of work exists to avoid: it looks like a complete
		// question set, it is signed as one, and the rights it silently left out are
		// recorded as having been reviewed by nobody — except that nobody can tell,
		// because the campaign says it is finished.
		httpapi.Error(w, http.StatusRequestEntityTooLarge, fmt.Sprintf(
			"this campaign would ask %d questions and the ceiling is %d (%s). It is refused whole "+
				"rather than shortened: a campaign that quietly dropped the rest would be signed "+
				"as complete while covering part of the estate. Narrow it with \"items\" or "+
				"\"principals\", or raise the ceiling deliberately",
			n, s.budgets().RecertifyRows, limits.EnvVar("RecertifyRows")))
		return
	}

	var (
		wrote    bool
		writeErr error
	)
	s.do(func() {
		wrote = true
		writeErr = s.recertifications.saveCampaign(cmp)
	})
	if !wrote {
		httpapi.Error(w, http.StatusServiceUnavailable, "recertify: this server is shutting down")
		return
	}
	if writeErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "recertify: "+writeErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusCreated, recertifyReportOf(cmp, s.budgets()))
}

// handleListRecertifications answers what campaigns exist, newest first.
func (s *Server) handleListRecertifications(w http.ResponseWriter, r *http.Request) {
	var (
		out     []recertifyCampaign
		ran     bool
		loadErr error
	)
	s.do(func() {
		ran = true
		out, loadErr = s.recertifications.listCampaigns()
	})
	if !ran {
		httpapi.Error(w, http.StatusServiceUnavailable, "recertifications: this server is shutting down")
		return
	}
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "recertifications: "+loadErr.Error())
		return
	}
	if out == nil {
		out = []recertifyCampaign{}
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// handleReadRecertification answers one campaign with its rows.
//
// `?mine=true` narrows it to the rows the caller is answerable for, which is what
// a reviewer wants and what the screen asks for: a manager opening a five-thousand
// row campaign to find their four is being asked to work for the software.
func (s *Server) handleReadRecertification(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cmp, found, err := s.loadCampaign(id)
	switch {
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "recertification: "+err.Error())
		return
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no campaign "+id)
		return
	}
	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("mine")), "true") {
		me := principalID(r)
		kept := make([]recertifyRow, 0, len(cmp.Rows))
		for _, row := range cmp.Rows {
			if s.mayDecideRow(r, cmp, row, me) {
				kept = append(kept, row)
			}
		}
		cmp.Rows = kept
	}
	httpapi.JSON(w, http.StatusOK, recertifyReportOf(cmp, s.budgets()))
}

// handleCloseRecertification declares a campaign over.
//
// It changes nothing about the rows. A campaign closes with undecided rows still in
// it — that is the normal ending, and the number is the finding. Closing that
// certified the remainder would manufacture the signature this record exists to
// prevent; closing that revoked it would take access away because somebody was on
// holiday.
func (s *Server) handleCloseRecertification(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cmp, found, err := s.loadCampaign(id)
	switch {
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "recertification: "+err.Error())
		return
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no campaign "+id)
		return
	case !cmp.Open():
		httpapi.Error(w, http.StatusConflict, fmt.Sprintf("campaign %s is already closed", id))
		return
	}

	now := time.Now().Unix()
	header := cmp
	header.Rows = nil
	header.ClosedAt, header.ClosedBy, header.UpdatedAt = now, principalID(r), now

	var saveErr error
	s.do(func() { saveErr = s.recertifications.campaigns.Save(header) })
	if saveErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "close the campaign: "+saveErr.Error())
		return
	}
	cmp.ClosedAt, cmp.ClosedBy = header.ClosedAt, header.ClosedBy
	httpapi.JSON(w, http.StatusOK, recertifyReportOf(cmp, s.budgets()))
}

// loadCampaign reads one campaign on the loop.
func (s *Server) loadCampaign(id string) (recertifyCampaign, bool, error) {
	var (
		cmp   recertifyCampaign
		found bool
		ran   bool
		err   error
	)
	s.do(func() {
		ran = true
		cmp, found, err = s.recertifications.campaign(id)
	})
	if !ran {
		return recertifyCampaign{}, false, fmt.Errorf("this server is shutting down")
	}
	return cmp, found, err
}

// recertifyReport is a campaign as somebody reads it.
type recertifyReport struct {
	recertifyCampaign
	Counts recertifyCounts `json:"counts"`
	// Reason explains a campaign that asks nothing, which otherwise reads as an
	// estate with nothing in it.
	Reason string `json:"reason,omitempty"`
	// Omitted says the rows were cut for reading rather than lost. A campaign is
	// answered row by row through its own route, so a truncated *view* costs
	// nothing — unlike a truncated campaign, which is refused outright.
	Omitted int `json:"omitted,omitempty"`
}

func recertifyReportOf(cmp recertifyCampaign, budgets limits.Limits) recertifyReport {
	rep := recertifyReport{recertifyCampaign: cmp, Counts: countRows(cmp.Rows)}
	if rep.Rows == nil {
		rep.Rows = []recertifyRow{}
	}
	if limit := int(budgets.RecertifyReport); len(rep.Rows) > limit {
		rep.Omitted = len(rep.Rows) - limit
		rep.Rows = rep.Rows[:limit]
	}
	rep.Reason = recertifyReason(cmp)
	return rep
}

// recertifyReason explains a campaign with no questions in it.
//
// An empty campaign and a healthy estate look identical from the outside, and the
// usual cause is neither: a scope naming a product id that does not exist, or a
// person who holds nothing. Saying so is the difference between "there is nothing
// to certify" and "you asked about nothing".
func recertifyReason(cmp recertifyCampaign) string {
	if len(cmp.Rows) > 0 {
		return ""
	}
	switch {
	case len(cmp.Items) == 0 && len(cmp.Principals) == 0:
		return "this campaign covers the whole inventory and the inventory is empty: nothing here " +
			"records anybody holding anything. On a new installation that is the expected answer " +
			"and the commissioning load is what fills it"
	default:
		return fmt.Sprintf("nothing in scope is held by anybody: the campaign named %d product(s) "+
			"and %d person(s), and the inventory records no right that matches. Check the product "+
			"ids and how the people were named before reading this as an estate with nothing to "+
			"certify", len(cmp.Items), len(cmp.Principals))
	}
}
