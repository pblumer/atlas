package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/connector/script"

	"github.com/pblumer/atlas/api/httpapi"
)

// maxScriptBytes caps a script-run request body (source + sample variables).
const maxScriptBytes = 256 << 10 // 256 KiB

type runScriptReq struct {
	Language  string         `json:"language"`
	Source    string         `json:"source"`
	Variables map[string]any `json:"variables"`
}

type runScriptResp struct {
	OK     bool   `json:"ok"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// handleRunScript runs a polyglot script task (PowerShell/Python/JavaScript)
// against caller-supplied sample variables and returns its result, so the Modeler
// can offer a "Run" button to try a script before deploying — the debugging loop
// that avoids a publish-per-edit cycle.
//
// It runs through the *same* interpreter the deployed worker uses (same binary and
// timeout), and only for a language whose worker is enabled on this server: an
// operator who turned a language off with its flag has also turned off testing it.
// Because it executes arbitrary interpreter code on demand, it is admin-only when
// auth is enforced (like the deployed workers, this is an opt-out-guarded trust
// surface, ADR-0047). A run error (non-zero exit, timeout, bad output) is reported
// faithfully as ok:false with the message, not an HTTP error.
func (s *Server) handleRunScript(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, maxScriptBytes))
	dec.UseNumber() // keep numbers exact for the interpreter
	var req runScriptReq
	if err := dec.Decode(&req); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	lang, ok := script.LangByName(strings.ToLower(strings.TrimSpace(req.Language)))
	if !ok {
		httpapi.JSON(w, http.StatusOK, runScriptResp{Error: "unsupported script language " + strconv.Quote(req.Language)})
		return
	}
	exec := s.scriptWorkers[lang.JobType]
	if exec == nil {
		httpapi.JSON(w, http.StatusOK, runScriptResp{Error: lang.Name + " is not enabled on this server (start it with --" + lang.Name + ", and install its interpreter)"})
		return
	}
	if strings.TrimSpace(req.Source) == "" {
		httpapi.JSON(w, http.StatusOK, runScriptResp{Error: "empty script"})
		return
	}
	result, err := exec.Run(r.Context(), req.Source, req.Variables)
	if err != nil {
		httpapi.JSON(w, http.StatusOK, runScriptResp{Error: err.Error()})
		return
	}
	httpapi.JSON(w, http.StatusOK, runScriptResp{OK: true, Result: result})
}
