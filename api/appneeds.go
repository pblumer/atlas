package api

import (
	"bytes"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/compiler"
)

// What an application needs from the installation it runs on (ADR-0467, amended
// 2026-10-21): the server constants its processes read, the application constants
// whose values the installation holds, and the configured workers its tasks name. A
// model names each of them by name alone — never a value, an endpoint or a secret
// (ADR-0041, ADR-0453, ADR-0456) — so a source tree can say what its processes need,
// and only the installation can say whether it has it.
//
// The list is read from the processes themselves, every time: on export from the
// application's drafts, on import from the tree's files. The manifest carries a copy
// for whoever reads the tree, and an import never trusts it, because a process edited
// by hand in a repository would otherwise bring a need the copy does not mention.

// The kinds of need.
const (
	needServerConstant      = "server-constant"
	needApplicationConstant = "application-constant"
	needWorker              = "worker"
)

// sourceNeed is one thing an application's processes read from the installation.
type sourceNeed struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Type is a constant's declared type, or the Worker Type a worker is named as.
	Type string `json:"type"`
	// Processes are the ids of the processes that read the constant or name the worker.
	Processes []string `json:"processes"`
	// Fallback says every process reading the constant declares a value to use when
	// the installation holds none, so a missing one refuses no start.
	Fallback bool `json:"fallback,omitempty"`
	// State is how a server answers the need: for a constant "set", "missing",
	// "fallback" or "type-mismatch", as on the Constants pages; for a worker
	// "configured", "missing", "other-type" or "unusable". Empty where no server was
	// asked, as in a manifest.
	State string `json:"state,omitempty"`
	// Detail says what the state alone does not: the Worker Type a worker of that name
	// is configured as here, or why one configured here is not usable.
	Detail string `json:"detail,omitempty"`
}

// needSource is one process file: its artifact id and its BPMN.
type needSource struct {
	ID  string
	XML []byte
}

// needsOf reads what the processes need, sorted by kind and name, and names the
// processes it could not read because they do not compile: a draft is work in
// progress, and its needs are only known once it is a model. It compiles, so it runs
// off the run loop.
func needsOf(processes []needSource) (needs []sourceNeed, unread []string) {
	byKey := map[string]*sourceNeed{}
	add := func(kind, name, typ, process string, fallback bool) {
		key := kind + "/" + name
		n := byKey[key]
		if n == nil {
			n = &sourceNeed{Kind: kind, Name: name, Type: typ, Fallback: true}
			byKey[key] = n
		}
		if !slices.Contains(n.Processes, process) {
			n.Processes = append(n.Processes, process)
		}
		n.Fallback = n.Fallback && fallback
	}
	for _, p := range processes {
		deployables, err := compiler.ParseAll(1, 1, bytes.NewReader(p.XML))
		if err != nil {
			unread = append(unread, p.ID)
			continue
		}
		for _, d := range deployables {
			for _, k := range d.Process.Constants() {
				switch {
				case !k.Bound() || k.Info:
				case k.Namespace == compiler.ServerNamespace:
					add(needServerConstant, k.Name, k.Type, p.ID, k.HasFallback)
				case k.Namespace == compiler.ApplicationNamespace:
					add(needApplicationConstant, k.Name, k.Type, p.ID, k.HasFallback)
				}
			}
			for _, ref := range d.Process.ConnectorRefs() {
				kind := connectorKindOfJobType(ref.JobType)
				// A job type no Worker Type claims names no configured worker, and a
				// worker written as a FEEL expression is only known when the task runs
				// (connectorWarnings skips both for the same reasons).
				if kind == "" || strings.HasPrefix(strings.TrimSpace(ref.Connector), "=") {
					continue
				}
				add(needWorker, ref.Connector, kind, p.ID, false)
			}
		}
	}
	needs = make([]sourceNeed, 0, len(byKey))
	for _, n := range byKey {
		if n.Kind == needWorker {
			n.Fallback = false
		}
		sort.Strings(n.Processes)
		needs = append(needs, *n)
	}
	order := map[string]int{needServerConstant: 0, needApplicationConstant: 1, needWorker: 2}
	sort.Slice(needs, func(i, j int) bool {
		if needs[i].Kind != needs[j].Kind {
			return order[needs[i].Kind] < order[needs[j].Kind]
		}
		return needs[i].Name < needs[j].Name
	})
	sort.Strings(unread)
	return needs, unread
}

