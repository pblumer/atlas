package api

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/pblumer/atlas/api/httpapi"

	"github.com/pblumer/atlas/api/token"
)

// maxPublicStartBytes caps a public start submission. A start form's data is
// small; this refuses an oversized anonymous payload (ADR-0029).
const maxPublicStartBytes = 256 << 10 // 256 KiB

// latestDeploymentByProcessID returns the current (highest-version) deployment
// for a process id, or nil if none is deployed. Must be called on the run-loop
// goroutine (it reads the deployment registry).
func (s *Server) latestDeploymentByProcessID(pid string) *deployment {
	want, ok := s.versions[pid]
	if !ok {
		return nil
	}
	for _, key := range s.order {
		if d := s.deployments[key]; d.ProcessID == pid && d.Version == want {
			return d
		}
	}
	return nil
}

type publicLinkResp struct {
	Token     string `json:"token"`
	ProcessID string `json:"processId"`
	FormID    string `json:"formId"`
	CreatedAt int64  `json:"createdAt"`
	URL       string `json:"url"`
}

func toPublicLinkResp(l publicLink) publicLinkResp {
	return publicLinkResp{
		Token: l.Token, ProcessID: l.ProcessID, FormID: l.FormID,
		CreatedAt: l.CreatedAt, URL: "/public/forms/" + l.Token,
	}
}

// handleCreatePublicLink mints (or returns the existing) public start link for a
// deployed process that has a start form (ADR-0029). Body: {"processId": "..."}.
// Publishing is idempotent per process id — a second call returns the same token
// rather than piling up links. 400 if the process has no start form, 404 if it is
// not deployed. Trusted route (gated with the rest of /api/v1 when auth is on).
func (s *Server) handleCreatePublicLink(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<10))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload struct {
		ProcessID string `json:"processId"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.ProcessID == "" {
		httpapi.Error(w, http.StatusBadRequest, "processId is required")
		return
	}
	var (
		out         publicLink
		notDeployed bool
		notExec     bool
		noForm      bool
		opErr       error
	)
	s.do(func() {
		d := s.latestDeploymentByProcessID(payload.ProcessID)
		if d == nil {
			notDeployed = true
			return
		}
		// A non-executable process must not be published for public starting.
		if d.cp != nil && !d.cp.IsExecutable() {
			notExec = true
			return
		}
		formID := d.cp.StartFormId()
		if formID == "" {
			noForm = true
			return
		}
		// Idempotent: reuse an existing link for this process id.
		links, e := s.publicLinks.LoadAll()
		if e != nil {
			opErr = e
			return
		}
		for _, l := range links {
			if l.ProcessID == payload.ProcessID {
				out = l
				return
			}
		}
		token, e := token.New()
		if e != nil {
			opErr = e
			return
		}
		out = publicLink{Token: token, ProcessID: payload.ProcessID, FormID: formID, CreatedAt: time.Now().Unix()}
		opErr = s.publicLinks.Save(out)
	})
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "create public link: "+opErr.Error())
	case notDeployed:
		httpapi.Error(w, http.StatusNotFound, "no deployed process with that id")
	case notExec:
		httpapi.Error(w, http.StatusConflict, "process is not executable and cannot be published")
	case noForm:
		httpapi.Error(w, http.StatusBadRequest, "the process has no start form to publish")
	default:
		httpapi.JSON(w, http.StatusOK, toPublicLinkResp(out))
	}
}

// handleListPublicLinks lists all public start links (newest first). Trusted.
func (s *Server) handleListPublicLinks(w http.ResponseWriter, _ *http.Request) {
	out := []publicLinkResp{}
	var loadErr error
	s.do(func() {
		links, e := s.publicLinks.LoadAll()
		loadErr = e
		for _, l := range links {
			out = append(out, toPublicLinkResp(l))
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list public links: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// handleRevokePublicLink revokes a link by token, killing the public URL. Trusted.
func (s *Server) handleRevokePublicLink(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	var delErr error
	s.do(func() { delErr = s.publicLinks.Delete(token) })
	if delErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "revoke public link: "+delErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{"token": token})
}

// --- public, unauthenticated endpoints (ADR-0029) ---
//
// These live under /public/, outside the /api/v1 surface auth gates, so an
// anonymous visitor can reach exactly one thing: the start form for one token.

// handlePublicFormSchema returns the form schema (and the process name) a public
// link renders. It is the one read a public visitor may perform, scoped to the
// token's form. 404 if the token is unknown or its form/process is gone.
func (s *Server) handlePublicFormSchema(w http.ResponseWriter, r *http.Request) {
	if !s.publicRate.allow(httpapi.ClientIP(r)) {
		httpapi.Error(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	token := r.PathValue("token")
	var (
		procName string
		schema   string
		found    bool
		opErr    error
	)
	s.do(func() {
		link, ok, e := s.publicLinks.Get(token)
		if e != nil || !ok {
			opErr = e
			return
		}
		f, ok, e := s.forms.Get(link.FormID)
		if e != nil || !ok {
			opErr = e
			return
		}
		found = true
		schema = f.Schema
		if d := s.latestDeploymentByProcessID(link.ProcessID); d != nil {
			procName = d.Name
			if procName == "" {
				procName = d.ProcessID
			}
		}
	})
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read form: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "unknown or revoked link")
	default:
		httpapi.JSON(w, http.StatusOK, map[string]any{
			"processName": procName,
			"schema":      json.RawMessage(schema),
		})
	}
}

// handlePublicFormStart starts an instance of the token's process, seeded with
// the submitted form data as start variables, through the same single-writer path
// every other start uses (ADR-0029/ADR-0002). It reads nothing back and touches
// no other process. Rate-limited and payload-capped. 404 if the token is unknown
// or its process is no longer deployed.
func (s *Server) handlePublicFormStart(w http.ResponseWriter, r *http.Request) {
	if !s.publicRate.allow(httpapi.ClientIP(r)) {
		httpapi.Error(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	token := r.PathValue("token")
	body, err := io.ReadAll(io.LimitReader(r.Body, maxPublicStartBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	vars, err := parseStartVariables(body)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	var (
		found   bool
		notExec bool
		runErr  error
	)
	s.do(func() {
		link, ok, e := s.publicLinks.Get(token)
		if e != nil || !ok {
			runErr = e
			return
		}
		d := s.latestDeploymentByProcessID(link.ProcessID)
		if d == nil {
			return // token valid but process undeployed → treated as not found
		}
		// The current version may have become non-executable since the link was
		// minted (a redeploy); refuse to start it.
		if d.cp != nil && !d.cp.IsExecutable() {
			notExec = true
			return
		}
		found = true
		s.proc.CreateInstance(d.Key, vars...)
		runErr = s.jobRunner.Drive()
	})
	switch {
	case runErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "start: "+runErr.Error())
	case notExec:
		httpapi.Error(w, http.StatusConflict, "process is not executable and cannot be started")
	case !found:
		httpapi.Error(w, http.StatusNotFound, "unknown or revoked link")
	default:
		httpapi.JSON(w, http.StatusOK, map[string]any{"started": true})
	}
}

// handlePublicFormPage serves the standalone public form page (no app shell) for
// a link. The page's script reads the token from its own URL and calls the two
// public JSON endpoints above. The token is validated so a bogus URL 404s rather
// than serving a shell that then fails.
func (s *Server) handlePublicFormPage(w http.ResponseWriter, r *http.Request) {
	if !s.publicRate.allow(httpapi.ClientIP(r)) {
		httpapi.Error(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	page, err := webFS.ReadFile("web/public-form.html")
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "public page missing")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page)
}
