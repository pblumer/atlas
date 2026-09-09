package api

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/mimimport"
)

// mimNoteResp is one node's conversion outcome, mirrored from mimimport.Note so
// the web UI can render a per-node report with status badges.
type mimNoteResp struct {
	NodeID   string `json:"nodeId"`
	Activity string `json:"activity"`
	Kind     string `json:"kind"`
	Status   string `json:"status"` // native | preserved | manual-review
	Detail   string `json:"detail,omitempty"`
}

// mimReportResp is the JSON form of a mimimport.Report: the per-status counts,
// the ordered notes, and any document-level warning (such as an input that only
// parsed after its unquoted attribute values were repaired).
type mimReportResp struct {
	Native       int           `json:"native"`
	Preserved    int           `json:"preserved"`
	ManualReview int           `json:"manualReview"`
	Warnings     []string      `json:"warnings,omitempty"`
	Notes        []mimNoteResp `json:"notes"`
}

// mimDraftResp is one draft an import created: its identity, its conversion
// report, the MIM resource it came from, and what it landed on.
type mimDraftResp struct {
	ProcessID string                `json:"processId"`
	Name      string                `json:"name"`
	ProjectID string                `json:"projectId,omitempty"`
	SavedAt   int64                 `json:"savedAt"`
	Report    mimReportResp         `json:"report"`
	Source    *mimimport.SourceInfo `json:"source,omitempty"`
	Impact    mimImpact             `json:"impact"`
}

// mimImportResp is the response of POST /api/v1/imports/mim. It echoes the first
// draft flat — a single-workflow import is the common case, and a client that
// predates multi-workflow exports reads these fields — and lists every draft the
// import created, because an Export-FIMConfig export holds one WorkflowDefinition
// per workflow and all of them are converted.
type mimImportResp struct {
	ProcessID string        `json:"processId"`
	Name      string        `json:"name"`
	ProjectID string        `json:"projectId,omitempty"`
	SavedAt   int64         `json:"savedAt"`
	Report    mimReportResp `json:"report"`

	Drafts []mimDraftResp `json:"drafts"`
	// Overwrote is what the import replaced, when the caller asked it to.
	Overwrote []mimImpact `json:"overwrote,omitempty"`
}

// candidateOf is one converted workflow on its way to becoming a draft: what it
// compiled to, where it would be saved, and what it declares.
type candidateOf struct {
	res      mimimport.Result
	pid      string
	name     string
	identity modelIdentity
}

// mimConflictResp is the 409 an import answers with when something already holds
// an id it would write to. Nothing has been written; the caller decides, and
// repeats the request with overwrite=true to go through with it.
type mimConflictResp struct {
	Reason  string      `json:"reason"`
	Impacts []mimImpact `json:"impacts"`
}