// answerNeedsOnLoop says how this installation answers each need, for the
// application appID ("" for one it does not have yet). pending holds application
// constants an import is about to set, which answer their need as if they were held.
// It reads the constant stores, the worker store and the live registries, so it runs
// on the run-loop goroutine (invariant I3).
func (s *Server) answerNeedsOnLoop(appID string, needs []sourceNeed, pending map[string]heldConstant) []sourceNeed {
	out := make([]sourceNeed, 0, len(needs))
	var (
		appHave, srvHave map[string]heldConstant
		workers          map[string]connector
	)
	for _, n := range needs {
		n.Processes = append([]string(nil), n.Processes...)
		switch n.Kind {
		case needServerConstant, needApplicationConstant:
			var have map[string]heldConstant
			if n.Kind == needServerConstant {
				if srvHave == nil {
					srvHave, _ = s.serverConstantsOnLoop()
				}
				have = srvHave
			} else {
				if appHave == nil {
					appHave, _ = s.applicationConstantsOnLoop(appID)
					for name, c := range pending {
						appHave[name] = c
					}
				}
				have = appHave
			}
			v, ok := have[n.Name]
			switch {
			case ok && v.Type != n.Type:
				n.State, n.Detail = "type-mismatch", v.Type
			case ok:
				n.State = "set"
			case n.Fallback:
				n.State = "fallback"
			default:
				n.State = "missing"
			}
		case needWorker:
			if workers == nil {
				workers = map[string]connector{}
				if recs, err := s.connectors.LoadAll(); err == nil {
					for _, c := range recs {
						workers[c.Name] = c
					}
				}
			}
			rec, ok := workers[n.Name]
			switch {
			case !ok:
				n.State = "missing"
			case rec.Kind != n.Type:
				n.State, n.Detail = "other-type", rec.Kind
			default:
				n.State = "configured"
				if why := s.connectorProblem(n.Type, n.Name); why != "" {
					n.State, n.Detail = "unusable", why
				}
			}
		}
		out = append(out, n)
	}
	return out
}

// applicationNeedsResp is what GET /api/v1/applications/{id}/needs answers: what the
// application's drafts need from an installation, how this one answers it, and how
// many constant values a download with its constants would carry.
type applicationNeedsResp struct {
	ApplicationID string       `json:"applicationId"`
	Needs         []sourceNeed `json:"needs"`
	// Unread names drafts whose needs could not be read, because they do not compile.
	Unread []string `json:"unread"`
	// Constants counts the values the application holds here.
	Constants int `json:"constants"`
	// Catalogs are the catalogues the caller may put in the application's package
	// (GET …/package?catalog=), each marked bound when one of its products runs on a
	// process of the application. Empty without the productmanager role, and on a
	// server with its catalogue switched off.
	Catalogs []catalog.ExportChoice `json:"catalogs"`
}

// handleApplicationNeeds serves GET /api/v1/applications/{id}/needs: the list a
// download of the application's source names, read from its drafts as the download
// carries them. Viewer access is enough, as for the download itself. The drafts are
// read on the run loop and compiled off it.
func (s *Server) handleApplicationNeeds(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, code, msg := s.authorizeProject(r, id, ScopeRoleViewer); code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	var (
		sources []needSource
		resp    = applicationNeedsResp{ApplicationID: id, Unread: []string{}, Catalogs: []catalog.ExportChoice{}}
		readErr error
	)
	s.do(func() {
		drafts, err := s.drafts.LoadAll()
		if err != nil {
			readErr = err
			return
		}
		for _, d := range drafts {
			if d.ProjectID == id {
				sources = append(sources, needSource{ID: d.ProcessID, XML: []byte(d.XML)})
			}
		}
		held, err := s.applicationConstantsOnLoop(id)
		if err != nil {
			readErr = err
			return
		}
		resp.Constants = len(held)
	})
	if readErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read application: "+readErr.Error())
		return
	}
	needs, unread := needsOf(sources)
	if unread != nil {
		resp.Unread = unread
	}
	s.do(func() { resp.Needs = s.answerNeedsOnLoop(id, needs, nil) })
	if p := httpapi.PrincipalFrom(r.Context()); !s.catalogueOff && mayReach(p, RoleProductManager) {
		processes := make([]string, 0, len(sources))
		for _, src := range sources {
			processes = append(processes, src.ID)
		}
		choices, err := s.catalogs.ExportChoices(p, processes)
		if err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "read catalogues: "+err.Error())
			return
		}
		resp.Catalogs = choices
	}
	httpapi.JSON(w, http.StatusOK, resp)
}
