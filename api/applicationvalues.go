package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/sidecar"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
)

// Application values per installation (ADR-0453).
//
// An application holds, on each installation, named and typed values its processes'
// constants bind to by name (<atlas:constant source="application"/>). DEV, INT and PROD
// each hold their own, which is the whole point: the BPMN is the same everywhere and says
// which value it needs, the installation says what that value is here.
//
// They are operator configuration in the sense of ADR-0105: durable on a sidecar, owned by
// the run-loop goroutine, pushed into the processor at startup and on every change, and
// never in the event log — an instance's value is frozen into its own creation events, so
// replay never asks for it again.

// applicationValue is one value as it is declared and stored: the type is one of the four
// a constant takes, and the value is its text, parsed by compiler.ParseConstantValue.
type applicationValue struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

// applicationValues is one application's values on this installation.
type applicationValues struct {
	ApplicationID string             `json:"applicationId"`
	Values        []applicationValue `json:"values"`
	UpdatedAt     int64              `json:"updatedAt"`
	UpdatedBy     string             `json:"updatedBy,omitempty"`
}

// applicationValueStore keeps one applicationValues record per application, one JSON file
// each — the sidecar approach every operator-config store uses (ADR-0019, ADR-0105). Owned
// by the run-loop goroutine, so it needs no locking of its own.
type applicationValueStore = sidecar.Store[applicationValues]

func newApplicationValueStore(dir string) (*applicationValueStore, error) {
	return sidecar.NewStore(dir, "applicationvaluestore",
		func(rec applicationValues) string { return rec.ApplicationID },
		sidecar.Order(func(a, b applicationValues) bool { return a.ApplicationID < b.ApplicationID }),
	)
}

// engineApplicationValues parses a record into the form the processor seeds, refusing the
// first value that is not one: a value that cannot be parsed must be refused when it is
// saved, never discovered when an instance starts.
func engineApplicationValues(vals []applicationValue) (map[string]engine.ApplicationValue, error) {
	out := make(map[string]engine.ApplicationValue, len(vals))
	for _, v := range vals {
		kind, b, text, err := compiler.ParseConstantValue(v.Type, v.Value)
		if err != nil {
			return nil, fmt.Errorf("value %q: %w", v.Name, err)
		}
		out[v.Name] = engine.ApplicationValue{Kind: inboundVarKind(kind), Bool: b, Text: text}
	}
	return out, nil
}

// loadApplicationValues pushes every application's values into the processor at startup.
// Which application each definition is filed under is pushed where the definition is
// registered (loadDeployment, deployModel) and where it moves (handleMoveProcess). Before
// the loop serves traffic, so touching the processor directly is single-writer-safe — the
// same discipline as loadCallOverrides. A record that no longer parses is skipped rather
// than failing startup: its application then has no values, which the Values tab and
// every start a person makes report.
func (s *Server) loadApplicationValues() error {
	recs, err := s.appValues.LoadAll()
	if err != nil {
		return err
	}
	for _, rec := range recs {
		vals, err := engineApplicationValues(rec.Values)
		if err != nil {
			continue
		}
		s.proc.SetApplicationValues(rec.ApplicationID, vals)
	}
	return nil
}

// applicationValuesOnLoop reads the values an application holds here, by name. Run loop
// only.
func (s *Server) applicationValuesOnLoop(applicationID string) (map[string]applicationValue, error) {
	out := map[string]applicationValue{}
	if applicationID == "" {
		return out, nil
	}
	rec, ok, err := s.appValues.Get(applicationID)
	if err != nil || !ok {
		return out, err
	}
	for _, v := range rec.Values {
		out[v.Name] = v
	}
	return out, nil
}

