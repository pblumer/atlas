package api

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/eventcatalog"
)

// The signals and messages of this installation (ADR-0447, ADR-0446).
//
// A signal or a message couples models by name alone, engine-wide and across
// projects, and the event catalogue (ADR-0435) does not list them: it is the contract
// of what atlas itself emits. These two views are derived on request from the
// deployed definitions instead, so nothing is stored and nothing can drift. An
// observation is not a contract: no field here promises a payload, because the
// payload of an own signal or message is every variable of the instance that sends
// it.
//
// Every deployed version is listed, and the newest of each process is marked. The
// findings read only the newest version of each process: an older version's start
// never fires, and whether its catches and throws are still live depends on running
// instances, which these views do not read. The page that shows them says so.
//
// Neither finding is a validation of a model: whether a receiver exists depends on
// other deployments, and the order of deploys is free (ADR-0026). They are not in the
// Problems panel.

// The findings, one function per channel computes them, and the route, the Console
// and MCP show its answer.
const (
	findingSignalUnreceived      = "signal.unreceived"
	findingSignalUnthrown        = "signal.unthrown"
	findingSignalCrossesProjects = "signal.crosses-projects"
	findingSignalReservedName    = "signal.reserved-name"

	findingMessageUnreceived      = "message.unreceived"
	findingMessageCrossesProjects = "message.crosses-projects"
	findingMessageReservedName    = "message.reserved-name"
)

