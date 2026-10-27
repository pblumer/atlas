package api

import (
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/archsketch"
	"github.com/pblumer/atlas/compiler"
)

// The cross-application coupling of a release's architecture (ADR-0495 slice E).
//
// A message is correlated by its name across the installation and a signal is
// broadcast to every process that catches its name, so the deployed definitions say
// who else is at the other end of each name the release throws or receives: the same
// reading the message and signal inventories make (ADR-0446, ADR-0447), taken once,
// on the run loop, when the snapshot is.

// collectNames adds the messages and signals one process throws, and the signals it
// catches; the messages it receives are its entry points already.
func collectNames(c *archsketch.Coupling, cp *compiler.CompiledProcess, processID string) {
	use := func(element string) []archsketch.Use {
		return []archsketch.Use{{ProcessID: processID, ElementID: element}}
	}
	for _, mp := range cp.MessageSenders() {
		if strings.TrimSpace(mp.MessageName) != "" {
			c.Names = append(c.Names, archsketch.NameUse{Kind: archsketch.NameMessage, Name: mp.MessageName,
				Direction: archsketch.DirectionOut, UsedBy: use(mp.Element)})
		}
	}
	for _, sp := range cp.SignalPoints() {
		if strings.TrimSpace(sp.SignalName) == "" {
			continue
		}
		direction := archsketch.DirectionOut
		if sp.Receives() {
			direction = archsketch.DirectionIn
		}
		c.Names = append(c.Names, archsketch.NameUse{Kind: archsketch.NameSignal, Name: sp.SignalName, Direction: direction, UsedBy: use(sp.Element)})
	}
}

// couplingPart completes the coupling: the processes of other applications at the other
// end of each name, read from the newest active version of every deployed definition,
// and the event feed's enabled subscriptions with the endpoints they push to. A
// process of the release's own application is not a peer. Nil when a store cannot be
// read. Run-loop goroutine only.
func (s *Server) couplingPart(appID string, c *archsketch.Coupling, received []archsketch.EntryPoint) *archsketch.Coupling {
	want := map[[3]string]bool{}
	for _, e := range received {
		want[[3]string{archsketch.NameMessage, e.Name, archsketch.DirectionIn}] = true
	}
	for _, n := range c.Names {
		want[[3]string{n.Kind, n.Name, n.Direction}] = true
	}
	names := map[string]string{}
	appName := func(id string) (string, bool) {
		if id == "" {
			return "", true
		}
		if n, ok := names[id]; ok {
			return n, true
		}
		proj, ok, err := s.projects.Get(id)
		if err != nil {
			return "", false
		}
		if ok {
			names[id] = proj.Name
		}
		return names[id], true
	}
	for _, d := range s.readableDefinitions(nil) {
		if !d.newest || d.inactive || (appID != "" && d.projectID == appID) {
			continue
		}
		app, ok := appName(d.projectID)
		if !ok {
			return nil
		}
		peer := func(kind, name, direction, element string) {
			if want[[3]string{kind, name, direction}] {
				c.Peers = append(c.Peers, archsketch.Peer{Kind: kind, Name: name, Direction: direction,
					ApplicationID: d.projectID, Application: app, ProcessID: d.processID, ElementID: element, System: d.system})
			}
		}
		// A peer that throws a name the release receives is coupled in; one that
		// receives a name the release throws is coupled out.
		for _, mp := range d.cp.MessageSenders() {
			peer(archsketch.NameMessage, mp.MessageName, archsketch.DirectionIn, mp.Element)
		}
		for _, mp := range d.cp.MessageReceivers() {
			peer(archsketch.NameMessage, mp.MessageName, archsketch.DirectionOut, mp.Element)
		}
		for _, sp := range d.cp.SignalPoints() {
			if sp.Receives() {
				peer(archsketch.NameSignal, sp.SignalName, archsketch.DirectionOut, sp.Element)
			} else {
				peer(archsketch.NameSignal, sp.SignalName, archsketch.DirectionIn, sp.Element)
			}
		}
	}
	if s.feedSubs != nil {
		subs, err := s.feedSubs.LoadAll()
		if err != nil {
			return nil
		}
		for _, sub := range subs {
			if !sub.Enabled {
				continue
			}
			c.Feed.Subscriptions++
			t := archsketch.FeedTarget{ConfiguredID: sub.WorkerID}
			if w, ok, err := s.connectors.Get(sub.WorkerID); err == nil && ok {
				t.Worker, t.Endpoint = w.Name, archsketch.ParseEndpoint(w.Endpoint)
			}
			c.Feed.Targets = append(c.Feed.Targets, t)
		}
	}
	return c
}

// redactCoupling is the coupling and the calls as this reader may see them: another
// application, and the processes in it, only for a reader who may see that
// application — the starmap's rule (ADR-0211, ADR-0495 §5) — and a feed endpoint only
// for a reader who may see its Worker. A process filed in no application, or in one
// that is gone, is judged by who deployed its current version, as the starmap judges
// it. Run-loop goroutine only.
func (s *Server) redactCoupling(r *http.Request, out *archsketch.Snapshot, mayViewWorker func(string) bool) {
	projs, err := s.projectsByID()
	mayView := func(appID, processID string) bool {
		if err != nil {
			return false
		}
		owner := ""
		if d := s.latestDeploymentByProcessID(processID); d != nil {
			owner = d.DeployedBy
		}
		return s.canViewArtifact(r, appID, owner, projs)
	}
	if len(out.Dependencies) > 0 {
		deps := make([]archsketch.Dependency, len(out.Dependencies))
		copy(deps, out.Dependencies)
		for i, d := range deps {
			if d.Kind == archsketch.DependencyCall && d.TargetApplicationID != "" && !mayView(d.TargetApplicationID, d.Name) {
				deps[i].TargetApplicationID, deps[i].TargetApplication, deps[i].TargetWithheld = "", "", true
			}
		}
		out.Dependencies = deps
	}
	if out.Coupling != nil {
		red := out.Coupling.Redacted(func(p archsketch.Peer) bool { return mayView(p.ApplicationID, p.ProcessID) }, mayViewWorker)
		out.Coupling = &red
	}
}
