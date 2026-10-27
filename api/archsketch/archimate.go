package archsketch

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/pblumer/atlas/api/panorama"
)

// Writing a snapshot as an ArchiMate Open Exchange document (ADR-0471 §3).
//
// The document is generated, never authored, and it says so the way the landscape's
// export does (ADR-0211 §8): its documentation opens with the sentence, every element
// carries atlas.* provenance, and the same snapshot produces the same bytes, so the
// file can be committed and diffed and a change in it is a change in the release.
//
// The mapping, at [MappingVersion]:
//
//   - the Atlas installation is a Node composed of System Software "Atlas";
//   - the release is an Artifact the Node is assigned to, realising the application's
//     Application Component;
//   - Atlas's API is a Technology Interface composed in the Node, serving the
//     component, and each message the application receives is an Application
//     Interface composed in the component;
//   - each worker the application depends on is an Application Service serving the
//     component — "the mail worker serves the invoice application", which is the
//     statement ADR-0211 already makes for a worker;
//   - a call into another application's process is that application's Application
//     Component serving this one;
//   - a job type is an Application Service serving the component, bound by the
//     panorama binding key atlas.jobType.
//
// And with a physical part:
//
//   - each public start link a process had is an Application Interface;
//   - a worker run by a worker process is System Software realising its Application
//     Service, joined to the Atlas node by a Path — the lease, opened by the worker;
//   - a target system is a Node, joined by a Path to whichever side opens the
//     connection: the worker process, or the Atlas node itself for a Worker Type the
//     engine runs. Each Path carries atlas.connection.initiator and the endpoint's
//     scheme, host and port.
//
// Path is ArchiMate 3.2 and outside Panorama's authoring subset; an imported document
// round-trips it (ADR-0189 §2).
//
// Every relationship is one Panorama's own authoring rules accept
// (panorama.MayConnect), so a document exported here and imported into Panorama is
// valid there too. Local decisions are not written: they are evaluated inside the
// engine and are not part of the application's boundary, and the documentation counts
// them instead.

type archElement struct {
	id, typ, name, doc string
	props              map[string]string
}

type archRelationship struct {
	typ, source, target string
	// access is an Access relationship's accessType: Read, Write, ReadWrite or Access.
	access string
}

// ArchiMate renders the snapshot as an ArchiMate Model Exchange document. A value
// [Snapshot.Select] narrowed to the technology layer is written as the installation
// alone.
//
// A value whose selection holds the data layer also carries the data layer's
// elements, joined to the application component when the application layer is shown.
func ArchiMate(s Snapshot, m Meta) []byte {
	s = s.selected()
	var elements []archElement
	var relationships []archRelationship
	app := ""
	if s.technologyOnly() {
		elements, relationships = technologyGraph(s, m)
	} else {
		elements, relationships = applicationGraph(s, m)
		app = "id-application"
	}
	if s.Strategy != nil || s.Business != nil || s.Data != nil {
		g := &layerGraph{s: s, elements: elements, relationships: relationships, app: app, processes: map[string]string{}}
		if s.Strategy != nil {
			g.strategy()
		}
		if s.Business != nil {
			g.business()
		}
		if s.Data != nil {
			g.data()
		}
		elements, relationships = g.elements, g.relationships
	}
	return exchangeDocument(s, m, elements, relationships)
}