// applicationValueRefusalOnLoop is why a person may not start this definition yet, or "":
// a constant bound to its application that has neither a value here nor a fallback would
// start the instance without it, and the person starting is there to read why it did not.
// Engine-internal starts — a timer, a message, a signal, a call activity — are not refused,
// because refusing them would lose the trigger; the deploy warned about them instead.
//
// It asks the processor, which holds exactly what the instance would be seeded with, so a
// start costs no read of the store.
func (s *Server) applicationValueRefusalOnLoop(d *deployment) string {
	if d == nil || d.cp == nil {
		return ""
	}
	for _, k := range d.cp.Constants() {
		if !k.FromApplication || k.HasFallback {
			continue
		}
		if d.ProjectID == "" {
			return fmt.Sprintf("constant %q takes its value from the application, but this process is not filed under one; file it under its application, or give the constant a fallback value (ADR-0453)", k.Name)
		}
		if !s.proc.HasApplicationValue(d.Key, k.Name) {
			return fmt.Sprintf("the application value %q is not set on this installation; set it on the application's Values tab before starting this process (ADR-0453)", k.Name)
		}
	}
	return ""
}

// applicationValueWarningsOnLoop is what a deploy says about a definition's bound
// constants on this installation: one bound in a process filed under no application, a
// value the application does not hold and no fallback covers, and a value held with a
// type other than the one the constant declares. Never a refusal — a model is routinely
// deployed before its world exists (ADR-0158).
func (s *Server) applicationValueWarningsOnLoop(d *deployment) []string {
	if d == nil || d.cp == nil {
		return nil
	}
	var out []string
	var have map[string]applicationValue
	for _, k := range d.cp.Constants() {
		if !k.FromApplication {
			continue
		}
		if d.ProjectID == "" {
			out = append(out, fmt.Sprintf("%s: constant %q takes its value from the application, but the process was deployed outside any application, so every instance gets %s",
				d.ProcessID, k.Name, fallbackPhrase(k)))
			continue
		}
		if have == nil {
			have, _ = s.applicationValuesOnLoop(d.ProjectID)
		}
		v, ok := have[k.Name]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("%s: the application value %q is not set on this installation, so every instance gets %s until it is set on the application's Values tab%s",
				d.ProcessID, k.Name, fallbackPhrase(k), startPhrase(k)))
		case v.Type != k.Type:
			out = append(out, fmt.Sprintf("%s: constant %q declares type %s, but the application holds it as %s on this installation; instances get the %s",
				d.ProcessID, k.Name, k.Type, v.Type, v.Type))
		}
	}
	return out
}

func fallbackPhrase(k compiler.Constant) string {
	if k.HasFallback {
		return fmt.Sprintf("the fallback %q", k.Value)
	}
	return "no value"
}

func startPhrase(k compiler.Constant) string {
	if k.HasFallback {
		return ""
	}
	return "; a start from the Modeler, the Tasks app, a public link or the API is refused until then"
}

// requiredApplicationValue is one name the application's deployed processes bind to.
type requiredApplicationValue struct {
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Processes []string `json:"processes"`
	// Fallback is set when every process binding the name declares a fallback, so a
	// missing value costs nothing worse than the fallback.
	Fallback bool `json:"fallback"`
	// State is "set", "missing", "fallback" (missing, and covered by a fallback) or
	// "type-mismatch" (held with another type than a process declares).
	State string `json:"state"`
}

type applicationValuesResp struct {
	ApplicationID string                     `json:"applicationId"`
	Values        []applicationValue         `json:"values"`
	Required      []requiredApplicationValue `json:"required"`
	UpdatedAt     int64                      `json:"updatedAt,omitempty"`
	UpdatedBy     string                     `json:"updatedBy,omitempty"`
}

