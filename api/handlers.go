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
	"strings"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

// maxXMLBytes caps a deployment body. BPMN models are small; this is a sanity
// bound, not a tuning knob.
const maxXMLBytes = 4 << 20 // 4 MiB

// maxFeelBytes caps a FEEL validation body. Expressions are tiny; this is a
// sanity bound.
const maxFeelBytes = 64 << 10 // 64 KiB

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
	// CollaborationKey groups a collaboration's pools: when non-zero, this process
	// is a pool of a collaboration and the value is the stable key the Operations
	// replay view is opened with (#/operations/c/{collaborationKey}). Zero for a
	// standalone process (ADR-0038).
	CollaborationKey uint64 `json:"collaborationKey,omitempty"`
}

// collaborationParticipants reports how many <participant> pools a model's
// <collaboration> declares. Two or more marks the XML as a collaboration whose
// pools deploy as sibling definitions sharing this XML (ADR-0023).
func collaborationParticipants(body []byte) int {
	var d struct {
		Participants []struct{} `xml:"collaboration>participant"`
	}
	// A deployed body is well-formed XML; on the impossible parse error the zero
	// value (no participants) is the right answer anyway.
	_ = xml.Unmarshal(body, &d)
	return len(d.Participants)
}

// poolSiblings returns the deployments that are pools of the same collaboration
// as d — every deployment sharing d's identical BPMN body — keeping the highest
// version of each pool (so a redeploy of the same collaboration shows its current
// pools), in registration order. For a standalone process it returns just d.
// Must be called on the run-loop goroutine.
func (s *Server) poolSiblings(d *deployment) []*deployment {
	if collaborationParticipants(d.xml) < 2 {
		return []*deployment{d}
	}
	latest := map[string]*deployment{}
	for _, key := range s.order {
		sib := s.deployments[key]
		if !bytes.Equal(sib.xml, d.xml) {
			continue
		}
		if cur, ok := latest[sib.ProcessID]; !ok || sib.Version > cur.Version {
			latest[sib.ProcessID] = sib
		}
	}
	var out []*deployment
	for _, key := range s.order {
		sib := s.deployments[key]
		if latest[sib.ProcessID] == sib {
			out = append(out, sib)
		}
	}
	return out
}

// collaborationKeyOf returns the stable group key for the collaboration d belongs
// to, or 0 when d is a standalone process. poolSiblings lists pools in
// registration order and keys are assigned monotonically, so the first pool
// carries the smallest key — a stable group id. Must be called on the run-loop
// goroutine.
func (s *Server) collaborationKeyOf(d *deployment) uint64 {
	pools := s.poolSiblings(d)
	if len(pools) < 2 {
		return 0
	}
	return pools[0].Key
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
	Tokens    int    `json:"tokens"` // tokens sitting here now (live — drawn green)
	Visits    int    `json:"visits"` // tokens that have ever passed through (history — drawn gray)
}

type runtimeResp struct {
	Instances int              `json:"instances"`
	Tokens    int              `json:"tokens"`
	Elements  []runtimeElement `json:"elements"`
}

// collabPool is one pool (participant) of a collaboration, as a deployed
// definition the collaboration runtime aggregates.
type collabPool struct {
	Key       uint64 `json:"key"`
	ProcessID string `json:"processId"`
	Name      string `json:"name"`
	Version   int32  `json:"version"`
}

// collabFlow is one delivered message flow on the replay timeline: which message
// crossed to which receiving element, when, and between which instances. The
// receiving element is the message-flow edge's target on the shared diagram.
type collabFlow struct {
	At                int64  `json:"at"` // unix nanoseconds
	MessageName       string `json:"messageName"`
	CorrelationKey    string `json:"correlationKey"`
	ReceiverElementID string `json:"receiverElementId"`
	SenderInstance    uint64 `json:"senderInstance,omitempty"`
	ReceiverInstance  uint64 `json:"receiverInstance,omitempty"`
}

