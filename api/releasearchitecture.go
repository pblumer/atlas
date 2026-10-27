package api

import (
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/api/archsketch"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/compiler"
)

// A release's architecture sketch (ADR-0471).
//
// The snapshot is taken on the run loop in the same step that saves the release
// record — on publish (mintRelease) and on the import of a promoted release — and it
// rides on the record, so the one atomic write that records a release records its
// snapshot too. A release never exists without one, and nothing here enters the WAL.
//
// The logical part is fixed by the release's content: the messages its processes
// receive, the workers they name, the job types they hand out, the local decisions
// they evaluate and the calls they make into processes the release did not ship. The
// physical part is this installation's, at that moment: its name and environment, the
// address it is configured to be reached under, the public start links its processes
// have, and for each worker name and job type what serves it here (ADR-0471 §1). The
// data part is fixed by the content again, with the class names checked against the
// application's information model at that moment: the data objects, stores and
// personal-data declarations of its processes (ADR-0495 slice C).
//
// What a reader sees of the physical part is decided when it is read, not when it is
// recorded: a worker's endpoint only by a reader who may see that worker's
// configuration (ADR-0205), the worker instances only by a reader who may see the
// Workers view. See redactedArchitecture.

// architectureSnapshot reads the release's process members from their compiled forms.
// Run-loop goroutine only: it reads the deployment registry and the project store.
//
// The members are deployed at this moment by construction — the publish or import
// that records the release has just deployed them — so a member with no compiled form
// is a fault worth saying, and it is recorded as unread rather than drawn as a process
// that offers and needs nothing.
func (s *Server) architectureSnapshot(appID string, members []releaseMember, takenAt int64) *archsketch.Snapshot {
	snap := &archsketch.Snapshot{
		FormatVersion:  archsketch.FormatVersion,
		MappingVersion: archsketch.MappingVersion,
		TakenAt:        takenAt,
		Processes:      []archsketch.Process{},
		EntryPoints:    []archsketch.EntryPoint{},
		Dependencies:   []archsketch.Dependency{},
	}
	shipped := map[string]bool{}
	placement := map[[2]string]string{}
	data := &archsketch.Data{}
	business := &archsketch.Business{}
	for _, m := range members {
		if m.Kind == "process" {
			shipped[m.Ref] = true
		}
	}
	for _, m := range members {
		if m.Kind != "process" {
			continue
		}
		proc := archsketch.Process{ProcessID: m.Ref, Version: m.ArtifactVer}
		d := s.deployments[m.Key]
		if d == nil || d.cp == nil {
			proc.Unread = true
			snap.Processes = append(snap.Processes, proc)
			continue
		}
		proc.Name = d.Name
		snap.Processes = append(snap.Processes, proc)
		cp := d.cp
		use := func(elementID string) []archsketch.Use {
			return []archsketch.Use{{ProcessID: m.Ref, ElementID: elementID}}
		}

		for _, r := range cp.MessageReceipts() {
			snap.EntryPoints = append(snap.EntryPoints, archsketch.EntryPoint{
				Kind: archsketch.EntryMessage, Name: r.MessageName, Starts: r.Starts, UsedBy: use(r.ElementId),
			})
		}

		// The same enumeration the landscape draws its worker edges from (workerRefs),
		// for the same reason: a reference whose job type no managed Worker Type claims
		// is not a worker reference at all.
		for _, ref := range cp.ConnectorRefs() {
			kind := connectorKindOfJobType(ref.JobType)
			if kind == "" {
				continue
			}
			dep := archsketch.Dependency{
				Kind: archsketch.DependencyWorker, Name: ref.Connector, WorkerType: kind, UsedBy: use(ref.ElementId),
			}
			if name := strings.TrimSpace(ref.Connector); strings.HasPrefix(name, "=") {
				dep.Kind, dep.Name = archsketch.DependencyRuntimeWorker, name
			} else {
				// Who opens the connection to the target is decided by where this
				// server runs the work: an in-process handler connects itself, and any
				// other job is leased by a worker process (ADR-0164, ADR-0168).
				placement[[2]string{dep.Name, kind}] = archsketch.PlacementWorker
				if s.jobRunner != nil && s.jobRunner.Handles(ref.JobType) {
					placement[[2]string{dep.Name, kind}] = archsketch.PlacementEngine
				}
			}
			snap.Dependencies = append(snap.Dependencies, dep)
		}

		for _, ref := range cp.JobTypeRefs() {
			snap.Dependencies = append(snap.Dependencies, archsketch.Dependency{
				Kind: archsketch.DependencyJobType, Name: ref.JobType, UsedBy: use(ref.ElementId),
			})
		}

		for _, ref := range cp.BusinessRuleDecisionRefs() {
			snap.Dependencies = append(snap.Dependencies, archsketch.Dependency{
				Kind: archsketch.DependencyDecision, Name: ref.DecisionId, UsedBy: use(cp.ElementBpmnId(ref.ElementIndex)),
			})
		}

		for _, ref := range cp.CallActivities() {
			if shipped[ref.CalledProcessId] {
				// A call between two processes of this release stays inside the
				// application; it is not a way out of it.
				continue
			}
			dep := archsketch.Dependency{Kind: archsketch.DependencyCall, Name: ref.CalledProcessId, UsedBy: use(ref.ElementId)}
			if target := s.latestDeploymentByProcessID(ref.CalledProcessId); target != nil && target.ProjectID != "" {
				if target.ProjectID == appID {
					continue
				}
				dep.TargetApplicationID = target.ProjectID
				if proj, ok, err := s.projects.Get(target.ProjectID); err == nil && ok {
					dep.TargetApplication = proj.Name
				}
			}
			snap.Dependencies = append(snap.Dependencies, dep)
		}

		collectData(data, cp, m.Ref)
		collectBusiness(business, cp, m.Ref, d.Name)
	}
	received := map[string]bool{}
	for _, e := range snap.EntryPoints {
		received[e.Name] = true
	}
	snap.Physical = s.physicalPart(shipped, received, snap.Dependencies, placement)
	snap.Data = s.dataPart(appID, data, snap.Dependencies)
	snap.Business = s.businessPart(business, shipped)
	snap.Strategy = s.strategyPart(appID, shipped, snap.Dependencies)
	snap.Normalize()
	return snap
}