// requiredApplicationValuesOnLoop collects what the newest version of each of the
// application's deployed processes binds to, and how this installation answers each.
func (s *Server) requiredApplicationValuesOnLoop(applicationID string, have map[string]applicationValue) []requiredApplicationValue {
	newest := map[string]int32{}
	for _, key := range s.order {
		if d := s.deployments[key]; d.ProjectID == applicationID && d.Version > newest[d.ProcessID] {
			newest[d.ProcessID] = d.Version
		}
	}
	byName := map[string]*requiredApplicationValue{}
	var order []string
	for _, key := range s.order {
		d := s.deployments[key]
		if d.ProjectID != applicationID || d.cp == nil || d.Version != newest[d.ProcessID] {
			continue
		}
		for _, k := range d.cp.Constants() {
			if !k.FromApplication {
				continue
			}
			req := byName[k.Name]
			if req == nil {
				req = &requiredApplicationValue{Name: k.Name, Type: k.Type, Fallback: true}
				byName[k.Name] = req
				order = append(order, k.Name)
			}
			req.Processes = append(req.Processes, d.ProcessID)
			req.Fallback = req.Fallback && k.HasFallback
			if v, ok := have[k.Name]; ok && v.Type != k.Type {
				req.State = "type-mismatch"
			}
		}
	}
	sort.Strings(order)
	out := make([]requiredApplicationValue, 0, len(order))
	for _, name := range order {
		req := byName[name]
		sort.Strings(req.Processes)
		if req.State == "" {
			switch _, ok := have[name]; {
			case ok:
				req.State = "set"
			case req.Fallback:
				req.State = "fallback"
			default:
				req.State = "missing"
			}
		}
		out = append(out, *req)
	}
	return out
}

// handleGetApplicationValues answers what an application holds on this installation and
// what its deployed processes require. Anyone who may view the application may read it:
// the values are identifiers a constant writes into every instance, not secrets.
func (s *Server) handleGetApplicationValues(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, code, msg := s.authorizeProject(r, id, ScopeRoleViewer); code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	out := applicationValuesResp{ApplicationID: id, Values: []applicationValue{}}
	var readErr error
	s.do(func() {
		rec, ok, err := s.appValues.Get(id)
		if err != nil {
			readErr = err
			return
		}
		have := map[string]applicationValue{}
		if ok {
			out.Values = append(out.Values, rec.Values...)
			out.UpdatedAt, out.UpdatedBy = rec.UpdatedAt, rec.UpdatedBy
			for _, v := range rec.Values {
				have[v.Name] = v
			}
		}
		out.Required = s.requiredApplicationValuesOnLoop(id, have)
	})
	if readErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read application values: "+readErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// handlePutApplicationValues replaces an application's values on this installation.
// Admin-gated by its route, like a call override (ADR-0105): it changes what every new
// instance of the application starts with. Every value is parsed before anything is
// written, so a malformed one refuses the whole set; then the set is persisted and only
// then handed to the processor (I2).
func (s *Server) handlePutApplicationValues(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, code, msg := s.authorizeProject(r, id, ScopeRoleViewer); code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().Settings))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var body struct {
		Values []applicationValue `json:"values"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	rec := applicationValues{ApplicationID: id, Values: []applicationValue{}, UpdatedAt: time.Now().Unix()}
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		rec.UpdatedBy = p.UserID
	}
	seen := map[string]bool{}
	for _, v := range body.Values {
		v.Name = strings.TrimSpace(v.Name)
		v.Type = strings.TrimSpace(v.Type)
		if v.Type == "" {
			v.Type = "string"
		}
		if v.Name == "" {
			httpapi.Error(w, http.StatusBadRequest, "a value has no name")
			return
		}
		if seen[v.Name] {
			httpapi.Error(w, http.StatusBadRequest, fmt.Sprintf("value %q is listed twice", v.Name))
			return
		}
		seen[v.Name] = true
		rec.Values = append(rec.Values, v)
	}
	vals, err := engineApplicationValues(rec.Values)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	var saveErr error
	s.do(func() {
		if len(rec.Values) == 0 {
			saveErr = s.appValues.Delete(id)
		} else {
			saveErr = s.appValues.Save(rec)
		}
		if saveErr != nil {
			return
		}
		s.proc.SetApplicationValues(id, vals)
	})
	if saveErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "persist application values: "+saveErr.Error())
		return
	}
	s.handleGetApplicationValues(w, r)
}
