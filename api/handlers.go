package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

// maxXMLBytes caps a deployment body. BPMN models are small; this is a sanity
// bound, not a tuning knob.
const maxXMLBytes = 4 << 20 // 4 MiB

type deployResp struct {
	Key       uint64 `json:"key"`
	ProcessID string `json:"processId"`
	Version   int32  `json:"version"`
}

type processResp struct {
	Key        uint64 `json:"key"`
	ProcessID  string `json:"processId"`
	Version    int32  `json:"version"`
	DeployedAt int64  `json:"deployedAt"`
}

type infoResp struct {
	Product string `json:"product"`
	Version string `json:"version"`
}

type runtimeElement struct {
	ElementID string `json:"elementId"`
	Type      string `json:"type"`
	Tokens    int    `json:"tokens"`
}

type runtimeResp struct {
	Instances int              `json:"instances"`
	Elements  []runtimeElement `json:"elements"`
}

type instanceResp struct {
	Key              uint64 `json:"key"`
	ProcessDefKey    uint64 `json:"processDefKey"`
	ProcessID        string `json:"processId"`
	Version          int32  `json:"version"`
	ElementInstances int    `json:"elementInstances"`
	State            string `json:"state"`
}

type statsResp struct {
	ActiveProcessInstances int `json:"activeProcessInstances"`
	ActiveElementInstances int `json:"activeElementInstances"`
}

type createInstanceResp struct {
	DefinitionKey uint64    `json:"definitionKey"`
	Stats         statsResp `json:"stats"`
}

// handleInfo reports product/version metadata for the UI shell.
func (s *Server) handleInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, infoResp{Product: "Atlas", Version: Version})
}