// physicalPart records what this installation has in place for the release, now.
// Run-loop goroutine only: it reads the node identity, the public-link and worker
// stores, and the worker registry.
//
// A store that cannot be read leaves the whole physical part out rather than a hole
// in it: a missing worker record would otherwise read as "this worker keeps its own
// configuration", which is a claim, and the renderings of a snapshot with no physical
// part say plainly that it has none.
func (s *Server) physicalPart(shipped, received map[string]bool, deps []archsketch.Dependency, placement map[[2]string]string) *archsketch.Physical {
	links, err := s.publicLinks.LoadAll()
	if err != nil {
		return nil
	}
	recs, err := s.connectors.LoadAll()
	if err != nil {
		return nil
	}
	subs, err := s.inboundSubs.LoadAll()
	if err != nil {
		return nil
	}
	p := &archsketch.Physical{
		ExternalURL:      archsketch.ParseEndpoint(s.externalURL),
		PublicStartLinks: []archsketch.StartLinks{},
		Workers:          []archsketch.WorkerSite{},
		JobTypes:         []archsketch.JobTypeSite{},
	}
	if id, err := s.nodeIdentity(); err == nil {
		p.Node, p.Environment = id.Name, id.Environment
	}

	counts := map[string]int{}
	for _, l := range links {
		if shipped[l.ProcessID] {
			counts[l.ProcessID]++
		}
	}
	for pid, n := range counts {
		p.PublicStartLinks = append(p.PublicStartLinks, archsketch.StartLinks{ProcessID: pid, Links: n})
	}

	configured := make(map[[2]string]connector, len(recs))
	for _, c := range recs {
		configured[[2]string{c.Name, c.Kind}] = c
	}
	seen := map[[2]string]bool{}
	for _, d := range deps {
		key := [2]string{d.Kind + "/" + d.Name, d.WorkerType}
		if seen[key] {
			continue
		}
		seen[key] = true
		switch d.Kind {
		case archsketch.DependencyWorker:
			site := archsketch.WorkerSite{
				Name: d.Name, WorkerType: d.WorkerType, Placement: placement[[2]string{d.Name, d.WorkerType}],
				Observers: archsketch.Observers{ObservedBy: s.workerInstances(func(st *workerStat) bool { return slices.Contains(st.Connectors, d.Name) })},
			}
			if c, ok := configured[[2]string{d.Name, d.WorkerType}]; ok {
				site.ConfiguredID = c.ID
				site.Endpoint = archsketch.ParseEndpoint(c.Endpoint)
				site.MailboxEndpoint = archsketch.ParseEndpoint(c.MailboxEndpoint)
				// The rest of the configuration, none of it a secret: which credential
				// a Worker uses is the vault's business, so only that it has one is
				// recorded (ADR-0495 slice B).
				site.Provider = c.Provider
				site.ScannerEndpoint = archsketch.ParseEndpoint(c.Scanner)
				site.Model = c.Model
				site.Credential = c.CredentialsRef != ""
				if c.Identity != nil {
					site.Identity = &archsketch.WorkerIdentity{Mode: c.Identity.Mode, Enforcement: c.Identity.Enforcement}
				}
			}
			if site.Endpoint == nil {
				// What the instances serving the name reported, when this installation
				// holds no endpoint for it itself — the worker that keeps its own
				// configuration (ADR-0168). One answer is recorded; several different
				// ones are recorded as differing, because a sketch drawing one system
				// where the installation reaches several would be wrong in the way that
				// matters most.
				switch reported := s.reportedEndpointsOf(d.Name); len(reported) {
				case 0:
				case 1:
					site.Endpoint, site.EndpointReported = archsketch.ParseEndpoint(reported[0]), true
				default:
					site.EndpointsDiffer = true
				}
			}
			p.Workers = append(p.Workers, site)
		case archsketch.DependencyJobType:
			p.JobTypes = append(p.JobTypes, archsketch.JobTypeSite{JobType: d.Name,
				Observers: archsketch.Observers{ObservedBy: s.workerInstances(func(st *workerStat) bool { return slices.Contains(st.Serves, d.Name) })}})
		}
	}

	// The inbound subscriptions that publish a message the release receives. A
	// subscription whose Worker was deleted since keeps that Worker's id, so a read
	// judges it as the ownerless configuration it was.
	byID := make(map[string]connector, len(recs))
	for _, c := range recs {
		byID[c.ID] = c
	}
	for _, sub := range subs {
		if !sub.Enabled || !received[sub.MessageName] {
			continue
		}
		in := archsketch.InboundSite{MessageName: sub.MessageName, ConfiguredID: sub.ConnectorID,
			Subject: inboundWatched(sub), Recursive: sub.Recursive && sub.WatchedSubject != ""}
		if c, ok := byID[sub.ConnectorID]; ok {
			in.Worker, in.WorkerType, in.Endpoint = c.Name, c.Kind, archsketch.ParseEndpoint(c.Endpoint)
		}
		p.Inbound = append(p.Inbound, in)
	}
	return p
}

