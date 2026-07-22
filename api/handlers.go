package api

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

// maxXMLBytes caps a deployment body. BPMN models are small; this is a sanity
// bound, not a tuning knob.
const maxXMLBytes = 4 << 20 // 4 MiB

// deployedProcess is one process registered by a deployment. A collaboration
// deploys several (one per executable pool); a plain model deploys one.
type deployedProcess struct {
	Key       uint64 `json:"key"`
	ProcessID string `json:"processId"`
	Name      string `json:"name"`
	Version   int32  `json:"version"`
}

// deployResp echoes the first deployed process flat (single-process clients read
// key/processId/version) and lists every process the model deployed, so a
// collaboration surfaces all its pools.
type deployResp struct {
	Key         uint64            `json:"key"`
	ProcessID   string            `json:"processId"`
	Version     int32             `json:"version"`
	Deployments []deployedProcess `json:"deployments"`
}

type processResp struct {
	Key        uint64 `json:"key"`
	ProcessID  string `json:"processId"`
	Name       string `json:"name"`
	Version    int32  `json:"version"`
	DeployedAt int64  `json:"deployedAt"`
}

// processName extracts the first <process name="…"> from BPMN XML, for display.
func processName(body []byte) string {
	_, name := processIdentity(body)
	return name
}

// processIdentity extracts the first process element's id and name from BPMN XML.
// encoding/xml matches on local name, so it works whether or not the element
// carries a namespace prefix (<process> or <bpmn:process>).
func processIdentity(body []byte) (id, name string) {
	var d struct {
		Processes []struct {
			ID   string `xml:"id,attr"`
			Name string `xml:"name,attr"`
		} `xml:"process"`
	}
	if err := xml.Unmarshal(body, &d); err != nil || len(d.Processes) == 0 {
		return "", ""
	}
	return d.Processes[0].ID, d.Processes[0].Name
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
	Tokens    int              `json:"tokens"`
	Elements  []runtimeElement `json:"elements"`
}

type instanceResp struct {
	Key              uint64         `json:"key"`
	ProcessDefKey    uint64         `json:"processDefKey"`
	ProcessID        string         `json:"processId"`
	Version          int32          `json:"version"`
	ElementInstances int            `json:"elementInstances"`
	State            string         `json:"state"`
	CompletedAt      int64          `json:"completedAt,omitempty"`
	Variables        []variableView `json:"variables"`
}

type statsResp struct {
	ActiveProcessInstances int `json:"activeProcessInstances"`
	ActiveElementInstances int `json:"activeElementInstances"`
}

type createInstanceResp struct {
	DefinitionKey uint64    `json:"definitionKey"`
	Stats         statsResp `json:"stats"`
}

type cancelInstanceResp struct {
	InstanceKey uint64    `json:"instanceKey"`
	State       string    `json:"state"`
	Stats       statsResp `json:"stats"`
}

// handleInfo reports product/version metadata for the UI shell.
func (s *Server) handleInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, infoResp{Product: "Atlas", Version: Version})
}

// handleDeploy parses a BPMN XML body, compiles and deploys every executable
// process it contains — one for a plain model, several for a collaboration (one
// per pool) — and returns the assigned key/id/version for each. Each pool's
// process becomes its own runnable definition; the message flows between pools
// are the diagram's counterpart of the message events that link them at runtime
// (ADR-0022).
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
		resp       deployResp
		compErr    error
		persistErr error
	)
	s.do(func() {
		deployables, err := compiler.ParseAll(s.nextKey, 1, bytes.NewReader(body))
		if err != nil {
			compErr = err
			return
		}
		deployedAt := time.Now().Unix()
		deployed := make([]deployedProcess, 0, len(deployables))
		for i := range deployables {
			cp := deployables[i].Process
			pid := cp.Intern(cp.BpmnProcessId)
			version := s.versions[pid] + 1
			cp.Version = version
			key := cp.Key // ParseAll assigned s.nextKey+i in document order
			// A pool's name labels its process; fall back to the process's own name.
			name := deployables[i].PoolName
			if name == "" {
				name = deployables[i].ProcessName
			}

			// Durable before visible (I2, ADR-0019): persist before registering. A
			// mid-collaboration failure leaves earlier pools deployed (no rollback
			// yet) and returns 500 — an honest limitation until deployment is a
			// first-class WAL event (ADR-0022).
			if err := s.deploys.save(persistedDeployment{
				Key:        key,
				ProcessID:  pid,
				Name:       name,
				Version:    version,
				DeployedAt: deployedAt,
				XML:        string(body),
			}); err != nil {
				persistErr = err
				return
			}

			s.versions[pid] = version
			s.proc.Deploy(cp)
			s.deployments[key] = &deployment{
				Key:        key,
				ProcessID:  pid,
				Name:       name,
				Version:    version,
				DeployedAt: deployedAt,
				xml:        body,
				cp:         cp,
			}
			s.order = append(s.order, key)
			if key >= s.nextKey {
				s.nextKey = key + 1
			}
			deployed = append(deployed, deployedProcess{Key: key, ProcessID: pid, Name: name, Version: version})
		}
		resp = deployResp{
			Key:         deployed[0].Key,
			ProcessID:   deployed[0].ProcessID,
			Version:     deployed[0].Version,
			Deployments: deployed,
		}
	})
	switch {
	case compErr != nil:
		// A compile failure is a client error: the submitted model is invalid.
		writeError(w, http.StatusBadRequest, compErr.Error())
	case persistErr != nil:
		writeError(w, http.StatusInternalServerError, "persist deployment: "+persistErr.Error())
	default:
		writeJSON(w, http.StatusOK, resp)
	}
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
				Name:       d.Name,
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