// applicationGraph is the application on its node, with everything it offers and
// depends on, and with a physical part, how each connection runs.
func applicationGraph(s Snapshot, m Meta) ([]archElement, []archRelationship) {
	var elements []archElement
	var relationships []archRelationship
	add := func(e archElement) string {
		elements = append(elements, e)
		return e.id
	}
	relate := func(typ, source, target string) {
		relationships = append(relationships, archRelationship{typ: typ, source: source, target: target})
	}

	node := add(archElement{id: "id-atlas-node", typ: "Node", name: "Atlas",
		doc:   "The Atlas server the release was recorded on.",
		props: map[string]string{"atlas.derivedKind": "node"}})
	if p := s.Physical; p != nil {
		elements[len(elements)-1].name = "Atlas — " + installationName(*p)
		elements[len(elements)-1].props["atlas.node"] = p.Node
		elements[len(elements)-1].props["atlas.environment"] = p.Environment
	}
	software := add(archElement{id: "id-atlas-software", typ: "SystemSoftware", name: "Atlas",
		doc:   "The Atlas workflow engine.",
		props: map[string]string{"atlas.derivedKind": "systemSoftware"}})
	relate("Composition", node, software)

	app := add(archElement{id: "id-application", typ: "ApplicationComponent", name: m.Application,
		doc: "The process application, as release v" + fmt.Sprint(m.Version) + " shipped it.",
		props: map[string]string{
			"atlas.derivedKind":       "application",
			panorama.KeyApplicationID: m.ApplicationID,
		}})
	release := add(archElement{id: "id-release", typ: "Artifact", name: fmt.Sprintf("%s v%d", m.Application, m.Version),
		doc: "The release: what was published together.",
		props: map[string]string{
			"atlas.derivedKind":   "release",
			panorama.KeyReleaseID: m.ReleaseID,
		}})
	relate("Assignment", node, release)
	relate("Realization", release, app)

	apiProps := map[string]string{"atlas.derivedKind": "api"}
	if p := s.Physical; p != nil && p.ExternalURL != nil {
		endpointProps(apiProps, *p.ExternalURL)
	}
	api := add(archElement{id: "id-atlas-api", typ: "TechnologyInterface", name: "Atlas API",
		doc:   "The HTTP API every way into the application goes through: starting instances, publishing messages, completing tasks.",
		props: apiProps})
	relate("Composition", node, api)
	relate("Serving", api, app)

	entryIDs := map[string]string{}
	for i, e := range s.EntryPoints {
		effect := "Continues a running instance"
		if e.Starts {
			effect = "Starts an instance"
		}
		id := add(archElement{id: fmt.Sprintf("id-entry-%d", i+1), typ: "ApplicationInterface", name: "Message " + e.Name,
			doc: effect + ". Received at " + usesText(e.UsedBy) + ".",
			props: map[string]string{
				"atlas.derivedKind": "entryPoint",
				"atlas.messageName": e.Name,
			}})
		relate("Composition", app, id)
		entryIDs[e.Name] = id
	}
	if p := s.Physical; p != nil {
		for i, l := range p.PublicStartLinks {
			id := add(archElement{id: fmt.Sprintf("id-startlink-%d", i+1), typ: "ApplicationInterface", name: "Public start form " + l.ProcessID,
				doc: "Public start links of process " + l.ProcessID + " on the recording installation: " + plural(l.Links, "link", "links") + ". Whoever holds one may start an instance.",
				props: map[string]string{
					"atlas.derivedKind":      "publicStartLink",
					panorama.KeyProcessID:    l.ProcessID,
					"atlas.publicStartLinks": fmt.Sprint(l.Links),
				}})
			relate("Composition", app, id)
		}
	}

	initiators := map[int][2]string{}
	for i, d := range s.Outbound() {
		var e archElement
		switch d.Kind {
		case DependencyWorker:
			e = archElement{typ: "ApplicationService", name: d.Name,
				doc: "A worker of type " + workerTypeName(d.WorkerType) + " the application hands work to. Used at " + usesText(d.UsedBy) + ".",
				props: map[string]string{
					"atlas.derivedKind": "worker",
					"atlas.workerName":  d.Name,
					"atlas.workerType":  d.WorkerType,
				}}
			if site := s.Physical.worker(d.Name, d.WorkerType); site != nil {
				workerDetailProps(e.props, *site)
			}
		case DependencyRuntimeWorker:
			e = archElement{typ: "ApplicationService", name: "Worker decided at run time",
				doc: "A worker of type " + workerTypeName(d.WorkerType) + " whose name the model computes per instance (" + d.Name + "). Used at " + usesText(d.UsedBy) + ".",
				props: map[string]string{
					"atlas.derivedKind":      "runtimeWorker",
					"atlas.workerExpression": d.Name,
					"atlas.workerType":       d.WorkerType,
				}}
		case DependencyJobType:
			e = archElement{typ: "ApplicationService", name: "Job type " + d.Name,
				doc: "Work any worker subscribed to job type " + d.Name + " leases from the Atlas API. Used at " + usesText(d.UsedBy) + ".",
				props: map[string]string{
					"atlas.derivedKind": "jobType",
					panorama.KeyJobType: d.Name,
				}}
			if site := s.Physical.jobType(d.Name); site != nil {
				e.doc += " On the recording installation: " + site.Observers.text() + "."
			}
		case DependencyCall:
			name := "Process " + d.Name
			props := map[string]string{"atlas.derivedKind": "call", panorama.KeyProcessID: d.Name}
			doc := "The application holding process " + d.Name + ", which this application calls. Called at " + usesText(d.UsedBy) + "."
			if d.TargetApplication != "" {
				name = d.TargetApplication
				props[panorama.KeyApplicationID] = d.TargetApplicationID
			} else {
				doc += " Nothing on the recording installation held that process when the snapshot was taken."
			}
			e = archElement{typ: "ApplicationComponent", name: name, doc: doc, props: props}
		default:
			continue
		}
		e.id = fmt.Sprintf("id-dependency-%d", i+1)
		service := add(e)
		relate("Serving", service, app)

		site := s.Physical.worker(d.Name, d.WorkerType)
		if d.Kind != DependencyWorker || site == nil {
			continue
		}
		// Who opens the connection to the target: the worker process, joined to Atlas
		// by its lease, or the Atlas node itself.
		initiator, initiatorKind := node, "atlas"
		if site.Placement == PlacementWorker {
			initiatorKind = "worker"
			initiator = add(archElement{id: fmt.Sprintf("id-worker-%d", i+1), typ: "SystemSoftware", name: "Worker for " + d.Name,
				doc: "The worker process that serves " + d.Name + ": it leases the work from the Atlas API and connects to the target system. On the recording installation: " + site.Observers.text() + ".",
				props: map[string]string{
					"atlas.derivedKind": "workerProcess",
					"atlas.workerName":  d.Name,
					"atlas.observedBy":  strings.Join(site.ObservedBy, ", "),
				}})
			relate("Realization", initiator, service)
			lease := add(archElement{id: fmt.Sprintf("id-lease-%d", i+1), typ: "Path", name: "Lease of " + d.Name,
				doc: "The worker opens this connection to the Atlas API to lease its jobs.",
				props: map[string]string{
					"atlas.derivedKind":          "lease",
					"atlas.connection.initiator": "worker",
				}})
			relate("Association", lease, initiator)
			relate("Association", lease, node)
		}
		initiators[i] = [2]string{initiator, initiatorKind}
		if site.Endpoint == nil || site.Endpoint.Withheld || site.Endpoint.Unparseable {
			if site.Endpoint != nil {
				elements[len(elements)-1].doc += " Its target endpoint is not in this document: " + site.Endpoint.String() + "."
			}
			continue
		}
		targetProps := map[string]string{"atlas.derivedKind": "targetSystem"}
		endpointProps(targetProps, *site.Endpoint)
		source := "as its configuration on the recording installation names it"
		if site.EndpointReported {
			source = "as the worker instances serving it reported it"
		}
		target := add(archElement{id: fmt.Sprintf("id-target-%d", i+1), typ: "Node", name: site.Endpoint.Host,
			doc: "The system worker " + d.Name + " connects to, " + source + ".", props: targetProps})
		pathProps := map[string]string{"atlas.derivedKind": "connection", "atlas.connection.initiator": initiatorKind}
		endpointProps(pathProps, *site.Endpoint)
		path := add(archElement{id: fmt.Sprintf("id-connection-%d", i+1), typ: "Path", name: site.Endpoint.String(),
			doc: "Opened by the " + map[string]string{"atlas": "Atlas server", "worker": "worker process"}[initiatorKind] + ".", props: pathProps})
		relate("Association", path, initiator)
		relate("Association", path, target)
	}

	// From mapping version 4: the further systems a worker reaches — its mailbox and
	// its malware scanner — and the inbound subscriptions that feed a way in.
	if s.MappingVersion >= 4 {
		for i, d := range s.Outbound() {
			site, ends := s.Physical.worker(d.Name, d.WorkerType), initiators[i]
			if d.Kind != DependencyWorker || site == nil || ends[0] == "" {
				continue
			}
			further(add, relate, fmt.Sprintf("%d", i+1), d.Name, *site, ends[0], ends[1])
		}
		for j, in := range s.Physical.inboundSubscriptions() {
			path := inboundPath(add, relate, j+1, in, node)
			if entry := entryIDs[in.MessageName]; entry != "" {
				relate("Association", path, entry)
			}
		}
	}
	return elements, relationships
}