// collectData adds one process's data objects with the elements that read and write
// them, the stores it names and its personal-data declaration to the data part being
// gathered (ADR-0495 slice C). A process declares its data objects at its root, so a
// name is one object within it.
func collectData(d *archsketch.Data, cp *compiler.CompiledProcess, processID string) {
	index := map[string]int{}
	for _, do := range cp.DataObjects() {
		name := cp.Intern(do.Name)
		index[name] = len(d.Objects)
		d.Objects = append(d.Objects, archsketch.DataObject{ProcessID: processID, Name: name, Class: cp.Intern(do.ItemType),
			Collection: do.IsCollection, InitialState: cp.Intern(do.InitialState)})
	}
	for id := range int32(cp.NodeCount()) {
		element := cp.ElementBpmnId(id)
		for _, in := range cp.DataInputAssociations(id) {
			if i, ok := index[cp.Intern(in.DataObject)]; ok {
				d.Objects[i].ReadBy = append(d.Objects[i].ReadBy, element)
			}
		}
		for _, out := range cp.DataOutputAssociations(id) {
			i, ok := index[cp.Intern(out.DataObject)]
			if !ok {
				continue
			}
			w := archsketch.DataWrite{ElementID: element, State: cp.Intern(out.TargetState)}
			for _, write := range out.Writes {
				if member := cp.Intern(write.TargetPath); member != "" {
					w.Members = append(w.Members, member)
				}
			}
			d.Objects[i].WrittenBy = append(d.Objects[i].WrittenBy, w)
		}
	}
	for _, st := range cp.DataStores() {
		d.Stores = append(d.Stores, archsketch.DataStore{Name: cp.Intern(st.Name),
			UsedBy: []archsketch.Use{{ProcessID: processID, ElementID: cp.Intern(st.ElementId)}}})
	}
	if cp.HasPersonalVariables() {
		d.Personal = append(d.Personal, archsketch.PersonalDeclaration{ProcessID: processID,
			Variables: slices.Clone(cp.PersonalVariables()), Subject: cp.DataSubjectVariable()})
	}
}

