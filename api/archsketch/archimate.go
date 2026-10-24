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

// ArchiMate renders the snapshot as an ArchiMate Model Exchange document.
func ArchiMate(s Snapshot, m Meta) []byte {
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
	b.WriteString("No diagram: the importing tool's own layout is the better one.\n\nNot shown:\n")
	for _, gap := range s.NotShown() {
		b.WriteString("  • " + gap + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// view names what the document holds, the way the image's stamp does.
func view(s Snapshot) string {
	if p := s.Physical; p != nil {
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