// handleDeleteProcess removes a deployed definition (one version). It refuses if
// the definition still has running instances, since a live instance resolves its
// definition by key on every batch.
func (s *Server) handleDeleteProcess(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid definition key")
		return
	}
	var (
		found      bool
		running    int
		scanErr    error
		persistErr error
	)
	s.do(func() {
		if _, ok := s.deployments[key]; !ok {
			return
		}
		found = true
		scanErr = s.store.ActiveProcessInstances(func(_ uint64, v *model.ProcessInstanceValue) error {
			if v.ProcessDefKey == key {
				running++
			}
			return nil
		})
		if scanErr != nil || running > 0 {
			return
		}
		// Durable before visible (I2, ADR-0019): remove the on-disk record first,
		// so a deletion that is acknowledged never reappears on restart.
		if err := s.deploys.delete(key); err != nil {
			persistErr = err
			return
		}
		s.proc.Undeploy(key)
		delete(s.deployments, key)
		for i, k := range s.order {
			if k == key {
				s.order = append(s.order[:i], s.order[i+1:]...)
				break
			}
		}
	})
	switch {
	case !found:
		writeError(w, http.StatusNotFound, "no deployment with that key")
	case scanErr != nil:
		writeError(w, http.StatusInternalServerError, "check instances: "+scanErr.Error())
	case running > 0:
		writeError(w, http.StatusConflict, fmt.Sprintf("cannot delete: %d running instance(s); cancel them first", running))
	case persistErr != nil:
		writeError(w, http.StatusInternalServerError, "remove deployment: "+persistErr.Error())
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleProcessRuntime returns, for one definition, how many instances are live
// and how many tokens (element instances) currently sit on each BPMN element —
// the data the browser overlays onto the diagram. An optional ?instance=<key>
// filter narrows the result to a single process instance, so the live view can
// isolate one instance on the diagram instead of aggregating all of them.
func (s *Server) handleProcessRuntime(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid definition key")
		return
	}
	// instanceFilter == 0 means "all instances"; instance keys are never 0.
	var instanceFilter uint64
	if q := r.URL.Query().Get("instance"); q != "" {
		instanceFilter, err = strconv.ParseUint(q, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid instance key")
			return
		}
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
			if instanceFilter != 0 && v.ProcessInstanceKey != instanceFilter {
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
			resp.Tokens++
			return nil
		})
		if scanErr != nil {
			return
		}
		for _, bid := range order {
			resp.Elements = append(resp.Elements, *byElement[bid])
		}
		scanErr = s.store.ActiveProcessInstances(func(piKey uint64, v *model.ProcessInstanceValue) error {
			if v.ProcessDefKey != key {
				return nil
			}
			if instanceFilter != 0 && piKey != instanceFilter {
				return nil
			}
			resp.Instances++
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

// handleCreateInstance starts one instance of a deployed definition, optionally
// seeded with variables from the request body ({"variables": {"amount": 100}}),
// runs the processor until idle, and returns the resulting live counts.
func (s *Server) handleCreateInstance(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid definition key")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	startVars, err := parseStartVariables(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
		s.proc.CreateInstance(key, startVars...)
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

// parseStartVariables reads {"variables": {name: scalar}} from a request body
// into VariableValues. Only scalar JSON (number, string, boolean, null) is
// supported; numbers keep their exact textual form for FEEL's decimal semantics.
func parseStartVariables(body []byte) ([]model.VariableValue, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}
	var payload struct {
		Variables map[string]any `json:"variables"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %v", err)
	}
	if len(payload.Variables) == 0 {
		return nil, nil
	}
	out := make([]model.VariableValue, 0, len(payload.Variables))
	for name, raw := range payload.Variables {
		vv := model.VariableValue{Name: name}
		switch x := raw.(type) {
		case nil:
			vv.Kind = model.VarNull
		case bool:
			vv.Kind, vv.Bool = model.VarBool, x
		case json.Number:
			vv.Kind, vv.Text = model.VarNumber, x.String()
		case string:
			vv.Kind, vv.Text = model.VarString, x
		default:
			return nil, fmt.Errorf("variable %q: only scalar values (number, string, boolean, null) are supported yet", name)
		}
		out = append(out, vv)
	}
	return out, nil
}

// variableView renders a variable for the operator UI.
type variableView struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Kind  string `json:"kind"`
}

func toVariableView(v *model.VariableValue) variableView {
	out := variableView{Name: v.Name}
	switch v.Kind {
	case model.VarBool:
		out.Kind = "boolean"
		if v.Bool {
			out.Value = "true"
		} else {
			out.Value = "false"
		}
	case model.VarNumber:
		out.Kind, out.Value = "number", v.Text
	case model.VarString:
		out.Kind, out.Value = "string", v.Text
	default:
		out.Kind, out.Value = "null", "null"
	}
	return out
}

// handleListInstances lists process instances — live ones (with their current
// token count) followed by finished ones from the history index, most recently
// completed first (ADR-0017). It is the operator "instances" view.
func (s *Server) handleListInstances(w http.ResponseWriter, _ *http.Request) {
	active := []instanceResp{}
	done := []instanceResp{}
	var scanErr error
	s.do(func() {
		// Attach the definition's id/version and the scope's variables to a row.
		enrich := func(r *instanceResp, key uint64) error {
			if d, ok := s.deployments[r.ProcessDefKey]; ok {
				r.ProcessID = d.ProcessID
				r.Version = d.Version
			}
			return s.store.VariablesOfScope(key, func(vv *model.VariableValue) error {
				r.Variables = append(r.Variables, toVariableView(vv))
				return nil
			})
		}

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
				Variables:        []variableView{},
			}
			if err := enrich(&r, key); err != nil {
				return err
			}
			active = append(active, r)
			return nil
		})
		if scanErr != nil {
			return
		}

		scanErr = s.store.CompletedProcessInstances(func(key uint64, v *model.ProcessInstanceValue) error {
			r := instanceResp{
				Key:           key,
				ProcessDefKey: v.ProcessDefKey,
				State:         v.State.String(),
				CompletedAt:   v.CompletedAt,
				Variables:     []variableView{},
			}
			if err := enrich(&r, key); err != nil {
				return err
			}
			done = append(done, r)
			return nil
		})
	})
	if scanErr != nil {
		writeError(w, http.StatusInternalServerError, "list instances: "+scanErr.Error())
		return
	}
	// Finished instances: most recently completed first.
	sort.Slice(done, func(i, j int) bool { return done[i].CompletedAt > done[j].CompletedAt })
	writeJSON(w, http.StatusOK, append(active, done...))
}

type publishMessageResp struct {
	Name           string    `json:"name"`
	CorrelationKey string    `json:"correlationKey"`
	Stats          statsResp `json:"stats"`
}

// handlePublishMessage publishes a message by name and correlation key, with
// optional payload variables, then runs the processor to idle so any waiting
// instance advances. It correlates against open subscriptions through the engine;
// a message that matches nothing is accepted as a no-op (no buffering yet,
// ADR-0020). Body: {"name","correlationKey","variables":{…}}.
func (s *Server) handlePublishMessage(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload struct {
		Name           string `json:"name"`
		CorrelationKey string `json:"correlationKey"`
	}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
	}
	if payload.Name == "" {
		writeError(w, http.StatusBadRequest, "message name is required")
		return
	}
	vars, err := parseStartVariables(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var (
		runErr  error
		statErr error
		stats   statsResp
	)
	s.do(func() {
		s.proc.PublishMessage(payload.Name, payload.CorrelationKey, vars...)
		if err := s.proc.RunUntilIdle(); err != nil {
			runErr = err
			return
		}
		stats, statErr = s.readStats()
	})
	switch {
	case runErr != nil:
		writeError(w, http.StatusInternalServerError, "publish message: "+runErr.Error())
	case statErr != nil:
		writeError(w, http.StatusInternalServerError, "read stats: "+statErr.Error())
	default:
		writeJSON(w, http.StatusOK, publishMessageResp{Name: payload.Name, CorrelationKey: payload.CorrelationKey, Stats: stats})
	}
}

// handleCancelInstance terminates a running process instance: it terminates
// every active element instance and records the instance as terminated in
// history, so it disappears from the running list and the live overlay and shows
// as "terminated" in the finished list. Useful for a stuck instance (e.g. one
// parked on a wait that will never complete). 404 if no active instance has the
// key. Returns the resulting live counts.
func (s *Server) handleCancelInstance(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid instance key")
		return
	}
	var (
		found   bool
		scanErr error
		runErr  error
		statErr error
		stats   statsResp
	)
	s.do(func() {
		scanErr = s.store.ActiveProcessInstances(func(k uint64, _ *model.ProcessInstanceValue) error {
			if k == key {
				found = true
			}
			return nil
		})
		if scanErr != nil || !found {
			return
		}
		s.proc.CancelInstance(key)
		if err := s.proc.RunUntilIdle(); err != nil {
			runErr = err
			return
		}
		stats, statErr = s.readStats()
	})
	switch {
	case scanErr != nil:
		writeError(w, http.StatusInternalServerError, "find instance: "+scanErr.Error())
	case !found:
		writeError(w, http.StatusNotFound, "no active instance with that key")
	case runErr != nil:
		writeError(w, http.StatusInternalServerError, "cancel instance: "+runErr.Error())
	case statErr != nil:
		writeError(w, http.StatusInternalServerError, "read stats: "+statErr.Error())
	default:
		writeJSON(w, http.StatusOK, cancelInstanceResp{InstanceKey: key, State: "terminated", Stats: stats})
	}
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

type draftResp struct {
	ProcessID string `json:"processId"`
	Name      string `json:"name"`
	SavedAt   int64  `json:"savedAt"`
}

// handleSaveDraft persists a diagram as a draft: the raw BPMN XML is stored as-is,
// keyed by its process id, WITHOUT compiling it — so an incomplete or not-yet
// executable model can still be saved and reopened. Re-saving the same process id
// overwrites the previous draft rather than creating a version.
func (s *Server) handleSaveDraft(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if len(body) == 0 {
		writeError(w, http.StatusBadRequest, "empty request body: expected BPMN XML")
		return
	}
	pid, name := processIdentity(body)
	if pid == "" {
		writeError(w, http.StatusBadRequest, "cannot save draft: no <process id> in the diagram")
		return
	}
	rec := draft{ProcessID: pid, Name: name, SavedAt: time.Now().Unix(), XML: string(body)}
	var saveErr error
	s.do(func() { saveErr = s.drafts.save(rec) })
	if saveErr != nil {
		writeError(w, http.StatusInternalServerError, "save draft: "+saveErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, draftResp{ProcessID: pid, Name: name, SavedAt: rec.SavedAt})
}

// handleListDrafts lists saved drafts, most recently saved first.
func (s *Server) handleListDrafts(w http.ResponseWriter, _ *http.Request) {
	list := []draftResp{}
	var loadErr error
	s.do(func() {
		var recs []draft
		recs, loadErr = s.drafts.loadAll()
		for _, d := range recs {
			list = append(list, draftResp{ProcessID: d.ProcessID, Name: d.Name, SavedAt: d.SavedAt})
		}
	})
	if loadErr != nil {
		writeError(w, http.StatusInternalServerError, "list drafts: "+loadErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleDraftXML returns a draft's raw BPMN XML so the editor can reopen it.
func (s *Server) handleDraftXML(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var (
		rec     draft
		ok      bool
		readErr error
	)
	s.do(func() { rec, ok, readErr = s.drafts.get(id) })
	switch {
	case readErr != nil:
		writeError(w, http.StatusInternalServerError, "read draft: "+readErr.Error())
	case !ok:
		writeError(w, http.StatusNotFound, "no draft with that process id")
	default:
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		_, _ = w.Write([]byte(rec.XML))
	}
}

// handleDeleteDraft removes a saved draft. Deleting an absent draft succeeds, so
// the operation is idempotent.
func (s *Server) handleDeleteDraft(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var delErr error
	s.do(func() { delErr = s.drafts.delete(id) })
	if delErr != nil {
		writeError(w, http.StatusInternalServerError, "delete draft: "+delErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
