package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/compiler"
)

// Bundle import (ADR-0129): the receiving half of publishing an application to
// another Atlas server. One request carries a whole release — the manifest plus
// every artifact — and this server validates and deploys it all-or-nothing, then
// records the release under the version the *publisher* minted.
//
// Why one request rather than driving the granular API: a mid-sequence failure
// across several calls would leave a half-deployed application, which is the exact
// failure ADR-0034's "validate all, then deploy all" exists to prevent. One request
// means one atomic outcome to report.
//
// The DMN models travel already resolved, from the publisher's release snapshot,
// so this server never has to reach the same temis instance — a release is meant to
// be the frozen thing that shipped.

// maxImportBytes caps a bundle. It is larger than the single-model limit because a
// bundle carries several BPMN definitions plus their resolved DMN models, but it is
// still bounded: the endpoint accepts a multi-megabyte body from an authenticated
// peer, so an unbounded read would be a memory-exhaustion lever for a leaked token.
const maxImportBytes = 32 << 20 // 32 MiB

// importBundleReq is the wire format a publishing peer sends.
type importBundleReq struct {
	// Application names the application to import into. It is matched by name and
	// created when absent, because the publisher's application id is meaningless
	// here — the two servers keep their own ids and the publisher remembers the
	// mapping (ADR-0129's per-target binding record).
	Application string `json:"application"`
	Release     struct {
		Version int32  `json:"version"`
		Note    string `json:"note"`
	} `json:"release"`
	Artifacts []importArtifact `json:"artifacts"`
}

// importArtifact is one artifact as it shipped. Kind is "process" today; the
// release manifest only carries BPMN definitions so far (ADR-0128 Phase 2).
type importArtifact struct {
	Kind      string   `json:"kind"`
	ProcessID string   `json:"processId"`
	XML       string   `json:"xml"`
	DMNXMLs   []string `json:"dmnXmls,omitempty"`
}

// importBundleResp reports what landed: this server's application id (which the
// publisher records for later status reads) and the release it accepted.
type importBundleResp struct {
	ApplicationID string              `json:"applicationId"`
	Application   string              `json:"application"`
	Imported      bool                `json:"imported"`
	Reason        string              `json:"reason,omitempty"`
	Definitions   []deployedProcess   `json:"definitions"`
	Release       *applicationRelease `json:"release,omitempty"`
}

// handleImportBundle receives a published application bundle from a peer. It is the
// only operation a deploy token may reach (deployAgentAllowed, ADR-0129).
func (s *Server) handleImportBundle(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxImportBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var req importBundleReq
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	name := strings.TrimSpace(req.Application)
	if name == "" {
		writeError(w, http.StatusBadRequest, "application name is required")
		return
	}
	if req.Release.Version <= 0 {
		writeError(w, http.StatusBadRequest, "release version must be positive")
		return
	}
	if len(req.Artifacts) == 0 {
		writeError(w, http.StatusBadRequest, "bundle carries no artifacts")
		return
	}
	for i, a := range req.Artifacts {
		if a.Kind != "" && a.Kind != "process" {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("artifact %d: unsupported kind %q", i, a.Kind))
			return
		}
		if strings.TrimSpace(a.XML) == "" {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("artifact %d: empty xml", i))
			return
		}
	}

	// Phase 1 (off-loop): compile every artifact before anything is registered. A
	// bundle that does not compile is refused whole, exactly as a local publish is.
	for _, a := range req.Artifacts {
		if _, err := compiler.ParseAll(1, 1, bytes.NewReader([]byte(a.XML))); err != nil {
			writeJSON(w, http.StatusConflict, importBundleResp{
				Application: name, Imported: false,
				Reason:      fmt.Sprintf("artifact %q does not compile: %s", a.ProcessID, err.Error()),
				Definitions: []deployedProcess{},
			})
			return
		}
	}

	var (
		appID       string
		already     bool
		deployed    []deployedProcess
		rel         applicationRelease
		opErr       error
		conflictMsg string
	)
	s.do(func() {
		// Resolve or create the application. Matching by name is what makes the
		// publisher's first import work without either side knowing the other's ids.
		projs, err := s.projects.loadAll()
		if err != nil {
			opErr = err
			return
		}
		for _, p := range projs {
			if p.Name == name {
				appID = p.ID
				break
			}
		}
		if appID == "" {
			id, err := newID()
			if err != nil {
				opErr = err
				return
			}
			now := time.Now().Unix()
			rec := project{ID: id, Name: name, Visibility: VisibilityPrivate, CreatedAt: now, UpdatedAt: now}
			if err := s.projects.save(rec); err != nil {
				opErr = err
				return
			}
			appID = id
		}

		// A version this server already holds is refused rather than overwritten: the
		// publisher mints versions, so the same number arriving twice means either a
		// retry or two publishers disagreeing, and silently replacing what is
		// deployed would hide both (ADR-0129).
		existing, err := s.releases.forApplication(appID)
		if err != nil {
			opErr = err
			return
		}
		for _, e := range existing {
			if e.Version == req.Release.Version {
				already = true
				conflictMsg = fmt.Sprintf("release v%d is already imported for %q", req.Release.Version, name)
				return
			}
		}

		// Phase 2 (on-loop): deploy every artifact, then record the release.
		deployedAt := time.Now().Unix()
		for _, a := range req.Artifacts {
			dmnXMLs := make([][]byte, 0, len(a.DMNXMLs))
			for _, x := range a.DMNXMLs {
				dmnXMLs = append(dmnXMLs, []byte(x))
			}
			dps, _, pErr := s.deployModel([]byte(a.XML), dmnXMLs, deployedAt, appID)
			if pErr != nil {
				opErr = pErr
				return
			}
			deployed = append(deployed, dps...)
		}

		relID, err := newID()
		if err != nil {
			opErr = err
			return
		}
		members := make([]releaseMember, 0, len(deployed))
		for _, d := range deployed {
			members = append(members, releaseMember{
				Kind: "process", Ref: d.ProcessID, ArtifactVer: d.Version, Key: d.Key,
			})
		}
		rel = applicationRelease{
			ID:            relID,
			ApplicationID: appID,
			Version:       req.Release.Version, // the publisher's number travels with the bundle
			PublishedAt:   deployedAt,
			Note:          strings.TrimSpace(req.Release.Note),
			Members:       members,
		}
		if p := principalFrom(r.Context()); p != nil {
			rel.PublishedBy = p.UserID
		}
		if err := s.releases.save(rel); err != nil {
			opErr = err
			return
		}
		if rel.Version > s.appVersions[appID] {
			s.appVersions[appID] = rel.Version
		}
	})

	switch {
	case opErr != nil:
		writeError(w, http.StatusInternalServerError, "import bundle: "+opErr.Error())
	case already:
		writeJSON(w, http.StatusConflict, importBundleResp{
			ApplicationID: appID, Application: name, Imported: false,
			Reason: conflictMsg, Definitions: []deployedProcess{},
		})
	default:
		if deployed == nil {
			deployed = []deployedProcess{}
		}
		writeJSON(w, http.StatusOK, importBundleResp{
			ApplicationID: appID, Application: name, Imported: true,
			Definitions: deployed, Release: &rel,
		})
	}
}