// technologyGraph is the installation alone: the Atlas node and its system software,
// the release deployed on it, its API, and each worker process and each connection
// Atlas opens itself, with the system it reaches.
func technologyGraph(s Snapshot, m Meta) ([]archElement, []archRelationship) {
	var elements []archElement
	var relationships []archRelationship
	add := func(e archElement) string {
		elements = append(elements, e)
		return e.id
	}
	relate := func(typ, source, target string) {
		relationships = append(relationships, archRelationship{typ: typ, source: source, target: target})
	}
	p := *s.Physical

	node := add(archElement{id: "id-atlas-node", typ: "Node", name: "Atlas — " + installationName(p),
		doc:   "The Atlas server the release was recorded on.",
		props: map[string]string{"atlas.derivedKind": "node", "atlas.node": p.Node, "atlas.environment": p.Environment}})
	software := add(archElement{id: "id-atlas-software", typ: "SystemSoftware", name: "Atlas",
		doc:   "The Atlas workflow engine.",
		props: map[string]string{"atlas.derivedKind": "systemSoftware"}})
	relate("Composition", node, software)
	release := add(archElement{id: "id-release", typ: "Artifact", name: fmt.Sprintf("%s v%d", m.Application, m.Version),
		doc: "The release: what was published together.",
		props: map[string]string{
			"atlas.derivedKind":   "release",
			panorama.KeyReleaseID: m.ReleaseID,
		}})
	relate("Assignment", node, release)
	apiProps := map[string]string{"atlas.derivedKind": "api"}
	if p.ExternalURL != nil {
		endpointProps(apiProps, *p.ExternalURL)
	}
	api := add(archElement{id: "id-atlas-api", typ: "TechnologyInterface", name: "Atlas API",
		doc:   "The HTTP API every way into the application goes through, and the one worker processes lease their work from.",
		props: apiProps})
	relate("Composition", node, api)

	for i, w := range p.Workers {
		initiator, initiatorKind := node, "atlas"
		if w.Placement == PlacementWorker {
			initiatorKind = "worker"
			props := map[string]string{
				"atlas.derivedKind": "workerProcess",
				"atlas.workerName":  w.Name,
				"atlas.workerType":  w.WorkerType,
				"atlas.observedBy":  strings.Join(w.ObservedBy, ", "),
			}
			workerDetailProps(props, w)
			initiator = add(archElement{id: fmt.Sprintf("id-worker-%d", i+1), typ: "SystemSoftware", name: "Worker for " + w.Name,
				doc:   "The worker process that serves " + w.Name + " (" + workerTypeName(w.WorkerType) + "): it leases the work from the Atlas API and connects to the target system. On the recording installation: " + w.Observers.text() + ".",
				props: props})
			lease := add(archElement{id: fmt.Sprintf("id-lease-%d", i+1), typ: "Path", name: "Lease of " + w.Name,
				doc: "The worker opens this connection to the Atlas API to lease its jobs.",
				props: map[string]string{
					"atlas.derivedKind":          "lease",
					"atlas.connection.initiator": "worker",
				}})
			relate("Association", lease, initiator)
			relate("Association", lease, node)
		}
		if s.MappingVersion >= 4 {
			further(add, relate, fmt.Sprintf("%d", i+1), w.Name, w, initiator, initiatorKind)
		}
		if w.Endpoint == nil || w.Endpoint.Withheld || w.Endpoint.Unparseable {
			if w.Endpoint != nil {
				elements[len(elements)-1].doc += " The target endpoint of " + w.Name + " is not in this document: " + w.Endpoint.String() + "."
			}
			continue
		}
		targetProps := map[string]string{"atlas.derivedKind": "targetSystem"}
		endpointProps(targetProps, *w.Endpoint)
		source := "as its configuration on the recording installation names it"
		if w.EndpointReported {
			source = "as the worker instances serving it reported it"
		}
		target := add(archElement{id: fmt.Sprintf("id-target-%d", i+1), typ: "Node", name: w.Endpoint.Host,
			doc: "The system worker " + w.Name + " connects to, " + source + ".", props: targetProps})
		pathProps := map[string]string{"atlas.derivedKind": "connection", "atlas.connection.initiator": initiatorKind}
		endpointProps(pathProps, *w.Endpoint)
		if w.Placement != PlacementWorker {
			workerDetailProps(pathProps, w)
		}
		path := add(archElement{id: fmt.Sprintf("id-connection-%d", i+1), typ: "Path", name: w.Endpoint.String(),
			doc: "Opened by the " + map[string]string{"atlas": "Atlas server", "worker": "worker process"}[initiatorKind] + ".", props: pathProps})
		relate("Association", path, initiator)
		relate("Association", path, target)
	}
	for i, j := range p.JobTypes {
		workers := add(archElement{id: fmt.Sprintf("id-jobtype-workers-%d", i+1), typ: "SystemSoftware", name: "Workers of job type " + j.JobType,
			doc: "The worker processes subscribed to job type " + j.JobType + ": they lease its work from the Atlas API. On the recording installation: " + j.Observers.text() + ".",
			props: map[string]string{
				"atlas.derivedKind": "jobTypeWorkers",
				panorama.KeyJobType: j.JobType,
				"atlas.observedBy":  strings.Join(j.ObservedBy, ", "),
			}})
		lease := add(archElement{id: fmt.Sprintf("id-jobtype-lease-%d", i+1), typ: "Path", name: "Lease of job type " + j.JobType,
			doc: "Each subscribed worker opens this connection to the Atlas API to lease its jobs.",
			props: map[string]string{
				"atlas.derivedKind":          "lease",
				"atlas.connection.initiator": "worker",
			}})
		relate("Association", lease, workers)
		relate("Association", lease, node)
	}
	for j, in := range p.inboundSubscriptions() {
		inboundPath(add, relate, j+1, in, node)
	}
	return elements, relationships
}