// handleImportMIM converts an uploaded Microsoft Identity Manager (MIM/FIM) XOML
// workflow — or an Export-FIMConfig XML, which may embed several — into BPMN via
// the mimimport package and saves each as a draft, returning the draft identities
// and a per-node conversion report.
//
// It never deploys: a model lands as a draft the author reviews and deploys
// deliberately, exactly like the "Import file…" path (handleSaveDraft). An
// optional ?name= overrides the process name (only when the input carries one
// workflow — one name cannot stand for several) and ?projectId= files the drafts
// under a project.
//
// Before writing anything it works out what each id already holds, and refuses
// with 409 rather than replacing a draft or landing on a deployed process's id
// unasked; ?overwrite=true is the caller saying to go ahead. See
// mimimport_preflight.go for what that impact covers and why it is worth
// knowing before the fact rather than after.
func (s *Server) handleImportMIM(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().ModelUpload))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if len(body) == 0 {
		httpapi.Error(w, http.StatusBadRequest, "empty request body: expected MIM/FIM XOML")
		return
	}

	// Phase 1, off the run loop: convert, and compile each model far enough to say
	// what it declares. Neither is the single writer's work.
	results, err := mimimport.ConvertAll(bytes.NewReader(body), r.URL.Query().Get("name"))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "convert XOML: "+err.Error())
		return
	}
	cands := make([]candidateOf, 0, len(results))
	for _, res := range results {
		pid, name := processIdentity(res.BPMN)
		if pid == "" {
			// The converter always emits a <process id>, so an empty id means the input
			// produced no recognisable workflow at all.
			httpapi.Error(w, http.StatusBadRequest, "no workflow found in the uploaded XOML")
			return
		}
		// A draft does not have to compile — an author may want to fix one in the
		// Modeler — so a model that does not is imported all the same. It just
		// declares nothing this preflight can credit it with, which makes the impact
		// below the conservative answer rather than a wrong one.
		identity, err := identifyModel(res.BPMN)
		if err != nil {
			res.Report.Warnings = append(res.Report.Warnings,
				"the converted model does not compile, so the impact of importing it is reported as if it declared nothing: "+err.Error())
		}
		cands = append(cands, candidateOf{res: res, pid: pid, name: name, identity: identity})
	}

	// Two workflows of one export can carry the same name, and a process id comes
	// from that name — so without this the second would land on the first inside a
	// single import, and overwrite=true would not help: only one could survive.
	if dup := duplicateProcessID(cands); dup != "" {
		httpapi.Error(w, http.StatusConflict, fmt.Sprintf(
			"two of the imported workflows would both be saved as process %q — rename one in MIM, or import them separately", dup))
		return
	}

	projectID := r.URL.Query().Get("projectId")
	overwrite := r.URL.Query().Get("overwrite") == "true"

	// Phase 2, on the loop: read what each id already holds. Nothing is written.
	var (
		impacts          []mimImpact
		existing         []draft // the draft on each id, zero value when free
		readErr, projErr error
		unknownProject   bool
		protectedProject bool
	)
	s.do(func() {
		if projectID != "" {
			proj, ok, e := s.projects.Get(projectID)
			switch {
			case e != nil:
				projErr = e
				return
			case !ok:
				unknownProject = true
				return
			case proj.Protected:
				// A protected system project's content is platform-managed (ADR-0122).
				protectedProject = true
				return
			}
		}
		for _, c := range cands {
			impact, d, e := s.mimImpactOf(c.pid, c.name, c.identity)
			if e != nil {
				readErr = e
				return
			}
			impacts = append(impacts, impact)
			existing = append(existing, d)
		}
	})
	switch {
	case projErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read project: "+projErr.Error())
		return
	case unknownProject:
		httpapi.Error(w, http.StatusBadRequest, "unknown project id")
		return
	case protectedProject:
		httpapi.Error(w, http.StatusForbidden, "protected system project cannot be modified")
		return
	case readErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read what the import would land on: "+readErr.Error())
		return
	}

	// Phase 3, off the loop: replacing a draft needs editor on it, exactly as
	// saving over one does (ADR-0071) — an import must not be a way around that.
	var occupied []mimImpact
	for i, impact := range impacts {
		if impact.Draft != nil {
			if code, msg := s.authorizeArtifact(r, existing[i].ProjectID, existing[i].OwnerID, ScopeRoleEditor); code != 0 {
				httpapi.Error(w, code, msg)
				return
			}
		}
		if impact.occupied() {
			occupied = append(occupied, impact)
		}
	}
	if len(occupied) > 0 && !overwrite {
		httpapi.JSON(w, http.StatusConflict, mimConflictResp{
			Reason:  conflictReason(occupied),
			Impacts: occupied,
		})
		return
	}

	// Phase 4, on the loop: write.
	at, by := time.Now().Unix(), principalID(r)
	drafts := make([]mimDraftResp, 0, len(cands))
	var saveErr error
	s.do(func() {
		for i, c := range cands {
			owner := existing[i].OwnerID
			if owner == "" {
				owner = by
			}
			proj := projectID
			if proj == "" && impacts[i].Draft != nil {
				// Overwriting a draft that is filed somewhere must not drop it into
				// Ungrouped, the same rule handleSaveDraft follows on a rename.
				proj = existing[i].ProjectID
			}
			rec := draft{ProcessID: c.pid, Name: c.name, ProjectID: proj, OwnerID: owner, SavedAt: at, XML: string(c.res.BPMN)}
			if saveErr = s.drafts.Save(rec); saveErr != nil {
				return
			}
			src := c.res.Report.Source
			resp := mimDraftResp{
				ProcessID: rec.ProcessID, Name: rec.Name, ProjectID: rec.ProjectID, SavedAt: rec.SavedAt,
				Report: toMIMReport(c.res.Report), Impact: impacts[i],
			}
			if src != (mimimport.SourceInfo{}) {
				resp.Source = &src
			}
			drafts = append(drafts, resp)
		}
	})
	if saveErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "save draft: "+saveErr.Error())
		return
	}

	resp := mimImportResp{Drafts: drafts}
	if len(drafts) > 0 {
		resp.ProcessID, resp.Name = drafts[0].ProcessID, drafts[0].Name
		resp.ProjectID, resp.SavedAt, resp.Report = drafts[0].ProjectID, drafts[0].SavedAt, drafts[0].Report
	}
	if overwrite {
		resp.Overwrote = occupied
	}
	httpapi.JSON(w, http.StatusOK, resp)
}

// duplicateProcessID returns the first process id two candidates share, or "".
func duplicateProcessID(cands []candidateOf) string {
	seen := map[string]bool{}
	for _, c := range cands {
		if seen[c.pid] {
			return c.pid
		}
		seen[c.pid] = true
	}
	return ""
}

// conflictReason says in one sentence what stands in the import's way, so a
// caller that renders nothing else still tells its user something true.
func conflictReason(occupied []mimImpact) string {
	reason := fmt.Sprintf("%d of the imported workflows would land on an id that is already taken", len(occupied))
	if len(occupied) == 1 {
		reason = mimImpactSummary(occupied[0])
	}
	// The running instances are the part worth putting in the one line a caller
	// might show on its own.
	instances := 0
	for _, i := range occupied {
		if i.atRisk() {
			instances += i.Deployed.ActiveInstances
		}
	}
	if instances > 0 {
		reason += fmt.Sprintf(" — %d running instance(s) stand on elements this model does not have", instances)
	}
	return reason
}

func toMIMReport(rep mimimport.Report) mimReportResp {
	out := mimReportResp{
		Native:       rep.Count(mimimport.StatusNative),
		Preserved:    rep.Count(mimimport.StatusPreserved),
		ManualReview: rep.Count(mimimport.StatusManualReview),
		Warnings:     rep.Warnings,
		Notes:        make([]mimNoteResp, 0, len(rep.Notes)),
	}
	for _, n := range rep.Notes {
		out.Notes = append(out.Notes, mimNoteResp{
			NodeID: n.NodeID, Activity: n.Activity, Kind: n.Kind,
			Status: string(n.Status), Detail: n.Detail,
		})
	}
	return out
}