// dataPart completes the data part: each class a data object declares or a store
// holds, checked against the application's information model, and for each process
// declaring personal data the workers and job types it hands work to. Each of those
// can read every variable visible at its task in plaintext — a leased job carries them
// all deciphered (pulledJob), and a Worker Atlas runs resolves its task through the
// personal reader (drive) — so the snapshot names them all rather than only those
// whose expressions name a personal variable, which would understate it.
//
// Nil when the information models cannot be read: a class recorded as not modeled
// because the store failed would be a claim, and a snapshot without a data part says
// plainly that it has none. Run-loop goroutine only.
func (s *Server) dataPart(appID string, d *archsketch.Data, deps []archsketch.Dependency) *archsketch.Data {
	vocab, err := s.infomodel.VocabularyOnLoop(appID)
	if err != nil {
		return nil
	}
	for i := range d.Stores {
		if st, ok := vocab.Store(d.Stores[i].Name); ok {
			d.Stores[i].Class = st.Class
		}
	}
	class := func(name string) {
		if name == "" {
			return
		}
		_, modeled := vocab.Class(name)
		d.Classes = append(d.Classes, archsketch.DataClass{Name: name, Modeled: modeled})
	}
	for _, o := range d.Objects {
		class(o.Class)
	}
	for _, st := range d.Stores {
		class(st.Class)
	}
	for i := range d.Personal {
		p := &d.Personal[i]
		for _, dep := range deps {
			if dep.Kind != archsketch.DependencyWorker && dep.Kind != archsketch.DependencyRuntimeWorker && dep.Kind != archsketch.DependencyJobType {
				continue
			}
			if slices.ContainsFunc(dep.UsedBy, func(u archsketch.Use) bool { return u.ProcessID == p.ProcessID }) {
				p.HandedTo = append(p.HandedTo, archsketch.PersonalRecipient{Kind: dep.Kind, Name: dep.Name, WorkerType: dep.WorkerType})
			}
		}
	}
	return d
}