// workerDetailProps writes what else a Worker's configuration says as properties
// (mapping version 4). An empty value is not written, so a snapshot from before
// version 4 produces the document it always did.
func workerDetailProps(props map[string]string, w WorkerSite) {
	if w.Identity != nil {
		props["atlas.identity.mode"] = w.Identity.Mode
		props["atlas.identity.enforcement"] = w.Identity.Enforcement
	}
	if w.Credential {
		props["atlas.credential.configured"] = "true"
	}
	props["atlas.agent.model"] = w.Model
	props["atlas.mail.provider"] = w.Provider
	if w.DetailWithheld {
		props["atlas.configuration.withheld"] = "true"
	}
}

// further adds the systems a worker reaches beyond its target — its mailbox and its
// malware scanner — each a Node joined by a Path to whichever side opens the
// connection. Withheld and unreadable endpoints are left out: there is no system to
// name.
func further(add func(archElement) string, relate func(typ, source, target string), suffix, name string, w WorkerSite, initiator, initiatorKind string) {
	for _, f := range []struct {
		kind, what string
		e          *Endpoint
	}{
		{"mailbox", "the mailbox it reads", w.MailboxEndpoint},
		{"scanner", "the malware scanner it hands messages to", w.ScannerEndpoint},
	} {
		if f.e == nil || f.e.Withheld || f.e.Unparseable {
			continue
		}
		props := map[string]string{"atlas.derivedKind": f.kind}
		endpointProps(props, *f.e)
		target := add(archElement{id: "id-" + f.kind + "-" + suffix, typ: "Node", name: f.e.Host,
			doc: "The system " + name + " reaches as " + f.what + ".", props: props})
		pathProps := map[string]string{"atlas.derivedKind": "connection", "atlas.connection.initiator": initiatorKind}
		endpointProps(pathProps, *f.e)
		path := add(archElement{id: "id-" + f.kind + "-connection-" + suffix, typ: "Path", name: f.e.String(),
			doc: "Opened by the " + map[string]string{"atlas": "Atlas server", "worker": "worker process"}[initiatorKind] + ".", props: pathProps})
		relate("Association", path, initiator)
		relate("Association", path, target)
	}
}