// modelEventFinding is one thing the definitions say is wrong with a name, or worth
// knowing about it: its rule and the sentence that names who is concerned.
type modelEventFinding struct {
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

// definitionReadable is the check that governs reading a deployed definition, and
// the one every listing derived from definitions passes each one through (ADR-0447
// §5). Today it admits every signed-in caller, as GET /api/v1/processes/{key}/xml
// does, so the views aggregate nothing a caller could not read one definition at a
// time. If definitions become object-gated, this is where, and both narrow together.
func definitionReadable(_ *httpapi.Principal, _ *deployment) bool { return true }

// newestVersions is the newest deployed version of every process. Runs on the loop.
func (s *Server) newestVersions() map[string]int32 {
	out := map[string]int32{}
	for _, d := range s.deployments {
		if d == nil {
			continue
		}
		if v, ok := out[d.ProcessID]; !ok || d.Version > v {
			out[d.ProcessID] = d.Version
		}
	}
	return out
}

// isSystemDefinition reports whether a deployed definition is one of atlas's own: a
// system process, filed in the system project. Such a definition may speak for atlas
// under an atlas.* name; any other that does is a finding. Runs on the loop.
func (s *Server) isSystemDefinition(d *deployment) bool {
	return s.systemPIDs[d.ProcessID] || d.ProjectID == systemProjectID
}

// deployedDefinition is what a listing derived from the definitions needs of one,
// copied on the loop. The compiled process is immutable, so its points are read off
// the loop, and the loop holds no more than the copy (invariant I3).
type deployedDefinition struct {
	key       uint64
	processID string
	name      string
	version   int32
	newest    bool
	projectID string
	system    bool
	inactive  bool
	cp        *compiler.CompiledProcess
}

// readableDefinitions copies every deployed definition p may read, in deploy order.
// Runs on the loop.
func (s *Server) readableDefinitions(p *httpapi.Principal) []deployedDefinition {
	newest := s.newestVersions()
	out := make([]deployedDefinition, 0, len(s.order))
	for _, key := range s.order {
		d := s.deployments[key]
		if d == nil || d.cp == nil || !definitionReadable(p, d) {
			continue
		}
		out = append(out, deployedDefinition{
			key: d.Key, processID: d.ProcessID, name: d.Name, version: d.Version,
			newest: newest[d.ProcessID] == d.Version, projectID: d.ProjectID,
			system: s.isSystemDefinition(d), inactive: d.inactive, cp: d.cp,
		})
	}
	return out
}

// deployedPointView is one deployed element that throws or receives a signal
// (ADR-0447 §1); the message findings read a process row of message-sources as one
// too. Interrupting is set for a boundary or an event subprocess only.
type deployedPointView struct {
	ProcessID     string `json:"processId"`
	ProcessName   string `json:"processName,omitempty"`
	Version       int32  `json:"version"`
	Newest        bool   `json:"newest"`
	DefinitionKey uint64 `json:"definitionKey"`
	ProjectID     string `json:"projectId,omitempty"`
	System        bool   `json:"system,omitempty"`
	Inactive      bool   `json:"inactive,omitempty"`
	ElementID     string `json:"elementId"`
	Role          string `json:"role"`
	Interrupting  *bool  `json:"interrupting,omitempty"`
}

// signalNameView is one signal name with every point that throws or receives it, and
// what the newest versions say about it.
type signalNameView struct {
	Name     string              `json:"name"`
	Points   []deployedPointView `json:"points"`
	Findings []modelEventFinding `json:"findings"`
}

// signalsResp is the inventory as GET /api/v1/signals answers it.
type signalsResp struct {
	Signals []signalNameView `json:"signals"`
}

// handleListSignals answers the inventory of the signals the deployed models throw
// and receive (ADR-0447): grouped by name, with each name's findings.
func (s *Server) handleListSignals(w http.ResponseWriter, r *http.Request) {
	p := httpapi.PrincipalFrom(r.Context())
	var defs []deployedDefinition
	s.do(func() { defs = s.readableDefinitions(p) })
	// Grouped by the name exactly as the engine matches it: "order " and "order" are
	// two names that never meet, which is a finding, not one name.
	byName := map[string][]deployedPointView{}
	for _, d := range defs {
		for _, sp := range d.cp.SignalPoints() {
			if !signalListed(sp, d.system) {
				continue
			}
			v := deployedPointView{
				ProcessID: d.processID, ProcessName: d.name, Version: d.version,
				Newest: d.newest, DefinitionKey: d.key, ProjectID: d.projectID,
				System: d.system, Inactive: d.inactive, ElementID: sp.Element, Role: string(sp.Role),
			}
			if sp.Role == compiler.SignalBoundary || sp.Role == compiler.SignalEventSubProcess {
				interrupting := sp.Interrupting
				v.Interrupting = &interrupting
			}
			byName[sp.SignalName] = append(byName[sp.SignalName], v)
		}
	}
	out := signalsResp{Signals: []signalNameView{}}
	for name, points := range byName {
		sort.Slice(points, func(i, j int) bool { return pointBefore(points[i], points[j]) })
		out.Signals = append(out.Signals, signalNameView{Name: name, Points: points, Findings: signalFindings(name, points)})
	}
	sort.Slice(out.Signals, func(i, j int) bool { return out.Signals[i].Name < out.Signals[j].Name })
	httpapi.JSON(w, http.StatusOK, out)
}

// signalListed says whether a point belongs to the inventory (ADR-0447 §2): every
// point of a name outside atlas.*, and of an atlas.* name only a throw by a model
// that is not atlas's own — the installation's act, which the finding
// signal.reserved-name names. A receiver of an atlas.* name stays in the
// administrator's listener view of ADR-0435 §7, which is where personal data's flow
// is shown, and a throw by a system process is the catalogue's.
func signalListed(sp compiler.SignalPoint, system bool) bool {
	if strings.TrimSpace(sp.SignalName) == "" {
		return false
	}
	if !eventcatalog.IsAtlasName(sp.SignalName) {
		return true
	}
	return !sp.Receives() && !system
}

// pointBefore orders the points of one name: by process, newest version first, then
// by element.
func pointBefore(a, b deployedPointView) bool {
	if a.ProcessID != b.ProcessID {
		return a.ProcessID < b.ProcessID
	}
	if a.Version != b.Version {
		return a.Version > b.Version
	}
	return a.ElementID < b.ElementID
}

// signalFindings is what the deployed definitions say about one signal name
// (ADR-0447 §4). It reads the newest version of each process, except where the
// question is whether any deployed definition throws the name at all.
func signalFindings(name string, points []deployedPointView) []modelEventFinding {
	out := []modelEventFinding{}
	var throwers, receivers, activeReceivers []deployedPointView
	thrownAnywhere := false
	for _, p := range points {
		throws := p.Role == string(compiler.SignalThrows)
		thrownAnywhere = thrownAnywhere || throws
		if !p.Newest {
			continue
		}
		switch {
		case throws:
			throwers = append(throwers, p)
		default:
			receivers = append(receivers, p)
			if !p.Inactive {
				activeReceivers = append(activeReceivers, p)
			}
		}
	}
	if eventcatalog.IsAtlasName(name) {
		// Only a throw by a model outside atlas's own is listed under such a name.
		if len(throwers) > 0 {
			out = append(out, modelEventFinding{Rule: findingSignalReservedName, Message: fmt.Sprintf(
				"%s throws %s, a name of the events atlas emits, from outside atlas's system processes. "+
					"It speaks for atlas: every model that listens to the name receives it.", definitionsText(throwers), name)})
		}
		return out
	}
	if len(throwers) > 0 && len(activeReceivers) == 0 {
		out = append(out, modelEventFinding{Rule: findingSignalUnreceived, Message: fmt.Sprintf(
			"Thrown by %s, and no newest, active definition receives it. A signal is not buffered: every throw is lost.",
			definitionsText(throwers))})
	}
	if len(receivers) > 0 && !thrownAnywhere {
		out = append(out, modelEventFinding{Rule: findingSignalUnthrown, Message: fmt.Sprintf(
			"Received by %s, and no deployed definition throws it. No route broadcasts a signal: "+
				"the receivers wait until a model that throws it is deployed.", definitionsText(receivers))})
	}
	if from, to, interrupting := crossings(throwers, receivers); len(to) > 0 {
		msg := fmt.Sprintf("Thrown in %s and received in %s. Every variable of the throwing instance is written into "+
			"the receiving instance, whose project's members may read it.", projectsText(from), projectsText(to))
		if len(interrupting) > 0 {
			msg += fmt.Sprintf(" An interrupting receiver there (%s) cancels the work it guards.", definitionsText(interrupting))
		}
		out = append(out, modelEventFinding{Rule: findingSignalCrossesProjects, Message: msg})
	}
	return out
}

// crossings is where a name crosses a project line: the projects of the senders
// whose name is received in another project, the projects that receive it from
// another, and the interrupting receivers among those. A definition without a project
// is a project of its own here: what it receives from a project has left that
// project all the same.
func crossings(senders, receivers []deployedPointView) (from, to []string, interrupting []deployedPointView) {
	fromSet, toSet := map[string]bool{}, map[string]bool{}
	for _, rcv := range receivers {
		crossed := false
		for _, snd := range senders {
			if snd.ProjectID != rcv.ProjectID {
				fromSet[snd.ProjectID], crossed = true, true
			}
		}
		if !crossed {
			continue
		}
		toSet[rcv.ProjectID] = true
		if rcv.Interrupting != nil && *rcv.Interrupting {
			interrupting = append(interrupting, rcv)
		}
	}
	return projectSet(fromSet), projectSet(toSet), interrupting
}

// projectSet is a set of project ids, sorted.
func projectSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// projectsText names projects for a sentence; "" is a definition without one.
func projectsText(ids []string) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		switch id {
		case "":
			parts = append(parts, "a definition without a project")
		case systemProjectID:
			parts = append(parts, "atlas's system project")
		default:
			parts = append(parts, "project "+id)
		}
	}
	return joinAnd(parts)
}

