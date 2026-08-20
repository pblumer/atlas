package api

import (
	"bytes"
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

// mimReportResp is the JSON form of a mimimport.Report: the per-status counts
// plus the ordered notes.
type mimReportResp struct {
	Native       int           `json:"native"`
	Preserved    int           `json:"preserved"`
	ManualReview int           `json:"manualReview"`
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
// deliberately, exactly like the "Import file…" path (handleSaveDraft). An
// optional ?name= overrides the process name and ?projectId= files the draft
// under a project (same validation as a normal draft save).
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
	rec := draft{ProcessID: pid, Name: name, ProjectID: projectID, SavedAt: time.Now().Unix(), XML: string(res.BPMN)}
	var (
		saveErr, projErr error
		unknownProject   bool
		protectedProject bool
	)
	s.do(func() {
		if projectID != "" {
			proj, ok, e := s.projects.Get(projectID)
			if e != nil {
				projErr = e
				return
			}
			if !ok {
				unknownProject = true
				return
			}
			// A protected system project's content is platform-managed (ADR-0122).
			if proj.Protected {
				protectedProject = true
				return
			}
		}
		saveErr = s.drafts.Save(rec)
	})
	switch {
	case projErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read project: "+projErr.Error())
	case unknownProject:
		httpapi.Error(w, http.StatusBadRequest, "unknown project id")
	case protectedProject:
		httpapi.Error(w, http.StatusForbidden, "protected system project cannot be modified")
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
