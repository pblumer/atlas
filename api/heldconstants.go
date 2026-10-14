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

// Constants the installation holds (ADR-0453, ADR-0456).
//
// A process reads two kinds of constant whose values are not in its model: an
// application's — application.NAME, held per application — and the installation's —
// server.NAME, held once for every application. DEV, INT and PROD each hold their own,
// which is the whole point: the BPMN is the same everywhere and declares which constant it
// reads (<atlas:constant source="application|server"/>), the installation says what the
// value is here.
//
// They are operator configuration in the sense of ADR-0105: durable on a sidecar, owned by
// the run-loop goroutine, pushed into the processor at startup and on every change, and
// never in the event log — an instance's value is frozen into its own creation events, so
// replay never asks for it again. The facts the namespaces carry besides — the node's
// name and environment, the application's id, key and name — are pushed the same way from
// where they are kept.

// heldConstant is one constant as it is declared and stored: the name is written in
// capitals (compiler.ValidConstantName), the type is one of the four a constant takes, and
// the value is its text, parsed by compiler.ParseConstantValue.
type heldConstant struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

// applicationConstants is one application's constants on this installation.
type applicationConstants struct {
	ApplicationID string         `json:"applicationId"`
	Constants     []heldConstant `json:"constants"`
	UpdatedAt     int64          `json:"updatedAt"`
	UpdatedBy     string         `json:"updatedBy,omitempty"`
}

// serverConstants is the installation's constants for every application: one record, under
// a fixed id, because a server is one installation.
type serverConstants struct {
	ID        string         `json:"id"`
	Constants []heldConstant `json:"constants"`
	UpdatedAt int64          `json:"updatedAt"`
	UpdatedBy string         `json:"updatedBy,omitempty"`
}

const serverConstantsID = "server"

// applicationConstantStore and serverConstantStore keep their records one JSON file each —
// the sidecar approach every operator-config store uses (ADR-0019, ADR-0105). Owned by the
// run-loop goroutine, so they need no locking of their own.
type (
	applicationConstantStore = sidecar.Store[applicationConstants]
	serverConstantStore      = sidecar.Store[serverConstants]
)

func newApplicationConstantStore(dir string) (*applicationConstantStore, error) {
	return sidecar.NewStore(dir, "applicationconstantstore",
		func(rec applicationConstants) string { return rec.ApplicationID },
		sidecar.Order(func(a, b applicationConstants) bool { return a.ApplicationID < b.ApplicationID }),
	)
}

func newServerConstantStore(dir string) (*serverConstantStore, error) {
	return sidecar.NewStore(dir, "serverconstantstore",
		func(rec serverConstants) string { return rec.ID },
		sidecar.Order(func(a, b serverConstants) bool { return a.ID < b.ID }),
	)
}

// engineConstants parses a stored list into the form the processor seeds, refusing the
// first constant that is not one: a value that cannot be parsed must be refused when it is
// saved, never discovered when an instance starts.
func engineConstants(list []heldConstant) (map[string]engine.ConstantValue, error) {
	out := make(map[string]engine.ConstantValue, len(list))
	for _, c := range list {
		kind, b, text, err := compiler.ParseConstantValue(c.Type, c.Value)
		if err != nil {
			return nil, fmt.Errorf("constant %q: %w", c.Name, err)
		}
		out[c.Name] = engine.ConstantValue{Kind: inboundVarKind(kind), Bool: b, Text: text}
	}
	return out, nil
}

// cleanHeldConstants checks a submitted list the way the compiler checks a declaration:
// every name written in capitals, none twice, the type one of the four (string when
// omitted), and every value a literal of its type.
func cleanHeldConstants(in []heldConstant) ([]heldConstant, map[string]engine.ConstantValue, error) {
	out := make([]heldConstant, 0, len(in))
	seen := map[string]bool{}
	for _, c := range in {
		c.Name = strings.TrimSpace(c.Name)
		c.Type = strings.TrimSpace(c.Type)
		if c.Type == "" {
			c.Type = "string"
		}
		if err := compiler.ValidConstantName(c.Name); err != nil {
			return nil, nil, err
		}
		if seen[c.Name] {
			return nil, nil, fmt.Errorf("constant %q is listed twice", c.Name)
		}
		seen[c.Name] = true
		out = append(out, c)
	}
	vals, err := engineConstants(out)
	if err != nil {
		return nil, nil, err
	}
	return out, vals, nil
}