// handleDeploy parses a BPMN XML body, compiles and deploys it, and returns the
// assigned definition key, process id, and version.
func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if len(body) == 0 {
		writeError(w, http.StatusBadRequest, "empty request body: expected BPMN XML")
		return
	}

	var (
		resp    deployResp
		compErr error
	)
	s.do(func() {
		cp, err := compiler.Parse(s.nextKey, 1, bytes.NewReader(body))
		if err != nil {
			compErr = err
			return
		}
		pid := cp.Intern(cp.BpmnProcessId)
		version := s.versions[pid] + 1
		s.versions[pid] = version
		cp.Version = version

		key := s.nextKey
		s.proc.Deploy(cp)
		s.deployments[key] = &deployment{
			Key:        key,
			ProcessID:  pid,
			Version:    version,
			DeployedAt: time.Now().Unix(),
			xml:        body,
			cp:         cp,
		}
		s.order = append(s.order, key)
		s.nextKey++
		resp = deployResp{Key: key, ProcessID: pid, Version: version}
	})
	if compErr != nil {
		// A compile failure is a client error: the submitted model is invalid.
		writeError(w, http.StatusBadRequest, compErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleListProcesses lists deployed definitions in registration order.
func (s *Server) handleListProcesses(w http.ResponseWriter, _ *http.Request) {
	list := []processResp{}
	s.do(func() {
		for _, key := range s.order {
			d := s.deployments[key]
			list = append(list, processResp{
				Key:        d.Key,
				ProcessID:  d.ProcessID,
				Version:    d.Version,
				DeployedAt: d.DeployedAt,
			})
		}
	})
	writeJSON(w, http.StatusOK, list)
}

// handleProcessXML returns a deployed definition's BPMN XML for the browser to
// render. If the model carries no diagram layout, a generated left-to-right
// layout is injected so it still renders (ensureDiagramLayout).
func (s *Server) handleProcessXML(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid definition key")
		return
	}
	var raw []byte
	s.do(func() {
		if d, ok := s.deployments[key]; ok {
			raw = d.xml
		}
	})
	if raw == nil {
		writeError(w, http.StatusNotFound, "no deployment with that key")
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = w.Write(ensureDiagramLayout(raw))
}

// handleProcessRuntime returns, for one definition, how many instances are live
// and how many tokens (element instances) currently sit on each BPMN element —
// the data the browser overlays onto the diagram.
func (s *Server) handleProcessRuntime(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid definition key")
		return
	}
	var (
		found   bool
		scanErr error
		resp    = runtimeResp{Elements: []runtimeElement{}}
	)
	s.do(func() {
		d, ok := s.deployments[key]
		if !ok {
			return
		}
		found = true

		byElement := map[string]*runtimeElement{}
		var order []string
		scanErr = s.store.ActiveElementInstances(func(_ uint64, v *model.ElementInstanceValue) error {
			if v.ProcessDefKey != key {
				return nil
			}
			bid := d.cp.ElementBpmnId(v.ElementId)
			if bid == "" {
				return nil
			}
			e := byElement[bid]
			if e == nil {
				e = &runtimeElement{ElementID: bid, Type: d.cp.Node(v.ElementId).Type.String()}
				byElement[bid] = e
				order = append(order, bid)
			}
			e.Tokens++
			return nil
		})
		if scanErr != nil {
			return
		}
		for _, bid := range order {
			resp.Elements = append(resp.Elements, *byElement[bid])
		}
		scanErr = s.store.ActiveProcessInstances(func(_ uint64, v *model.ProcessInstanceValue) error {
			if v.ProcessDefKey == key {
				resp.Instances++
			}
			return nil
		})
	})
	switch {
	case !found:
		writeError(w, http.StatusNotFound, "no deployment with that key")
	case scanErr != nil:
		writeError(w, http.StatusInternalServerError, "read runtime: "+scanErr.Error())
	default:
		writeJSON(w, http.StatusOK, resp)
	}
}

// handleCreateInstance starts one instance of a deployed definition and runs the
// processor until idle, then returns the resulting live counts.
func (s *Server) handleCreateInstance(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid definition key")
		return
	}
	var (
		found   bool
		runErr  error
		statErr error
		stats   statsResp
	)
	s.do(func() {
		if _, ok := s.deployments[key]; !ok {
			return
		}
		found = true
		s.proc.CreateInstance(key)
		if err := s.proc.RunUntilIdle(); err != nil {
			runErr = err
			return
		}
		stats, statErr = s.readStats()
	})
	switch {
	case !found:
		writeError(w, http.StatusNotFound, "no deployment with that key")
	case runErr != nil:
		writeError(w, http.StatusInternalServerError, "run instance: "+runErr.Error())
	case statErr != nil:
		writeError(w, http.StatusInternalServerError, "read stats: "+statErr.Error())
	default:
		writeJSON(w, http.StatusOK, createInstanceResp{DefinitionKey: key, Stats: stats})
	}
}

// handleListInstances lists live process instances with their definition and
// how many element instances (tokens) each currently holds — the operator
// "running instances" view.
func (s *Server) handleListInstances(w http.ResponseWriter, _ *http.Request) {
	list := []instanceResp{}
	var scanErr error
	s.do(func() {
		scanErr = s.store.ActiveProcessInstances(func(key uint64, v *model.ProcessInstanceValue) error {
			elements := 0
			if err := s.store.ElementInstancesOfProcess(key, func(uint64) error {
				elements++
				return nil
			}); err != nil {
				return err
			}
			r := instanceResp{
				Key:              key,
				ProcessDefKey:    v.ProcessDefKey,
				ElementInstances: elements,
				State:            "active",
			}
			if d, ok := s.deployments[v.ProcessDefKey]; ok {
				r.ProcessID = d.ProcessID
				r.Version = d.Version
			}
			list = append(list, r)
			return nil
		})
	})
	if scanErr != nil {
		writeError(w, http.StatusInternalServerError, "list instances: "+scanErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleStats returns the live instance counts.
func (s *Server) handleStats(w http.ResponseWriter, _ *http.Request) {
	var (
		stats statsResp
		err   error
	)
	s.do(func() { stats, err = s.readStats() })
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read stats: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