// inboundWatched says what an inbound subscription watches, in the terms of the
// Worker kind it reads through: a clio subject, a Jira query, a spreadsheet and its
// range, a folder, a channel or a mail folder (ADR-0075, ADR-0214, ADR-0234). A
// subscription names exactly one of them.
func inboundWatched(sub inboundSubscription) string {
	switch {
	case sub.WatchedSubject != "":
		return sub.WatchedSubject
	case sub.JQL != "":
		return "JQL " + sub.JQL
	case sub.SpreadsheetID != "":
		return strings.TrimSpace("spreadsheet " + sub.SpreadsheetID + " " + sub.WatchRange)
	case sub.FolderID != "":
		return "folder " + sub.FolderID
	case sub.ChannelID != "":
		return "channel " + sub.ChannelID
	case sub.MailFolder != "":
		return "mail folder " + sub.MailFolder
	}
	return ""
}

// workerInstances names the worker instances seen this run that match. The registry
// is runtime state a restart empties (api/workers.go), which is why a snapshot calls
// these observed rather than serving. Run-loop goroutine only.
func (s *Server) workerInstances(match func(*workerStat) bool) []string {
	var out []string
	if s.workers == nil {
		return out
	}
	for _, st := range s.workers.byName {
		if match(st) {
			name := st.Worker
			if name == "" {
				name = "an unnamed worker"
			}
			out = append(out, name)
		}
	}
	return out
}