// inboundPath adds one inbound subscription: the Path Atlas opens to watch a subject,
// and the system it watches when this reader may know it.
func inboundPath(add func(archElement) string, relate func(typ, source, target string), n int, in InboundSite, node string) string {
	props := map[string]string{
		"atlas.derivedKind":          "inboundSubscription",
		"atlas.messageName":          in.MessageName,
		"atlas.workerName":           in.Worker,
		"atlas.workerType":           in.WorkerType,
		"atlas.inbound.subject":      in.Subject,
		"atlas.connection.initiator": "atlas",
	}
	if in.Withheld {
		props["atlas.configuration.withheld"] = "true"
	}
	doc := "Atlas watches " + in.sourceText() + " and publishes each new event as message " + in.MessageName + "."
	reachable := in.Endpoint != nil && !in.Endpoint.Withheld && !in.Endpoint.Unparseable
	if reachable {
		endpointProps(props, *in.Endpoint)
	}
	path := add(archElement{id: fmt.Sprintf("id-inbound-%d", n), typ: "Path", name: "Inbound subscription " + in.sourceText(), doc: doc, props: props})
	relate("Association", path, node)
	if reachable {
		source := add(archElement{id: fmt.Sprintf("id-inbound-source-%d", n), typ: "Node", name: in.Endpoint.Host,
			doc: "The system whose events become message " + in.MessageName + ".", props: map[string]string{"atlas.derivedKind": "inboundSource"}})
		relate("Association", path, source)
	}
	return path
}

// exchangeDocument writes elements and relationships as the Model Exchange document,
// with the provenance that keeps it from being mistaken for one somebody drew.
func exchangeDocument(s Snapshot, m Meta, elements []archElement, relationships []archRelationship) []byte {
	var out strings.Builder
	out.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	out.WriteString(`<model xmlns="` + panorama.ExchangeNamespace + `"` +
		` xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"` +
		` xsi:schemaLocation="` + panorama.ExchangeNamespace + " " + panorama.ExchangeNamespace + `archimate3_Model.xsd"` +
		` identifier="id-atlas-release-sketch">` + "\n")
	out.WriteString(`  <name xml:lang="en">` + esc(title(m)) + "</name>\n")
	out.WriteString(`  <documentation xml:lang="en">` + esc(archiProvenance(s, m)) + "</documentation>\n")

	used := map[string]bool{}
	out.WriteString("  <elements>")
	for _, e := range elements {
		fmt.Fprintf(&out, "\n    <element identifier=%q xsi:type=%q>", e.id, e.typ)
		out.WriteString("\n      <name xml:lang=\"en\">" + esc(e.name) + "</name>")
		if e.doc != "" {
			out.WriteString("\n      <documentation xml:lang=\"en\">" + esc(e.doc) + "</documentation>")
		}
		keys := sortedKeys(e.props)
		if len(keys) > 0 {
			out.WriteString("\n      <properties>")
			for _, k := range keys {
				used[k] = true
				fmt.Fprintf(&out, "\n        <property propertyDefinitionRef=%q><value xml:lang=\"en\">%s</value></property>",
					propertyDefinitionID(k), esc(e.props[k]))
			}
			out.WriteString("\n      </properties>")
		}
		out.WriteString("\n    </element>")
	}
	out.WriteString("\n  </elements>\n")

	out.WriteString("  <relationships>")
	for i, r := range relationships {
		access := ""
		if r.access != "" {
			access = fmt.Sprintf(" accessType=%q", r.access)
		}
		fmt.Fprintf(&out, "\n    <relationship identifier=%q source=%q target=%q xsi:type=%q%s/>",
			fmt.Sprintf("id-rel-%d", i+1), r.source, r.target, r.typ, access)
	}
	out.WriteString("\n  </relationships>\n")

	out.WriteString("  <propertyDefinitions>")
	for _, k := range sortedKeys(used) {
		fmt.Fprintf(&out, "\n    <propertyDefinition identifier=%q type=\"string\"><name>%s</name></propertyDefinition>",
			propertyDefinitionID(k), esc(k))
	}
	out.WriteString("\n  </propertyDefinitions>\n")
	out.WriteString("</model>\n")
	return []byte(out.String())
}