// collabRuntimeResp is the whole collaboration's runtime for the replay view:
// its pools, the merged live/visited element overlay across every pool, and the
// time-ordered message flows that crossed between them (ADR-0038).
type collabRuntimeResp struct {
	Pools        []collabPool     `json:"pools"`
	Instances    int              `json:"instances"`
	Tokens       int              `json:"tokens"`
	Elements     []runtimeElement `json:"elements"`
	MessageFlows []collabFlow     `json:"messageFlows"`
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

type validateFeelReq struct {
	Expression string `json:"expression"`
}

type validateFeelResp struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// handleValidateFeel compiles a FEEL expression with the same engine deployment
// uses, so the Modeler can flag syntax/type errors as they're typed instead of
// only at deploy time. Unknown identifiers are allowed (they're process
// variables, discovered via CompileAuto) — only genuine parse/type errors fail.
//
// It is a pure compile: no state is read or written, so it runs off the
// single-writer loop (no s.do) and never touches the processor hot path — a
// read-only edit-time check, consistent with "compile, don't interpret"
// (ADR-0008).
func (s *Server) handleValidateFeel(w http.ResponseWriter, r *http.Request) {
	var req validateFeelReq
	if err := json.NewDecoder(io.LimitReader(r.Body, maxFeelBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	// A blank expression is a no-op success: an empty field simply carries no
	// condition/script, which the editor treats as unset rather than an error.
	if strings.TrimSpace(req.Expression) == "" {
		writeJSON(w, http.StatusOK, validateFeelResp{OK: true})
		return
	}
	if _, err := expr.CompileAuto(req.Expression); err != nil {
		writeJSON(w, http.StatusOK, validateFeelResp{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, validateFeelResp{OK: true})
}

type evalFeelReq struct {
	Expression string         `json:"expression"`
	Variables  map[string]any `json:"variables"`
}

type evalFeelResp struct {
	OK     bool   `json:"ok"`
	Result string `json:"result"`
	Kind   string `json:"kind"`
	Error  string `json:"error,omitempty"`
}

// handleEvaluateFeel compiles and evaluates a FEEL expression against sample
// variables, so the Modeler's "Test expression" can show what an expression
// produces before deploying. A FEEL type error (number + string, division by
// zero, …) evaluates to null rather than erroring — reported faithfully as a
// null result. Like validation, it's a pure compile+eval over a caller-supplied
// scope: no engine state is read or written, so it runs off the single-writer
// loop and never touches the processor hot path (ADR-0008).
func (s *Server) handleEvaluateFeel(w http.ResponseWriter, r *http.Request) {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxFeelBytes))
	dec.UseNumber() // keep numbers exact (json.Number) for FEEL's decimals
	var req evalFeelReq
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Expression) == "" {
		writeJSON(w, http.StatusOK, evalFeelResp{OK: false, Error: "empty expression"})
		return
	}
	compiled, err := expr.CompileAuto(req.Expression)
	if err != nil {
		writeJSON(w, http.StatusOK, evalFeelResp{OK: false, Error: err.Error()})
		return
	}
	bindings, err := feelBindings(req.Variables)
	if err != nil {
		writeJSON(w, http.StatusOK, evalFeelResp{OK: false, Error: err.Error()})
		return
	}
	v, err := compiled.Eval(bindings)
	if err != nil {
		writeJSON(w, http.StatusOK, evalFeelResp{OK: false, Error: err.Error()})
		return
	}
	kind, b, text := expr.Classify(v)
	result := text
	switch kind {
	case expr.KindBool:
		result = strconv.FormatBool(b)
	case expr.KindNull:
		result = "null"
	}
	writeJSON(w, http.StatusOK, evalFeelResp{OK: true, Result: result, Kind: feelKindName(kind)})
}

// feelBindings converts the JSON sample variables into FEEL values. Numbers keep
// their exact text (json.Number) so decimals aren't mangled by float rounding.
// Objects and arrays bind as FEEL contexts and lists — the same contract as start
// variables (ADR-0037).
func feelBindings(in map[string]any) (map[string]expr.Value, error) {
	out := make(map[string]expr.Value, len(in))
	for name, raw := range in {
		switch x := raw.(type) {
		case nil:
			out[name] = expr.Null
		case bool:
			out[name] = expr.Bool(x)
		case string:
			out[name] = expr.String(x)
		case json.Number:
			out[name] = expr.FromStored(expr.KindNumber, false, x.String())
		case map[string]any, []any:
			out[name] = expr.FromJSON(x)
		default:
			return nil, fmt.Errorf("variable %q: unsupported value type %T", name, raw)
		}
	}
	return out, nil
}

// feelKindName maps a classified value kind to the label the UI shows.
func feelKindName(k expr.ValueKind) string {
	switch k {
	case expr.KindBool:
		return "boolean"
	case expr.KindNumber:
		return "number"
	case expr.KindString:
		return "string"
	case expr.KindJSON:
		return "json"
	default:
		return "null"
	}
}

// handleDeploy parses a BPMN XML body, compiles and deploys every executable
// process it contains — one for a plain model, several for a collaboration (one
// per pool) — and returns the assigned key/id/version for each. Each pool's
// process becomes its own runnable definition; the message flows between pools
// are the diagram's counterpart of the message events that link them at runtime
// (ADR-0023).
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
		var deployed []deployedProcess
		// The direct deploy endpoint carries no DMN reference; a business rule task
		// deployed this way has no decision model and would park. DMN execution is
		// wired through project bundle-deploy, which resolves the reference (ADR-0034).
		deployed, compErr, persistErr = s.deployModel(body, nil, time.Now().Unix())
		if compErr != nil || persistErr != nil {
			return
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

// deployModel compiles and registers every executable process in one BPMN model
// — one for a plain model, several for a collaboration (one per pool) — persisting
// each before registering it (durable before visible, I2 / ADR-0019). It returns
// the deployed processes, or a compile error (a client error: the model is
// invalid) or a persist error (a server error), the two failure modes the deploy
// handlers distinguish. It MUST be called on the run-loop goroutine (inside do),
// since it mutates the deployment registry and the processor.
//
// A mid-model persist failure leaves earlier processes deployed (no rollback
// yet) — an honest limitation until deployment is a first-class WAL event.
//
// dmnXML is the resolved DMN model this model's business rule tasks evaluate
// against (nil when there are none). It is snapshotted into each process's
// deployment record and registered in the DMN registry under the process key, so
// the tasks run now and re-register on restart (ADR-0014/ADR-0034). The caller is
// responsible for having validated it; a compile failure here is a server error.
func (s *Server) deployModel(body, dmnXML []byte, deployedAt int64) (deployed []deployedProcess, compErr, persistErr error) {
	deployables, err := compiler.ParseAll(s.nextKey, 1, bytes.NewReader(body))
	if err != nil {
		return nil, err, nil
	}
	deployed = make([]deployedProcess, 0, len(deployables))
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

		if err := s.deploys.save(persistedDeployment{
			Key:        key,
			ProcessID:  pid,
			Name:       name,
			Version:    version,
			DeployedAt: deployedAt,
			XML:        string(body),
			DMNXML:     string(dmnXML),
		}); err != nil {
			return deployed, nil, err
		}

		s.versions[pid] = version
		s.proc.Deploy(cp)
		// Register the process's decisions so its business rule tasks can evaluate.
		if dmnXML != nil {
			if err := s.dmnRegistry.Deploy(key, dmnXML); err != nil {
				return deployed, nil, fmt.Errorf("register dmn model for %s: %w", pid, err)
			}
		}
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
	return deployed, nil, nil
}

// handleListProcesses lists deployed definitions in registration order.
func (s *Server) handleListProcesses(w http.ResponseWriter, _ *http.Request) {
	list := []processResp{}
	s.do(func() {
		for _, key := range s.order {
			d := s.deployments[key]
			list = append(list, processResp{
				Key:              d.Key,
				ProcessID:        d.ProcessID,
				Name:             d.Name,
				Version:          d.Version,
				DeployedAt:       d.DeployedAt,
				CollaborationKey: s.collaborationKeyOf(d),
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
		// get returns the accumulator for an element index, creating it (and
		// recording its position) on first sight. Both the live-token scan and the
		// visit-history scan funnel through it, so an element carries its live and
		// historical counts on one entry.
		get := func(elementId int32) *runtimeElement {
			bid := d.cp.ElementBpmnId(elementId)
			if bid == "" {
				return nil
			}
			e := byElement[bid]
			if e == nil {
				e = &runtimeElement{ElementID: bid, Type: d.cp.Node(elementId).Type.String()}
				byElement[bid] = e
				order = append(order, bid)
			}
			return e
		}

		// Live tokens: element instances sitting on an element right now.
		scanErr = s.store.ActiveElementInstances(func(_ uint64, v *model.ElementInstanceValue) error {
			if v.ProcessDefKey != key {
				return nil
			}
			if instanceFilter != 0 && v.ProcessInstanceKey != instanceFilter {
				return nil
			}
			if e := get(v.ElementId); e != nil {
				e.Tokens++
				resp.Tokens++
			}
			return nil
		})
		if scanErr != nil {
			return
		}
		// History: every token that has ever passed through an element, so the
		// overlay shows the flow distribution even once instances have finished.
		scanErr = s.store.ElementVisitHistory(key, instanceFilter, func(elementId int32, count int64) error {
			if e := get(elementId); e != nil {
				e.Visits += int(count)
			}
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

// handleCollaborationRuntime returns the runtime of a whole collaboration for the
// replay view: its pools, the live-token/visited overlay merged across every pool
// onto the shared diagram, and the message flows that crossed between them in the
// order they occurred (the replay timeline, ADR-0038). The path key may be any
// pool of the collaboration; the sibling pools are discovered from the shared XML.
func (s *Server) handleCollaborationRuntime(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid definition key")
		return
	}
	var (
		found   bool
		scanErr error
		resp    = collabRuntimeResp{Pools: []collabPool{}, Elements: []runtimeElement{}, MessageFlows: []collabFlow{}}
	)
	// Flows are collected across pools then sorted into one timeline; ts leads,
	// with the log position as a stable tiebreaker for same-nanosecond flows.
	type tsFlow struct {
		ts  int64
		pos uint64
		f   collabFlow
	}
	var flows []tsFlow
	s.do(func() {
		d, ok := s.deployments[key]
		if !ok {
			return
		}
		found = true
		pools := s.poolSiblings(d)

		// scan runs one store scan, keeping the first error so later scans on a
		// broken store become no-ops instead of masking it.
		scan := func(fn func() error) {
			if scanErr == nil {
				scanErr = fn()
			}
		}
		byElement := map[string]*runtimeElement{}
		var order []string
		for _, pd := range pools {
			pd := pd
			resp.Pools = append(resp.Pools, collabPool{Key: pd.Key, ProcessID: pd.ProcessID, Name: pd.Name, Version: pd.Version})
			// Resolve this pool's element indices against its own compiled process;
			// the shared diagram's ids are globally unique, so pools merge cleanly.
			get := func(elementId int32) *runtimeElement {
				bid := pd.cp.ElementBpmnId(elementId)
				if bid == "" {
					return nil
				}
				e := byElement[bid]
				if e == nil {
					e = &runtimeElement{ElementID: bid, Type: pd.cp.Node(elementId).Type.String()}
					byElement[bid] = e
					order = append(order, bid)
				}
				return e
			}
			scan(func() error {
				return s.store.ActiveElementInstances(func(_ uint64, v *model.ElementInstanceValue) error {
					if v.ProcessDefKey != pd.Key {
						return nil
					}
					if e := get(v.ElementId); e != nil {
						e.Tokens++
						resp.Tokens++
					}
					return nil
				})
			})
			scan(func() error {
				return s.store.ElementVisitHistory(pd.Key, 0, func(elementId int32, count int64) error {
					if e := get(elementId); e != nil {
						e.Visits += int(count)
					}
					return nil
				})
			})
			scan(func() error {
				return s.store.ActiveProcessInstances(func(_ uint64, v *model.ProcessInstanceValue) error {
					if v.ProcessDefKey == pd.Key {
						resp.Instances++
					}
					return nil
				})
			})
			scan(func() error {
				return s.store.MessageFlowHistory(pd.Key, func(ts int64, pos uint64, v *model.MessageFlowValue) error {
					flows = append(flows, tsFlow{ts: ts, pos: pos, f: collabFlow{
						At:                ts,
						MessageName:       v.MessageName,
						CorrelationKey:    v.CorrelationKey,
						ReceiverElementID: pd.cp.ElementBpmnId(v.ReceiverElementId),
						SenderInstance:    v.SenderProcessInstanceKey,
						ReceiverInstance:  v.ReceiverProcessInstanceKey,
					}})
					return nil
				})
			})
		}
		if scanErr != nil {
			return
		}
		for _, bid := range order {
			resp.Elements = append(resp.Elements, *byElement[bid])
		}
	})
	switch {
	case !found:
		writeError(w, http.StatusNotFound, "no deployment with that key")
	case scanErr != nil:
		writeError(w, http.StatusInternalServerError, "read collaboration runtime: "+scanErr.Error())
	default:
		sort.Slice(flows, func(i, j int) bool {
			if flows[i].ts != flows[j].ts {
				return flows[i].ts < flows[j].ts
			}
			return flows[i].pos < flows[j].pos
		})
		for _, tf := range flows {
			resp.MessageFlows = append(resp.MessageFlows, tf.f)
		}
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
		if err := s.jobRunner.Drive(); err != nil {
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

// parseStartVariables reads {"variables": {name: value}} from a request body
// into VariableValues. Scalars (number, string, boolean, null) are stored
// directly; numbers keep their exact textual form for FEEL's decimal semantics.
// Objects and arrays are stored as canonical JSON under VarJSON, so they bind
// back into FEEL as contexts and lists for property access (ADR-0037).
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
		case map[string]any, []any:
			text, ok := expr.ToJSON(expr.FromJSON(x))
			if !ok {
				return nil, fmt.Errorf("variable %q: value is not encodable as JSON", name)
			}
			vv.Kind, vv.Text = model.VarJSON, text
		default:
			return nil, fmt.Errorf("variable %q: unsupported value type %T", name, raw)
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
	case model.VarJSON:
		out.Kind, out.Value = "json", v.Text
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
		if err := s.jobRunner.Drive(); err != nil {
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
		if err := s.jobRunner.Drive(); err != nil {
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
	ProjectID string `json:"projectId,omitempty"`
	SavedAt   int64  `json:"savedAt"`
}

// handleSaveDraft persists a diagram as a draft: the raw BPMN XML is stored as-is,
// keyed by its process id, WITHOUT compiling it — so an incomplete or not-yet
// executable model can still be saved and reopened. Re-saving the same process id
// overwrites the previous draft rather than creating a version. An optional
// ?projectId= query files the draft into that project (ADR-0034); it must name an
// existing project, else the save is rejected.
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
	projectID := r.URL.Query().Get("projectId")
	hasProjectParam := r.URL.Query().Has("projectId")
	rec := draft{ProcessID: pid, Name: name, ProjectID: projectID, SavedAt: time.Now().Unix(), XML: string(body)}
	var (
		saveErr, projErr error
		unknownProject   bool
	)
	s.do(func() {
		if !hasProjectParam {
			existing, ok, e := s.drafts.get(pid)
			if e == nil && ok {
				rec.ProjectID = existing.ProjectID
			}
		} else if projectID != "" {
			_, ok, e := s.projects.get(projectID)
			if e != nil {
				projErr = e
				return
			}
			if !ok {
				unknownProject = true
				return
			}
		}
		saveErr = s.drafts.save(rec)
	})
	switch {
	case projErr != nil:
		writeError(w, http.StatusInternalServerError, "read project: "+projErr.Error())
	case unknownProject:
		writeError(w, http.StatusBadRequest, "unknown project id")
	case saveErr != nil:
		writeError(w, http.StatusInternalServerError, "save draft: "+saveErr.Error())
	default:
		writeJSON(w, http.StatusOK, draftResp{ProcessID: pid, Name: name, ProjectID: rec.ProjectID, SavedAt: rec.SavedAt})
	}
}

// handleListDrafts lists saved drafts, most recently saved first. An optional
// ?projectId= query narrows the list to one project's artifacts (ADR-0034).
func (s *Server) handleListDrafts(w http.ResponseWriter, r *http.Request) {
	filter := r.URL.Query().Get("projectId")
	list := []draftResp{}
	var loadErr error
	s.do(func() {
		var recs []draft
		recs, loadErr = s.drafts.loadAll()
		for _, d := range recs {
			if filter != "" && d.ProjectID != filter {
				continue
			}
			list = append(list, draftResp{ProcessID: d.ProcessID, Name: d.Name, ProjectID: d.ProjectID, SavedAt: d.SavedAt})
		}
	})
	if loadErr != nil {
		writeError(w, http.StatusInternalServerError, "list drafts: "+loadErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleMoveDraft reassigns a draft to a different project (or to Ungrouped when
// projectId is empty), without touching its XML. Body: {"projectId": "..."}. A
// non-empty projectId must name an existing project (ADR-0034).
func (s *Server) handleMoveDraft(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload struct {
		ProjectID string `json:"projectId"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	var (
		found, unknownProject    bool
		getErr, projErr, saveErr error
		view                     draftResp
	)
	s.do(func() {
		rec, ok, e := s.drafts.get(id)
		if e != nil {
			getErr = e
			return
		}
		if !ok {
			return
		}
		found = true
		if payload.ProjectID != "" {
			_, pok, pe := s.projects.get(payload.ProjectID)
			if pe != nil {
				projErr = pe
				return
			}
			if !pok {
				unknownProject = true
				return
			}
		}
		rec.ProjectID = payload.ProjectID
		if saveErr = s.drafts.save(rec); saveErr != nil {
			return
		}
		view = draftResp{ProcessID: rec.ProcessID, Name: rec.Name, ProjectID: rec.ProjectID, SavedAt: rec.SavedAt}
	})
	switch {
	case getErr != nil:
		writeError(w, http.StatusInternalServerError, "read draft: "+getErr.Error())
	case !found:
		writeError(w, http.StatusNotFound, "no draft with that process id")
	case projErr != nil:
		writeError(w, http.StatusInternalServerError, "read project: "+projErr.Error())
	case unknownProject:
		writeError(w, http.StatusBadRequest, "unknown project id")
	case saveErr != nil:
		writeError(w, http.StatusInternalServerError, "move draft: "+saveErr.Error())
	default:
		writeJSON(w, http.StatusOK, view)
	}
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