// loadHeldConstants pushes the installation's constants and the facts the namespaces carry
// into the processor at startup: every application's constants and its id, key and name,
// the installation's constants, and the node identity. Which application each definition
// is filed under is pushed where the definition is registered (loadDeployment,
// deployModel) and where it moves (handleMoveProcess). Before the loop serves traffic, so
// touching the processor directly is single-writer-safe — the same discipline as
// loadCallOverrides. A record that no longer parses is skipped rather than failing
// startup: its constants are then missing, which the Constants pages and every start a
// person makes report.
func (s *Server) loadHeldConstants() error {
	recs, err := s.appConstants.LoadAll()
	if err != nil {
		return err
	}
	for _, rec := range recs {
		if vals, err := engineConstants(rec.Constants); err == nil {
			s.proc.SetApplicationConstants(rec.ApplicationID, vals)
		}
	}
	apps, err := s.projects.LoadAll()
	if err != nil {
		return err
	}
	for _, app := range apps {
		s.noteApplication(app)
	}
	if rec, ok, err := s.srvConstants.Get(serverConstantsID); err != nil {
		return err
	} else if ok {
		if vals, err := engineConstants(rec.Constants); err == nil {
			s.proc.SetServerConstants(vals)
		}
	}
	if identity, ok, err := s.settings.getNode(); err == nil && ok {
		s.noteNode(identity)
	}
	return nil
}

// noteApplication tells the processor what the application namespace says about an
// application. Run-loop goroutine (or startup): called wherever an application is saved
// with a name or key that may have changed.
//
// A Server assembled without a processor — a store-level test — has nobody to tell, so
// the three note helpers do nothing there.
func (s *Server) noteApplication(app project) {
	if s.proc == nil {
		return
	}
	s.proc.SetApplicationInfo(app.ID, engine.ApplicationInfo{Key: app.Key, Name: app.Name})
}

// forgetApplication drops what the processor holds for a deleted application: its
// constants and the facts its namespace reported. Run-loop goroutine only.
func (s *Server) forgetApplication(id string) {
	if s.proc == nil {
		return
	}
	s.proc.SetApplicationConstants(id, nil)
	s.proc.SetApplicationInfo(id, engine.ApplicationInfo{})
}

// noteNode tells the processor what the server namespace says about this installation.
// Run-loop goroutine (or startup): at startup and whenever an operator names the node.
func (s *Server) noteNode(identity nodeIdentity) {
	if s.proc == nil {
		return
	}
	s.proc.SetServerInfo(engine.ServerInfo{ID: identity.ID, Name: identity.Name, Environment: identity.Environment})
}

// boundConstantRefusalOnLoop is why a person may not start this definition yet, or "": a
// constant the installation is to hold — for its application, or for every application —
// that has neither a value here nor a fallback would start the instance without it, and
// the person starting is there to read why it did not. Engine-internal starts — a timer, a
// message, a signal, a call activity — are not refused, because refusing them would lose
// the trigger; the deploy warned about them instead. A fact Atlas fills itself is never a
// reason to refuse.
//
// It asks the processor, which holds exactly what the instance would be seeded with, so a
// start costs no read of a store.
func (s *Server) boundConstantRefusalOnLoop(d *deployment) string {
	if d == nil || d.cp == nil {
		return ""
	}
	for _, k := range d.cp.Constants() {
		if !k.Bound() || k.Info || k.HasFallback {
			continue
		}
		switch k.Namespace {
		case compiler.ApplicationNamespace:
			if d.ProjectID == "" {
				return fmt.Sprintf("application.%s takes its value from the application, but this process is not filed under one; file it under its application, or give the constant a fallback value (ADR-0453)", k.Name)
			}
			if !s.proc.HasApplicationConstant(d.Key, k.Name) {
				return fmt.Sprintf("the application constant %q is not set on this installation; set it on the application's Constants tab before starting this process (ADR-0453)", k.Name)
			}
		case compiler.ServerNamespace:
			if !s.proc.HasServerConstant(k.Name) {
				return fmt.Sprintf("the server constant %q is not set on this installation; set it under Admin → Engine → Server constants before starting this process (ADR-0456)", k.Name)
			}
		}
	}
	return ""
}

