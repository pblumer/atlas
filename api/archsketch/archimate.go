package archsketch

import (
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
}

// ArchiMate renders the snapshot as an ArchiMate Model Exchange document. A value
// [Snapshot.Select] narrowed to the technology layer is written as the installation
// alone.
func ArchiMate(s Snapshot, m Meta) []byte {
	if s.technologyOnly() {
		elements, relationships := technologyGraph(s, m)
		return exchangeDocument(s, m, elements, relationships)
	}
	elements, relationships := applicationGraph(s, m)
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
		relationships = append(relationships, archRelationship{typ, source, target})
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
		relationships = append(relationships, archRelationship{typ, source, target})
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
		fmt.Fprintf(&out, "\n    <relationship identifier=%q source=%q target=%q xsi:type=%q/>",
			fmt.Sprintf("id-rel-%d", i+1), r.source, r.target, r.typ)
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