// reportedEndpointsOf returns the distinct endpoints the instances seen this run
// reported for a worker name, sorted. Run-loop goroutine only.
func (s *Server) reportedEndpointsOf(name string) []string {
	var out []string
	if s.workers == nil {
		return out
	}
	for _, st := range s.workers.byName {
		if e, ok := st.Endpoints[name]; ok && !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	slices.Sort(out)
	return out
}

// redactedArchitecture is the snapshot as this reader may see it: a worker's endpoint
// only for a reader who may see that worker's configuration, the worker instances
// only for a reader who may see the Workers view, and a catalogue product only for a
// reader who may maintain its catalogue. Run-loop goroutine only: it reads the worker
// and catalogue stores.
//
// A worker that no longer exists is judged as an ownerless one, which only an
// administrator may see (connectorRole) — its endpoint was somebody's configuration,
// and that it was deleted since is no reason to show it to everybody.
func (s *Server) redactedArchitecture(r *http.Request, snap *archsketch.Snapshot) *archsketch.Snapshot {
	if snap == nil {
		return snap
	}
	out := *snap
	if snap.Business != nil {
		b := s.withholdProducts(r, *snap.Business)
		out.Business = &b
	}
	if snap.Physical == nil {
		return &out
	}
	mayViewWorker := func(id string) bool {
		if id == "" {
			return true
		}
		c, ok, err := s.connectors.Get(id)
		if err != nil {
			return false
		}
		if !ok {
			c = connector{}
		}
		code, _ := s.checkConnectorRole(r, c, ScopeRoleViewer)
		return code == 0
	}
	red := snap.Physical.Redacted(mayViewWorker, mayReach(httpapi.PrincipalFrom(r.Context()), RoleOperator))
	out.Physical = &red
	return &out
}

// releaseArchitectureResp is the JSON answer: the release's names beside the snapshot,
// because the snapshot is the release's content and these are what a reader calls it.
type releaseArchitectureResp struct {
	ApplicationID string `json:"applicationId"`
	Application   string `json:"application"`
	ReleaseID     string `json:"releaseId"`
	Version       int32  `json:"version"`
	PublishedAt   int64  `json:"publishedAt"`
	// Layers says which layers the snapshot records and which this answer shows, so a
	// reader can offer the rest without guessing what an older release holds.
	Layers       releaseArchitectureLayers `json:"layers"`
	Architecture *archsketch.Snapshot      `json:"architecture"`
}

type releaseArchitectureLayers struct {
	Recorded []archsketch.Layer `json:"recorded"`
	Shown    []archsketch.Layer `json:"shown"`
}

func (s *Server) handleReleaseArchitecture(w http.ResponseWriter, r *http.Request) {
	s.serveReleaseArchitecture(w, r, "json")
}

func (s *Server) handleReleaseArchitectureSVG(w http.ResponseWriter, r *http.Request) {
	s.serveReleaseArchitecture(w, r, "svg")
}

func (s *Server) handleReleaseArchitectureXML(w http.ResponseWriter, r *http.Request) {
	s.serveReleaseArchitecture(w, r, "xml")
}

// serveReleaseArchitecture answers one release's snapshot under the application's
// sharing scope (ADR-0071), in the format the route names. The rendering runs off the
// loop: it reads nothing but the snapshot it is handed.
func (s *Server) serveReleaseArchitecture(w http.ResponseWriter, r *http.Request, format string) {
	appID := r.PathValue("id")
	version64, err := strconv.ParseInt(r.PathValue("version"), 10, 32)
	if err != nil || version64 <= 0 {
		httpapi.Error(w, http.StatusBadRequest, "invalid release version")
		return
	}
	version := int32(version64)
	selection, err := archsketch.ParseSelection(r.URL.Query().Get("layers"))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	proj, code, msg := s.authorizeProject(r, appID, ScopeRoleViewer)
	if code != 0 {
		httpapi.Error(w, code, msg)
		return
	}

	var (
		rel     *applicationRelease
		loadErr error
	)
	s.do(func() {
		rels, err := s.releases.forApplication(appID)
		if err != nil {
			loadErr = err
			return
		}
		for i := range rels {
			if rels[i].Version == version {
				rel = &rels[i]
				rel.Architecture = s.redactedArchitecture(r, rel.Architecture)
				return
			}
		}
	})
	switch {
	case loadErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "load releases: "+loadErr.Error())
		return
	case rel == nil:
		httpapi.Error(w, http.StatusNotFound, "no release v"+strconv.Itoa(int(version))+" of this application")
		return
	case rel.Architecture == nil:
		// Said in words rather than left as a bare 404, because the two read alike and
		// send somebody to different places: this release exists and simply predates
		// snapshots, so publishing again is the remedy, not looking for a typo.
		httpapi.Error(w, http.StatusNotFound, "release v"+strconv.Itoa(int(version))+
			" has no architecture snapshot: it was recorded before Atlas took them (ADR-0471). "+
			"A release published from now on carries one.")
		return
	}

	recorded := rel.Architecture.Recorded()
	if selection&recorded == 0 {
		// Refused in words rather than drawn empty: an empty picture reads as a release
		// that offers and needs nothing, which is not what this one says.
		httpapi.Error(w, http.StatusNotFound, "release v"+strconv.Itoa(int(version))+
			" records none of the layers asked for ("+selection.String()+"); it records "+recorded.String())
		return
	}
	view := rel.Architecture.Select(selection)

	m := archsketch.Meta{ApplicationID: appID, Application: proj.Name, ReleaseID: rel.ID, Version: rel.Version}
	switch format {
	case "svg":
		serveSketch(w, "image/svg+xml; charset=utf-8", archsketch.SVG(view, m))
	case "xml":
		serveSketch(w, "application/xml; charset=utf-8", archsketch.ArchiMate(view, m))
	default:
		httpapi.JSON(w, http.StatusOK, releaseArchitectureResp{
			ApplicationID: appID, Application: proj.Name, ReleaseID: rel.ID,
			Version: rel.Version, PublishedAt: rel.PublishedAt,
			Layers:       releaseArchitectureLayers{Recorded: recorded.Layers(), Shown: (selection & recorded).Layers()},
			Architecture: &view,
		})
	}
}

// serveSketch writes a rendering under the headers that keep it inert wherever it is
// opened: the type pinned, and a policy with no script origin. The renderings carry
// no script to begin with; the policy is what keeps it that way if one ever did.
func serveSketch(w http.ResponseWriter, contentType string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