// boundConstantWarningsOnLoop is what a deploy says about a definition's bound constants
// and facts on this installation: an application constant in a process filed under no
// application, a constant the installation does not hold and no fallback covers, a
// constant held with a type other than the one declared, and a fact not set here. Never a
// refusal — a model is routinely deployed before its world exists (ADR-0158).
func (s *Server) boundConstantWarningsOnLoop(d *deployment) []string {
	if d == nil || d.cp == nil {
		return nil
	}
	var out []string
	var appHave, srvHave map[string]heldConstant
	for _, k := range d.cp.Constants() {
		if !k.Bound() {
			continue
		}
		path := k.Namespace + "." + k.Name
		if k.Namespace == compiler.ApplicationNamespace && d.ProjectID == "" {
			out = append(out, fmt.Sprintf("%s: %s takes its value from the application, but the process was deployed outside any application, so every instance gets %s",
				d.ProcessID, path, fallbackPhrase(k)))
			continue
		}
		if k.Info {
			if !s.factHeld(d, &k) {
				out = append(out, fmt.Sprintf("%s: %s is not set on this installation, so every instance gets %s", d.ProcessID, path, fallbackPhrase(k)))
			}
			continue
		}
		var have map[string]heldConstant
		where := "the application's Constants tab"
		if k.Namespace == compiler.ApplicationNamespace {
			if appHave == nil {
				appHave, _ = s.applicationConstantsOnLoop(d.ProjectID)
			}
			have = appHave
		} else {
			if srvHave == nil {
				srvHave, _ = s.serverConstantsOnLoop()
			}
			have, where = srvHave, "Admin → Engine → Server constants"
		}
		v, ok := have[k.Name]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("%s: %s is not set on this installation, so every instance gets %s until it is set under %s%s",
				d.ProcessID, path, fallbackPhrase(k), where, startPhrase(k)))
		case v.Type != k.Type:
			out = append(out, fmt.Sprintf("%s: %s is declared as %s, but this installation holds it as %s; instances get the %s",
				d.ProcessID, path, k.Type, v.Type, v.Type))
		}
	}
	return out
}