// layerGraph is what the writers of the strategy, business and data layers share: the
// elements and relationships written so far, the application component when the
// application layer is shown, and the Application Process each process became, so the
// layers meet at one element per process.
type layerGraph struct {
	s             Snapshot
	elements      []archElement
	relationships []archRelationship
	app           string
	processes     map[string]string
}

func (g *layerGraph) add(e archElement) string {
	g.elements = append(g.elements, e)
	return e.id
}

func (g *layerGraph) relate(typ, source, target, access string) {
	g.relationships = append(g.relationships, archRelationship{typ: typ, source: source, target: target, access: access})
}

// process is the Application Process of one release process, written the first time a
// layer needs it and assigned from the component when there is one.
func (g *layerGraph) process(pid string) string {
	if id, ok := g.processes[pid]; ok {
		return id
	}
	name, doc := pid, "Process "+pid+" as the release shipped it."
	n := len(g.s.Processes) + len(g.processes) + 1
	for i, p := range g.s.Processes {
		if p.ProcessID == pid {
			n = i + 1
			if p.Name != "" {
				name = p.Name
			}
			doc = fmt.Sprintf("Process %s v%d as the release shipped it.", pid, p.Version)
			break
		}
	}
	id := g.add(archElement{id: fmt.Sprintf("id-process-%d", n), typ: "ApplicationProcess", name: "Process " + name, doc: doc,
		props: map[string]string{"atlas.derivedKind": "process", panorama.KeyProcessID: pid}})
	if g.app != "" {
		g.relate("Assignment", g.app, id, "")
	}
	g.processes[pid] = id
	return id
}

// dependencies maps each dependency the application layer wrote to the identifier it
// gave it; empty when the application layer is not shown.
func (g *layerGraph) dependencies() map[PersonalRecipient]string {
	ids := map[PersonalRecipient]string{}
	if g.app != "" {
		for i, dep := range g.s.Outbound() {
			ids[PersonalRecipient{Kind: dep.Kind, Name: dep.Name, WorkerType: dep.WorkerType}] = fmt.Sprintf("id-dependency-%d", i+1)
		}
	}
	return ids
}

// strategy adds the strategy layer (mapping version 6): each capability the release
// realises as a Capability its Application Process — or, with the application layer,
// its worker's Application Service — realises, and each value stream using one as a
// Value Stream composed of its stages, each stage served by the capabilities it uses.
func (g *layerGraph) strategy() {
	st := g.s.Strategy
	deps := g.dependencies()
	workers := map[string]string{}
	for dep, id := range deps {
		if dep.Kind == DependencyWorker {
			workers[dep.Name] = id
		}
	}
	capabilities := map[string]string{}
	for i, c := range st.Capabilities {
		props := map[string]string{
			"atlas.derivedKind":           "capability",
			"atlas.capability.key":        c.Key,
			"atlas.capability.state":      c.State,
			"atlas.capability.measures":   c.measuresText(),
			"atlas.capability.realizedBy": strings.TrimPrefix(c.realizedText(), "realised by "),
		}
		if c.Owner != nil {
			props["atlas.capability.owner"] = strings.TrimPrefix(c.Owner.text(), "owner ")
		}
		if c.ConfirmedAt > 0 {
			props["atlas.capability.confirmed"] = formatDate(c.ConfirmedAt)
		}
		id := g.add(archElement{id: fmt.Sprintf("id-capability-%d", i+1), typ: "Capability", name: c.Name,
			doc:   "A capability the release realises: " + c.realizedText() + "; " + c.stateText() + "; " + c.Owner.text() + ".",
			props: props})
		capabilities[c.Key] = id
		for _, r := range c.RealizedBy {
			switch {
			case r.Kind == RealizedByProcess:
				g.relate("Realization", g.process(r.ProcessID), id, "")
			case workers[r.Worker] != "":
				g.relate("Realization", workers[r.Worker], id, "")
			}
		}
	}
	for i, v := range st.ValueStreams {
		owner := ""
		if v.Owner != nil {
			owner = strings.TrimPrefix(v.Owner.text(), "owner ")
		}
		stream := g.add(archElement{id: fmt.Sprintf("id-valuestream-%d", i+1), typ: "ValueStream", name: v.Name,
			doc:   "A value stream a stage of which uses a capability the release realises: " + v.stagesText() + ".",
			props: map[string]string{"atlas.derivedKind": "valueStream", "atlas.valueStream.key": v.Key, "atlas.valueStream.owner": owner}})
		for j, stage := range v.Stages {
			id := g.add(archElement{id: fmt.Sprintf("id-stage-%d-%d", i+1, j+1), typ: "ValueStream", name: stage.Name,
				doc:   fmt.Sprintf("Stage %d of value stream %s.", j+1, v.Name),
				props: map[string]string{"atlas.derivedKind": "valueStreamStage", "atlas.valueStream.stage": stage.Key}})
			g.relate("Composition", stream, id, "")
			for _, key := range stage.Capabilities {
				if c := capabilities[key]; c != "" {
					g.relate("Serving", c, id, "")
				}
			}
		}
	}
}

