package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/startgrant"
)

// The self-service half of ADR-0516: the routes a signed-in person reaches to see,
// and later start, the processes an administrator granted them. They are a second
// door to the same engine start the operator routes use, which is why they are
// Server methods beside it rather than an area service of their own: the start
// path, the deployment registry and the form checks are the server's, and a copy
// of any of them would be a second start implementation (ADR-0516, I4).
//
// The user role is the coarse gate the route table enforces. It is never enough:
// every request also needs a person's own session and a current grant, checked on
// the run loop against the newest deployed version, and a server without
// authentication offers none of it.

// maxStartableProcesses bounds the self-service list. A person is granted a few
// processes, not hundreds; a list longer than this says so with truncated.
const maxStartableProcesses = 200

// startableProcess is one process the signed-in person may start now, as the
// self-service list answers it. Nothing here names a grant, an audience or another
// person: what the list says is that this person may start this version.
type startableProcess struct {
	// Key is the definition key of the newest deployed version — the key the
	// self-service start names. It is not a permission: the start checks again.
	Key             uint64             `json:"key"`
	ProcessID       string             `json:"processId"`
	Name            string             `json:"name"`
	Version         int32              `json:"version"`
	ApplicationID   string             `json:"applicationId"`
	ApplicationName string             `json:"applicationName"`
	StartForm       startableStartForm `json:"startForm"`
}

// startableStartForm says whether the version starts from a form and which: a
// process without one starts with no values at all.
type startableStartForm struct {
	Available bool   `json:"available"`
	FormID    string `json:"formId,omitempty"`
}

// selfServicePrincipal is the gate in front of every self-service route. An open
// server answers 404: there is no such resource there, and its operators keep the
// Start view they have (ADR-0516). A credential that is not a person's own session
// — an API token, the internal token, an application a person connected — is
// refused even when it carries the user role: the right to start is the person's,
// and delegating it needs a design of its own.
func (s *Server) selfServicePrincipal(w http.ResponseWriter, r *http.Request) (*httpapi.Principal, bool) {
	if !s.authEnabled {
		httpapi.Error(w, http.StatusNotFound,
			"starting a process yourself needs authentication; on this server processes are started from the Start view")
		return nil, false
	}
	p := httpapi.PrincipalFrom(r.Context())
	if p == nil {
		httpapi.Error(w, http.StatusUnauthorized, "authentication required")
		return nil, false
	}
	if !p.Session || strings.TrimSpace(p.UserID) == "" {
		httpapi.Error(w, http.StatusForbidden,
			"starting a process yourself needs your own signed-in session; API tokens and connected applications cannot")
		return nil, false
	}
	return p, true
}

// grantPrincipal is the request's identity as the grant store reads it: the user id
// and the groups the session carries now, which every membership change updates in
// place (ADR-0185).
func grantPrincipal(p *httpapi.Principal) startgrant.Principal {
	return startgrant.Principal{UserID: p.UserID, GroupIDs: append([]string(nil), p.GroupIDs...)}
}

// handleMyStartableProcesses answers the processes the signed-in person may start
// now: GET /api/v1/me/startable-processes.
func (s *Server) handleMyStartableProcesses(w http.ResponseWriter, r *http.Request) {
	p, ok := s.selfServicePrincipal(w, r)
	if !ok {
		return
	}
	principal := grantPrincipal(p)
	var (
		items     []startableProcess
		err       error
		uncertain bool
	)
	s.do(func() {
		if uncertain = s.startGrants.Uncertain(); uncertain {
			return
		}
		items, err = s.startableOnLoop(s.startGrants.GrantedTo(principal))
	})
	switch {
	case uncertain:
		// Fail closed, and say so: an empty list would read as "nothing granted".
		httpapi.Error(w, http.StatusServiceUnavailable, "the processes you may start cannot be read right now; try again later")
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "startable processes: "+err.Error())
	default:
		page, total, truncated := pageStartable(items, maxStartableProcesses)
		httpapi.JSON(w, http.StatusOK, map[string]any{
			"items": page, "total": total, "totalExact": true, "truncated": truncated,
		})
	}
}

// startableOnLoop resolves granted scopes to the versions the person may start
// now. One pass over the registry finds the newest version of every granted
// process — the version latestDeploymentByProcessID answers, so the list, the
// administrator's preview and the start agree — and each application is read once.
// Runs on the loop.
func (s *Server) startableOnLoop(grants []startgrant.Grant) ([]startableProcess, error) {
	out := []startableProcess{}
	if len(grants) == 0 {
		return out, nil
	}
	want := make(map[string]bool, len(grants))
	for _, g := range grants {
		want[g.ProcessID] = true
	}
	newest := make(map[string]*deployment, len(want))
	for _, key := range s.order {
		d := s.deployments[key]
		if d == nil || !want[d.ProcessID] || d.Version != s.versions[d.ProcessID] {
			continue
		}
		newest[d.ProcessID] = d
	}
	type appRecord struct {
		p     project
		found bool
	}
	apps := map[string]appRecord{}
	for _, g := range grants {
		a, seen := apps[g.ApplicationID]
		if !seen {
			p, found, err := s.projects.Get(g.ApplicationID)
			if err != nil {
				return nil, err
			}
			a = appRecord{p: p, found: found}
			apps[g.ApplicationID] = a
		}
		d := newest[g.ProcessID]
		if t := s.targetFor(g.ApplicationID, a.p, a.found, d); !t.ProcessInApplication || len(t.Blockers) > 0 {
			continue
		}
		name := d.Name
		if name == "" {
			name = d.ProcessID
		}
		form := d.cp.StartFormId()
		out = append(out, startableProcess{
			Key: d.Key, ProcessID: d.ProcessID, Name: name, Version: d.Version,
			ApplicationID: g.ApplicationID, ApplicationName: a.p.Name,
			StartForm: startableStartForm{Available: form != "", FormID: form},
		})
	}
	return out, nil
}

// pageStartable orders the list the way a person scans it — by application, then by
// process name — with ids as tie-breakers so the order is total, and cuts it at
// limit.
func pageStartable(items []startableProcess, limit int) ([]startableProcess, int, bool) {
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if x, y := strings.ToLower(a.ApplicationName), strings.ToLower(b.ApplicationName); x != y {
			return x < y
		}
		if a.ApplicationID != b.ApplicationID {
			return a.ApplicationID < b.ApplicationID
		}
		if x, y := strings.ToLower(a.Name), strings.ToLower(b.Name); x != y {
			return x < y
		}
		if a.ProcessID != b.ProcessID {
			return a.ProcessID < b.ProcessID
		}
		return a.Key < b.Key
	})
	total := len(items)
	if total > limit {
		return items[:limit], total, true
	}
	return items, total, false
}