// factHeld reports whether this installation knows the fact a definition declares. Run
// loop only: it reads the settings store for the node identity.
func (s *Server) factHeld(d *deployment, k *compiler.Constant) bool {
	if k.Namespace == compiler.ApplicationNamespace {
		return d.ProjectID != "" // id, key and name are an application's own
	}
	identity, ok, err := s.settings.getNode()
	if err != nil || !ok {
		return false
	}
	switch k.Name {
	case "id":
		return identity.ID != ""
	case "name":
		return identity.Name != ""
	case "environment":
		return identity.Environment != ""
	}
	return false
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

// applicationConstantsOnLoop and serverConstantsOnLoop read the constants held here, by
// name. Run loop only.
func (s *Server) applicationConstantsOnLoop(applicationID string) (map[string]heldConstant, error) {
	out := map[string]heldConstant{}
	if applicationID == "" {
		return out, nil
	}
	rec, ok, err := s.appConstants.Get(applicationID)
	if err != nil || !ok {
		return out, err
	}
	for _, c := range rec.Constants {
		out[c.Name] = c
	}
	return out, nil
}

func (s *Server) serverConstantsOnLoop() (map[string]heldConstant, error) {
	out := map[string]heldConstant{}
	rec, ok, err := s.srvConstants.Get(serverConstantsID)
	if err != nil || !ok {
		return out, err
	}
	for _, c := range rec.Constants {
		out[c.Name] = c
	}
	return out, nil
}

// requiredConstant is one constant the deployed processes read from a namespace.
type requiredConstant struct {
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Processes []string `json:"processes"`
	// Fallback is set when every process reading the name declares a fallback, so a
	// missing constant costs nothing worse than the fallback.
	Fallback bool `json:"fallback"`
	// State is "set", "missing", "fallback" (missing, and covered by a fallback) or
	// "type-mismatch" (held with another type than a process declares).
	State string `json:"state"`
}

// heldConstantsResp is what the Constants pages read: the constants held here, what the
// deployed processes require of them, and who last changed them.
type heldConstantsResp struct {
	ApplicationID string             `json:"applicationId,omitempty"`
	Constants     []heldConstant     `json:"constants"`
	Required      []requiredConstant `json:"required"`
	UpdatedAt     int64              `json:"updatedAt,omitempty"`
	UpdatedBy     string             `json:"updatedBy,omitempty"`
	UpdatedByName string             `json:"updatedByName,omitempty"`
}

// requiredConstantsOnLoop collects what the newest version of each deployed process that
// counts reads from namespace, and how this installation answers each. Facts are left out:
// nobody sets them here.
func (s *Server) requiredConstantsOnLoop(namespace string, counts func(*deployment) bool, have map[string]heldConstant) []requiredConstant {
	newest := map[string]int32{}
	for _, key := range s.order {
		if d := s.deployments[key]; counts(d) && d.Version > newest[d.ProcessID] {
			newest[d.ProcessID] = d.Version
		}
	}
	byName := map[string]*requiredConstant{}
	var order []string
	for _, key := range s.order {
		d := s.deployments[key]
		if !counts(d) || d.cp == nil || d.Version != newest[d.ProcessID] {
			continue
		}
		for _, k := range d.cp.Constants() {
			if k.Namespace != namespace || k.Info {
				continue
			}
			req := byName[k.Name]
			if req == nil {
				req = &requiredConstant{Name: k.Name, Type: k.Type, Fallback: true}
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
	out := make([]requiredConstant, 0, len(order))
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

// readHeldConstantsBody decodes a PUT body {"constants": [...]} and checks it.
func (s *Server) readHeldConstantsBody(r *http.Request) ([]heldConstant, map[string]engine.ConstantValue, error) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().Settings))
	if err != nil {
		return nil, nil, fmt.Errorf("read body: %w", err)
	}
	var body struct {
		Constants []heldConstant `json:"constants"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, nil, fmt.Errorf("invalid body: %w", err)
	}
	return cleanHeldConstants(body.Constants)
}

// handleGetApplicationConstants answers what an application holds on this installation and
// what its deployed processes require. Anyone who may view the application may read it:
// the constants are identifiers an instance carries, not secrets.
func (s *Server) handleGetApplicationConstants(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, code, msg := s.authorizeProject(r, id, ScopeRoleViewer); code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	out := heldConstantsResp{ApplicationID: id, Constants: []heldConstant{}}
	var readErr error
	s.do(func() {
		rec, ok, err := s.appConstants.Get(id)
		if err != nil {
			readErr = err
			return
		}
		have := map[string]heldConstant{}
		if ok {
			out.Constants = append(out.Constants, rec.Constants...)
			out.UpdatedAt, out.UpdatedBy = rec.UpdatedAt, rec.UpdatedBy
			for _, c := range rec.Constants {
				have[c.Name] = c
			}
		}
		out.Required = s.requiredConstantsOnLoop(compiler.ApplicationNamespace,
			func(d *deployment) bool { return d.ProjectID == id }, have)
	})
	if readErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read application constants: "+readErr.Error())
		return
	}
	out.UpdatedByName = s.principalNamer()(out.UpdatedBy)
	httpapi.JSON(w, http.StatusOK, out)
}

// handlePutApplicationConstants replaces an application's constants on this installation.
// Admin-gated by its route, like a call override (ADR-0105): it changes what every new
// instance of the application starts with. Every constant is checked before anything is
// written, so a malformed one refuses the whole set; then the set is persisted and only
// then handed to the processor (I2).
func (s *Server) handlePutApplicationConstants(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, code, msg := s.authorizeProject(r, id, ScopeRoleViewer); code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	list, vals, err := s.readHeldConstantsBody(r)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	rec := applicationConstants{ApplicationID: id, Constants: list, UpdatedAt: time.Now().Unix()}
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		rec.UpdatedBy = p.UserID
	}
	var saveErr error
	s.do(func() {
		if saveErr = s.appConstants.Save(rec); saveErr != nil {
			return
		}
		s.proc.SetApplicationConstants(id, vals)
	})
	if saveErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "persist application constants: "+saveErr.Error())
		return
	}
	s.handleGetApplicationConstants(w, r)
}

// handleGetServerConstants answers what this installation holds for every application and
// what the deployed processes require of it. Any signed-in caller may read it, for the
// reason an application's constants are readable: they are identifiers, not secrets, and
// the Modeler offers them while a model is written.
func (s *Server) handleGetServerConstants(w http.ResponseWriter, r *http.Request) {
	out := heldConstantsResp{Constants: []heldConstant{}}
	var readErr error
	s.do(func() {
		rec, ok, err := s.srvConstants.Get(serverConstantsID)
		if err != nil {
			readErr = err
			return
		}
		have := map[string]heldConstant{}
		if ok {
			out.Constants = append(out.Constants, rec.Constants...)
			out.UpdatedAt, out.UpdatedBy = rec.UpdatedAt, rec.UpdatedBy
			for _, c := range rec.Constants {
				have[c.Name] = c
			}
		}
		out.Required = s.requiredConstantsOnLoop(compiler.ServerNamespace,
			func(*deployment) bool { return true }, have)
	})
	if readErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read server constants: "+readErr.Error())
		return
	}
	out.UpdatedByName = s.principalNamer()(out.UpdatedBy)
	httpapi.JSON(w, http.StatusOK, out)
}

// handlePutServerConstants replaces the installation's constants. Admin-gated by its route:
// it changes what every new instance of every process reading them starts with. Checked,
// persisted, then handed to the processor, as for an application's.
func (s *Server) handlePutServerConstants(w http.ResponseWriter, r *http.Request) {
	list, vals, err := s.readHeldConstantsBody(r)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	rec := serverConstants{ID: serverConstantsID, Constants: list, UpdatedAt: time.Now().Unix()}
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		rec.UpdatedBy = p.UserID
	}
	var saveErr error
	s.do(func() {
		if saveErr = s.srvConstants.Save(rec); saveErr != nil {
			return
		}
		s.proc.SetServerConstants(vals)
	})
	if saveErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "persist server constants: "+saveErr.Error())
		return
	}
	s.handleGetServerConstants(w, r)
}