// definitionsText names the definitions of some points for a sentence, each once:
// "billing v3 and hr-notify v2".
func definitionsText(points []deployedPointView) string {
	seen := map[string]bool{}
	var parts []string
	for _, p := range points {
		label := fmt.Sprintf("%s v%d", p.ProcessID, p.Version)
		if !seen[label] {
			seen[label] = true
			parts = append(parts, label)
		}
	}
	sort.Strings(parts)
	return joinAnd(parts)
}

func joinAnd(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

// messageNameView is one message name with every source of it — the processes that
// wait for it and send it, and the senders that are not models — and what they say
// about it (ADR-0446 §2).
type messageNameView struct {
	Name     string              `json:"name"`
	Sources  []messageSourceView `json:"sources"`
	Findings []modelEventFinding `json:"findings"`
}

// messageNamesResp is the grouped view as GET /api/v1/message-names answers it.
type messageNamesResp struct {
	Messages []messageNameView `json:"messages"`
}

// handleListMessageNames answers the rows of message-sources grouped by name, with
// each name's findings. It reads the one function message-sources reads, under the
// same filters, so the flat listing and this one never disagree (ADR-0446 §2).
func (s *Server) handleListMessageNames(w http.ResponseWriter, r *http.Request) {
	rows, err := s.messageSources(r)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list message names: "+err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, messageNamesResp{Messages: groupMessageSources(rows)})
}

// groupMessageSources groups rows already in message-sources' order by name, keeping
// that order within a name. The name is the one the engine correlates on, exactly: a
// name that differs by a space is another name.
func groupMessageSources(rows []messageSourceView) []messageNameView {
	out := []messageNameView{}
	at := map[string]int{}
	for _, row := range rows {
		name := row.MessageName
		if strings.TrimSpace(name) == "" {
			continue
		}
		i, ok := at[name]
		if !ok {
			i = len(out)
			at[name] = i
			out = append(out, messageNameView{Name: name})
		}
		out[i].Sources = append(out[i].Sources, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	for i := range out {
		out[i].Findings = messageFindings(out[i].Name, out[i].Sources)
	}
	return out
}

// messageFindings is what the sources of one message name say about it (ADR-0446 §3).
// It reads the newest version of each process. It never says that nothing sends a
// name: the publish route can send any name, and no definition shows its callers.
func messageFindings(name string, rows []messageSourceView) []modelEventFinding {
	out := []modelEventFinding{}
	var throws, waits []deployedPointView // the process rows of the newest versions
	var otherSenders []string             // enabled watches and active product actions
	activeWaits := 0
	for _, r := range rows {
		switch r.SourceKind {
		case sourceInboundWatch:
			if r.Enabled {
				otherSenders = append(otherSenders, fmt.Sprintf("the inbound watch of worker %s", firstNonEmpty(r.ConnectorName, r.ConnectorID)))
			}
		case sourceProductAction:
			if r.Enabled {
				otherSenders = append(otherSenders, fmt.Sprintf("action %s of product %s", r.Action, firstNonEmpty(r.ProductName, r.ProductID)))
			}
		case sourceWaitingProcess, sourceProcessThrow:
			if !r.Newest {
				continue
			}
			p := deployedPointView{ProcessID: r.ProcessID, Version: r.Version, ProjectID: r.ProjectID,
				System: r.System, Inactive: !r.Enabled, ElementID: r.ElementID, Role: r.Role, Interrupting: r.Interrupting}
			if r.SourceKind == sourceProcessThrow {
				throws = append(throws, p)
				continue
			}
			waits = append(waits, p)
			if r.Enabled {
				activeWaits++
			}
		}
	}
	var own []deployedPointView
	for _, t := range throws {
		if !t.System {
			own = append(own, t)
		}
	}
	if eventcatalog.IsAtlasName(name) && len(own) > 0 {
		out = append(out, modelEventFinding{Rule: findingMessageReservedName, Message: fmt.Sprintf(
			"%s sends %s, a name of the events atlas emits, from outside atlas's system processes. "+
				"It speaks for atlas: a system process waiting for the name receives it.", definitionsText(own), name)})
	}
	if activeWaits == 0 && (len(throws) > 0 || len(otherSenders) > 0) {
		senders := otherSenders
		if len(throws) > 0 {
			senders = append([]string{definitionsText(throws)}, senders...)
		}
		out = append(out, modelEventFinding{Rule: findingMessageUnreceived, Message: fmt.Sprintf(
			"Sent by %s, and no newest, active definition waits for it. What it sends is lost: a message is kept "+
				"for a receiver only when it is published through POST /api/v1/messages with a timeToLiveMs.",
			joinAnd(senders))})
	}
	if from, to, _ := crossings(throws, waits); len(to) > 0 {
		out = append(out, modelEventFinding{Rule: findingMessageCrossesProjects, Message: fmt.Sprintf(
			"Sent in %s and waited for in %s. Every variable of the sending instance is written into the receiving "+
				"instance, whose project's members may read it; the correlation key decides which instance, not what "+
				"it receives.", projectsText(from), projectsText(to))})
	}
	return out
}