// business adds the business layer (mapping version 6): each process as a Business
// Process its Application Process realises, each role as a Business Role assigned to
// the processes that name it, and each product as a Product associated with the
// processes that carry it out. Product is ArchiMate 3.2 and outside Panorama's
// authoring subset, written the way Path is (ADR-0495, decision drivers).
func (g *layerGraph) business() {
	bz := g.s.Business
	bprocesses := map[string]string{}
	for i, p := range bz.Processes {
		id := g.add(archElement{id: fmt.Sprintf("id-bprocess-%d", i+1), typ: "BusinessProcess", name: cmp.Or(p.Name, p.ProcessID),
			doc: "The work process " + p.ProcessID + " does. Its user tasks: " + p.tasksText() + ".",
			props: map[string]string{
				"atlas.derivedKind":   "businessProcess",
				panorama.KeyProcessID: p.ProcessID,
				"atlas.tasks":         p.tasksText(),
			}})
		g.relate("Realization", g.process(p.ProcessID), id, "")
		bprocesses[p.ProcessID] = id
	}
	for i, r := range bz.Roles {
		id := g.add(archElement{id: fmt.Sprintf("id-role-%d", i+1), typ: "BusinessRole", name: r.Name,
			doc:   map[string]string{RoleLane: "A lane", RoleCandidateGroup: "A candidate group"}[r.Kind] + " the release's processes name, as the model writes it: " + r.usesText() + ".",
			props: map[string]string{"atlas.derivedKind": "role", "atlas.role.kind": r.Kind}})
		last := ""
		for _, u := range r.UsedBy {
			if bp := bprocesses[u.ProcessID]; bp != "" && u.ProcessID != last {
				g.relate("Assignment", id, bp, "")
				last = u.ProcessID
			}
		}
	}
	for i, p := range bz.Products {
		actions := make([]string, 0, len(p.Actions))
		for _, a := range p.Actions {
			text := a.Key + " (message " + a.Message
			for _, o := range a.Outcomes {
				text += "; " + o.Outcome + " publishes " + o.EventType
			}
			actions = append(actions, text+")")
		}
		id := g.add(archElement{id: fmt.Sprintf("id-product-%d", i+1), typ: "Product", name: p.Name,
			doc: "A catalogue product a release process carries out: " + p.bindingText() + ".",
			props: map[string]string{
				"atlas.derivedKind":     "product",
				"atlas.product.id":      p.ID,
				"atlas.product.catalog": cmp.Or(p.CatalogName, p.CatalogID),
				"atlas.product.state":   p.State,
				"atlas.product.actions": strings.Join(actions, ", "),
			}})
		last := ""
		for _, b := range p.Bindings {
			if bp := bprocesses[b.ProcessID]; bp != "" && b.ProcessID != last {
				g.relate("Association", id, bp, "")
				last = b.ProcessID
			}
		}
	}
}

