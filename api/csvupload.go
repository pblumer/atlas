package api

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/pblumer/atlas/connector/csvimport"
)

// maxCSVUploadBytes caps a CSV upload. Generous enough for a realistic batch of
// records, small enough to refuse a runaway upload: the file is read whole and
// materialised as one VarJSON `rows` variable, so the ceiling also bounds the
// resulting variable (ADR-0084).
const maxCSVUploadBytes = 16 << 20 // 16 MiB

// csvMultipartMemory is how much of the multipart body ParseMultipartForm keeps
// in memory before spilling parts to temp files; the total is still bounded by
// maxCSVUploadBytes via MaxBytesReader.
const csvMultipartMemory = 4 << 20 // 4 MiB

// csvInstanceResp is the result of starting an instance from a CSV upload: the
// started definition, how many rows were parsed and seeded, the file's name, and
// the resulting live counts.
type csvInstanceResp struct {
	DefinitionKey uint64    `json:"definitionKey"`
	RowCount      int       `json:"rowCount"`
	FileName      string    `json:"fileName,omitempty"`
	Stats         statsResp `json:"stats"`
}

// handleCreateInstanceFromCSV starts one instance of a deployed definition seeded
// from an uploaded CSV (ADR-0084). The request is multipart/form-data with a
// `file` part (the CSV) and a `config` part (a JSON csvimport.Config describing the
// predefined column layout). The CSV is parsed against that layout into a list of
// row objects, seeded as the start variables `rows` (a VarJSON array), `rowCount`
// (a number), and `fileName` (a string), then the instance is created and driven
// through the same single-writer path every other start uses — no engine change.
func (s *Server) handleCreateInstanceFromCSV(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid definition key")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxCSVUploadBytes)
	if err := r.ParseMultipartForm(csvMultipartMemory); err != nil {
		writeError(w, http.StatusBadRequest, "parse upload: "+err.Error())
		return
	}

	cfgRaw := r.FormValue("config")
	if cfgRaw == "" {
		writeError(w, http.StatusBadRequest, "missing config part (a JSON column layout)")
		return
	}
	var cfg csvimport.Config
	if err := json.Unmarshal([]byte(cfgRaw), &cfg); err != nil {
		writeError(w, http.StatusBadRequest, "invalid config JSON: "+err.Error())
		return
	}

	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing file part (the CSV)")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "read file: "+err.Error())
		return
	}

	rows, err := csvimport.ParseRows(cfg, data)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Seed the parsed rows as start variables. rows is an array of row objects
	// (VarJSON); rowCount and fileName give the process — and the audit trail —
	// the batch's size and origin without re-reading the file.
	rowsAny := make([]any, len(rows))
	for i := range rows {
		rowsAny[i] = rows[i]
	}
	fileName := filepath.Base(hdr.Filename)
	startVars, err := startVarsFromMap(map[string]any{
		"rows":     rowsAny,
		"rowCount": json.Number(strconv.Itoa(len(rows))),
		"fileName": fileName,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var (
		found   bool
		notExec bool
		runErr  error
		statErr error
		stats   statsResp
	)
	s.do(func() {
		d, ok := s.deployments[key]
		if !ok {
			return
		}
		found = true
		if d.cp != nil && !d.cp.IsExecutable() {
			notExec = true
			return
		}
		s.proc.CreateInstance(key, startVars...)
		if err := s.jobRunner.Drive(); err != nil {
			runErr = err
			return
		}
		stats, statErr = s.readStats()
	})
	switch {
	case !found:
		writeError(w, http.StatusNotFound, "no deployment with that key")
	case notExec:
		writeError(w, http.StatusConflict, "process is not executable and cannot be started")
	case runErr != nil:
		writeError(w, http.StatusInternalServerError, "run instance: "+runErr.Error())
	case statErr != nil:
		writeError(w, http.StatusInternalServerError, "read stats: "+statErr.Error())
	default:
		writeJSON(w, http.StatusOK, csvInstanceResp{
			DefinitionKey: key, RowCount: len(rows), FileName: fileName, Stats: stats,
		})
	}
}
