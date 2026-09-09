package api

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
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

// mimImportResp is the response of POST /api/v1/imports/mim: the identity of the
// draft that was created plus the conversion report.
type mimImportResp struct {
	ProcessID string        `json:"processId"`
	Name      string        `json:"name"`
	ProjectID string        `json:"projectId,omitempty"`
	SavedAt   int64         `json:"savedAt"`
	Report    mimReportResp `json:"report"`
}

// handleImportMIM converts an uploaded Microsoft Identity Manager (MIM/FIM) XOML
// workflow — or an Export-FIMConfig XML that embeds one — into BPMN via the
// mimimport package and saves it as a draft, returning the draft identity and a
// per-node conversion report.
//
// It never deploys: the model lands as a draft the author reviews and deploys
// deliberately, exactly like the "Import file…" path (handleSaveDraft) — and it now
// files that draft under the same rules, because an import is a draft save with a
// converter in front of it and the two drifting apart is what let this one write
// where a plain save could not.
//
// An optional ?name= overrides the process name. An optional ?projectId= files the
// draft into that application, which needs editor on it like every other write to
// one (ADR-0071); *omitting* it does not mean "file it under none" — a re-import of
// a workflow already held leaves that draft in the application it is in, since only
// an explicit target moves an artifact. An optional ?from= says this import is a new
// draft rather than a deliberate replacement, so the process id it lands on has to
// be free and one another draft already holds is refused with 409 instead of
// silently overwriting it (ADR-0222); omitting it keeps the plain upsert-by-id the
// MCP tools and scripted callers rely on.
func (s *Server) handleImportMIM(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if len(body) == 0 {
		httpapi.Error(w, http.StatusBadRequest, "empty request body: expected MIM/FIM XOML")
		return
	}
	res, err := mimimport.Convert(bytes.NewReader(body), r.URL.Query().Get("name"))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "convert XOML: "+err.Error())
		return
	}
	pid, name := processIdentity(res.BPMN)
	if pid == "" {
		// The converter always emits a <process id>, so an empty id means the input
		// produced no recognisable workflow at all.
		httpapi.Error(w, http.StatusBadRequest, "no workflow found in the uploaded XOML")
		return
	}

	projectID := r.URL.Query().Get("projectId")
	hasProjectParam := r.URL.Query().Has("projectId")
	claiming := r.URL.Query().Has("from") && strings.TrimSpace(r.URL.Query().Get("from")) != pid
	takenMsg := fmt.Sprintf(
		"another draft already uses the process id %q — import it as a replacement, or rename the workflow", pid)

	// existing is the draft this import lands on. It decides three things: whether
	// the id is free, which application the draft keeps when the request names none,
	// and who owns it.
	var (
		existing draft
		existed  bool
		getErr   error
	)
	s.do(func() { existing, existed, getErr = s.drafts.Get(pid) })
	if getErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read draft: "+getErr.Error())
		return
	}
	if claiming && existed {
		httpapi.Error(w, http.StatusConflict, takenMsg)
		return
	}
	// Replacing a draft is a write to whatever governs it today (ADR-0071).
	if existed {
		if code, msg := s.authorizeArtifact(r, existing.ProjectID, existing.OwnerID, ScopeRoleEditor); code != 0 {
			httpapi.Error(w, code, msg)
			return
		}
	}
	destProjectID := existing.ProjectID
	if hasProjectParam {
		destProjectID = projectID
	}
	if destProjectID != "" && (hasProjectParam || !existed) {
		if code, msg := s.authorizeTargetProject(r, destProjectID, ScopeRoleEditor); code != 0 {
			httpapi.Error(w, code, msg)
			return
		}
	}
	// Preserve the original creator on a replacement; stamp the importer on a new
	// draft, so an ungrouped import is that person's personal space (ADR-0071) rather
	// than the ownerless, open artifact an empty creator means.
	ownerID := existing.OwnerID
	if !existed {
		ownerID = s.artifactOwnerOnCreate(r)
	}

	rec := draft{
		ProcessID: pid, Name: name, ProjectID: destProjectID, OwnerID: ownerID,
		SavedAt: time.Now().Unix(), XML: string(res.BPMN),
	}
	var (
		saveErr, projErr error
		protectedProject bool
		taken            bool
	)
	s.do(func() {
		// A protected system project's content is platform-managed (ADR-0122), and
		// effectiveRole grants admins and owners a role on it, so this is the backstop
		// the scope check defers to — at both ends: writing into one, and carrying a
		// draft out of one, which naming no application would otherwise do.
		checked := ""
		for _, id := range [...]string{existing.ProjectID, rec.ProjectID} {
			if id == "" || id == checked {
				continue
			}
			checked = id
			proj, ok, e := s.projects.Get(id)
			if e != nil {
				projErr = e
				return
			}
			if ok && proj.Protected {
				protectedProject = true
				return
			}
		}
		// Re-check the id inside the writer's turn: the read above happened in an
		// earlier turn, so a concurrent save could have claimed it since. Losing that
		// race must refuse, not overwrite.
		if claiming {
			if _, ok, e := s.drafts.Get(pid); e != nil {
				projErr = e
				return
			} else if ok {
				taken = true
				return
			}
		}
		saveErr = s.drafts.Save(rec)
	})
	switch {
	case projErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read project: "+projErr.Error())
	case protectedProject:
		httpapi.Error(w, http.StatusForbidden, "protected system project cannot be modified")
	case taken:
		httpapi.Error(w, http.StatusConflict, takenMsg)
	case saveErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "save draft: "+saveErr.Error())
	default:
		httpapi.JSON(w, http.StatusOK, mimImportResp{
			ProcessID: pid, Name: name, ProjectID: rec.ProjectID, SavedAt: rec.SavedAt,
			Report: toMIMReport(res.Report),
		})
	}
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