// data adds the data layer (mapping version 5): each process that keeps data or
// declares personal data as an Application Process the component is assigned to, each
// data object as a Data Object it accesses — read, written or both, as its
// associations say — realising the Business Object of its class, each store as a Data
// Object associated with the processes that name it, and each personal-data
// declaration as a Data Object every worker the process hands work to reads.
func (g *layerGraph) data() {
	d := g.s.Data
	add, relate, process := g.add, g.relate, g.process

	classes := map[string]string{}
	for i, c := range d.Classes {
		doc := "A class the release keeps data as. The process models declare it as a type; the application's information model does not define it."
		modeled := ""
		if c.Modeled {
			doc, modeled = "A class the release keeps data as, defined in the application's information model.", "true"
		}
		classes[c.Name] = add(archElement{id: fmt.Sprintf("id-class-%d", i+1), typ: "BusinessObject", name: c.Name, doc: doc,
			props: map[string]string{"atlas.derivedKind": "class", "atlas.class.modeled": modeled}})
	}
	for i, o := range d.Objects {
		collection := ""
		if o.Collection {
			collection = "true"
		}
		id := add(archElement{id: fmt.Sprintf("id-dataobject-%d", i+1), typ: "DataObject", name: o.Name,
			doc: "A data object of process " + o.ProcessID + ": " + o.useDetail() + ".",
			props: map[string]string{
				"atlas.derivedKind":           "dataObject",
				panorama.KeyProcessID:         o.ProcessID,
				"atlas.dataObject.class":      o.Class,
				"atlas.dataObject.collection": collection,
				"atlas.dataObject.states":     strings.Join(o.states(), ", "),
			}})
		relate("Access", process(o.ProcessID), id, o.accessType())
		if c := classes[o.Class]; c != "" {
			relate("Realization", id, c, "")
		}
	}
	for i, st := range d.Stores {
		id := add(archElement{id: fmt.Sprintf("id-datastore-%d", i+1), typ: "DataObject", name: st.Name,
			doc: "A data store, where instances of a class outlive the process that made them. Named at " + usesText(st.UsedBy) +
				". Which activity reads or writes it is not in the compiled model.",
			props: map[string]string{"atlas.derivedKind": "dataStore", "atlas.dataStore.class": st.Class}})
		last := "" // UsedBy is sorted by process, so one association per process
		for _, u := range st.UsedBy {
			if u.ProcessID != last {
				relate("Association", process(u.ProcessID), id, "")
				last = u.ProcessID
			}
		}
		if c := classes[st.Class]; c != "" {
			relate("Realization", id, c, "")
		}
	}

	recipients := g.dependencies()
	for i, p := range d.Personal {
		names := make([]string, 0, len(p.HandedTo))
		for _, r := range p.HandedTo {
			names = append(names, r.text())
		}
		id := add(archElement{id: fmt.Sprintf("id-personal-%d", i+1), typ: "DataObject", name: "Personal data of " + p.ProcessID,
			doc: "Process " + p.ProcessID + " declares " + strings.Join(p.Variables, ", ") + " personal, about the person " + p.Subject +
				" names; " + p.handedToText() + ". Only the declaration is recorded, never a value.",
			props: map[string]string{
				"atlas.derivedKind":        "personalData",
				panorama.KeyProcessID:      p.ProcessID,
				"atlas.personal":           "true",
				"atlas.personal.variables": strings.Join(p.Variables, ", "),
				"atlas.personal.subject":   p.Subject,
				"atlas.personal.handedTo":  strings.Join(names, ", "),
			}})
		relate("Association", process(p.ProcessID), id, "")
		for _, r := range p.HandedTo {
			if rid := recipients[r]; rid != "" {
				relate("Access", rid, id, "Read")
			}
		}
	}
}

// archiProvenance is the paragraph that keeps the document from being mistaken for one
// somebody drew, and that lists what it does not carry.
func archiProvenance(s Snapshot, m Meta) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Generated by Atlas from release v%d of %s (release %s), snapshot taken %s. ",
		m.Version, m.Application, m.ReleaseID, formatTime(s.TakenAt))
	fmt.Fprintf(&b, "DERIVED, NOT AUTHORED: nothing in this model was drawn by a person. It is the "+
		"release's %s in ArchiMate's vocabulary at mapping version %d — the same "+
		"snapshot always produces this document, and editing it here changes nothing in Atlas.\n\n", view(s), s.MappingVersion)
	if n := s.LocalDecisions(); n > 0 {
		fmt.Fprintf(&b, "Local decisions evaluated inside the engine (%d) are not written as elements: they do not cross the application's boundary. ", n)
	}
	if n := s.UnreadProcesses(); n > 0 {
		fmt.Fprintf(&b, "Incomplete: %s could not be read when the snapshot was taken, so what they offer and need is missing. ",
			plural(n, "process", "processes"))
	}
	if note := s.layerNote(); note != "" {
		b.WriteString(note + " ")
	}
	b.WriteString("No diagram: the importing tool's own layout is the better one.\n\nNot shown:\n")
	for _, gap := range s.NotShown() {
		b.WriteString("  • " + gap + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// view names what the document holds, the way the image's stamp does.
func view(s Snapshot) string {
	if s.bandsOnly() {
		return s.bandsName() + " architecture"
	}
	if p := s.Physical; p != nil {
		if s.technologyOnly() {
			return "physical architecture on " + installationName(*p)
		}
		return "logical architecture and the physical one of " + installationName(*p)
	}
	return "logical architecture"
}

// endpointProps writes an endpoint's three parts as the descriptive properties a
// reader of the document finds them under.
func endpointProps(props map[string]string, e Endpoint) {
	props["atlas.endpoint.scheme"] = e.Scheme
	props["atlas.endpoint.host"] = e.Host
	props["atlas.endpoint.port"] = e.Port
}

func workerTypeName(t string) string {
	if t == "" {
		return "unknown"
	}
	return fmt.Sprintf("%q", t)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k, v := range m {
		if s, ok := any(v).(string); ok && s == "" {
			// An empty property says nothing and would be written as one.
			continue
		}
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// propertyDefinitionID is stable for a key, the same spelling the landscape's export
// uses, so a property means one definition across both kinds of generated document.
func propertyDefinitionID(key string) string {
	return "propid-" + strings.ReplaceAll(strings.TrimPrefix(key, "atlas."), ".", "-")
}
