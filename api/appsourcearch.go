package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/capability"
	"github.com/pblumer/atlas/api/importplan"
	"github.com/pblumer/atlas/api/infomodel"
)

// What a source tree carries of the architecture (format 4,
// ADR-0528).
//
//	atlas.json                          "informationModels": [{"name", "path"}], "architecture": path
//	architecture/<name>.model.json      an information model, in the shape POST /infomodel/import reads
//	architecture/capabilities.json      {"realizations": […], "capabilities": […], "valueStreams": […]}
//
// The capability map is the server's, the organisation's (ADR-0305): one flat register
// that catalogue headings (ADR-0524), Panorama (ADR-0308) and the gap report read with
// one meaning per key. A tree therefore never owns a capability. It carries:
//
//   - its information models, which belong to the application and are planned and
//     decided like any artifact (infomodel:<name>);
//   - its realizations — which of its processes, and which Workers, realize which
//     capability key — owned by the application, written and replaced by every import,
//     removed with the application (realizations:<key>);
//   - proposals: capabilities and value streams in ADR-0305's shape, for the keys it
//     needs. A key the map has is linked and its record is never written over; a key
//     it lacks is a gap (capability:<key>, valuestream:<key>) the caller settles —
//     adopt the proposal, map it onto a key the map has, or omit it. Nothing is filed
//     in the map silently.
//
// The format does not care where the map comes from. A server whose map was installed
// from a shared reference model (the ADR's option C) reads the same tree the same way:
// fewer gaps, the same decisions.
//
// Design-time state throughout: sidecar records, nothing in the event log (I2, I4).

// architecturePath is where an export writes the architecture file, and
// informationModelDir where it writes the models.
const (
	architecturePath    = "architecture/capabilities.json"
	informationModelDir = "architecture/"
)

// sourceInfoModel indexes one information model of a tree.
type sourceInfoModel struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// sourceArchitecture is the architecture file.
type sourceArchitecture struct {
	// Comment is for whoever reads the file; the import ignores it.
	Comment      string                          `json:"_comment,omitempty"`
	Realizations []capability.BundledRealization `json:"realizations"`
	// Capabilities and ValueStreams are proposals: offered for keys the map lacks,
	// never written over a record the map has.
	Capabilities []capability.Capability  `json:"capabilities"`
	ValueStreams []capability.ValueStream `json:"valueStreams"`
}

// treeArchitectureParsed is what a tree carries of the architecture, read and checked.
type treeArchitectureParsed struct {
	models []treeModel
	arch   *sourceArchitecture
}

type treeModel struct {
	name  string
	data  []byte
	model infomodel.Model
}

// treeArchitecture reads and checks the architecture a tree carries: every model file
// present, parseable, named as the manifest names it and named once; the architecture
// file well-formed, its realizations naming processes of the tree, its proposals each
// valid on its own. Whether a proposal fits the map is the plan's question, not this
// one's.
func treeArchitecture(man sourceManifest, byPath map[string][]byte) (treeArchitectureParsed, error) {
	var out treeArchitectureParsed
	if (len(man.InformationModels) > 0 || man.Architecture != "") && man.FormatVersion < sourceFormatVersion {
		return out, fmt.Errorf("%s names information models or an architecture, which format %d carries; it says format %d",
			manifestPath, sourceFormatVersion, man.FormatVersion)
	}
	seen := map[string]bool{}
	for _, e := range man.InformationModels {
		raw, ok := byPath[e.Path]
		if !ok {
			return out, fmt.Errorf("information model %q names %q, which is not in the tree", e.Name, e.Path)
		}
		m, _, err := infomodel.ParseBundleModel(raw)
		if err != nil {
			return out, fmt.Errorf("information model %s: %w", e.Path, err)
		}
		if e.Name != "" && strings.TrimSpace(e.Name) != m.Name {
			return out, fmt.Errorf("information model %s is named %q, and %s names it %q", e.Path, m.Name, manifestPath, e.Name)
		}
		if seen[m.Name] {
			return out, fmt.Errorf("two information models are named %q", m.Name)
		}
		seen[m.Name] = true
		out.models = append(out.models, treeModel{name: m.Name, data: raw, model: m})
	}
	if man.Architecture == "" {
		return out, nil
	}
	raw, ok := byPath[man.Architecture]
	if !ok {
		return out, fmt.Errorf("%s names its architecture in %q, which is not in the tree", manifestPath, man.Architecture)
	}
	var arch sourceArchitecture
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&arch); err != nil {
		return out, fmt.Errorf("invalid %s: %w", man.Architecture, err)
	}
	processes := map[string]bool{}
	for _, p := range man.Processes {
		processes[p.ID] = true
	}
	arch.Realizations = capability.NormalizeBundledRealizations(arch.Realizations)
	if findings := capability.ValidateBundledRealizations(arch.Realizations, processes); len(findings) > 0 {
		return out, fmt.Errorf("%s: %s", man.Architecture, strings.Join(findings, "; "))
	}
	capKeys := map[string]bool{}
	for i := range arch.Capabilities {
		c := &arch.Capabilities[i]
		if len(c.Realizations) > 0 {
			return out, fmt.Errorf("%s: proposed capability %q carries realizations; a bundle states what it realizes under realizations, and the map's own realizations are the map's",
				man.Architecture, c.Key)
		}
		capability.NormalizeCapability(c)
		if findings := capability.ValidateCapability(*c); len(findings) > 0 {
			return out, fmt.Errorf("%s: proposed capability %q: %s", man.Architecture, c.Key, strings.Join(findings, "; "))
		}
		if capKeys[c.Key] {
			return out, fmt.Errorf("%s proposes capability %q twice", man.Architecture, c.Key)
		}
		capKeys[c.Key] = true
	}
	streamKeys := map[string]bool{}
	for i := range arch.ValueStreams {
		v := &arch.ValueStreams[i]
		capability.NormalizeValueStream(v)
		if findings := capability.ValidateValueStream(*v); len(findings) > 0 {
			return out, fmt.Errorf("%s: proposed value stream %q: %s", man.Architecture, v.Key, strings.Join(findings, "; "))
		}
		if streamKeys[v.Key] {
			return out, fmt.Errorf("%s proposes value stream %q twice", man.Architecture, v.Key)
		}
		streamKeys[v.Key] = true
	}
	out.arch = &arch
	return out, nil
}

// architecturePlan is what an import does with a tree's architecture.
type architecturePlan struct {
	artifacts []importplan.Artifact
	undecided []string
	// incoming are the bases the plan settles (the models' document hashes).
	incoming map[string]string
	// models to write, by name.
	models []treeModel
	// capabilities and valueStreams adopted into the map.
	capabilities []capability.Capability
	valueStreams []capability.ValueStream
	// realizations is the application's list as the decisions settle it; nil when the
	// tree carries no architecture, which leaves the application's record as it is.
	realizations      []capability.BundledRealization
	writeRealizations bool
}

// modelContentBase is the base under which an import remembers the content of the
// model it wrote, beside the document's hash under the model's ref: the stored model
// gets ids the document does not carry, so its content is told unchanged by its own
// fingerprint rather than by the document's bytes.
func modelContentBase(name string) string { return "infomodel-content:" + name }

// proposalFingerprint is a capability as a proposal compares: what it says, without the
// realizations (the bundle states its own apart) and without the record's revision,
// audit fields and confirmation.
func proposalFingerprint(c capability.Capability) string {
	capability.NormalizeCapability(&c)
	c.Realizations = nil
	c.Revision, c.CreatedAt, c.CreatedBy, c.UpdatedAt, c.UpdatedBy = 0, 0, "", 0, ""
	c.Confirmation = capability.Confirmation{}
	data, _ := json.Marshal(c)
	return importplan.Hash(data)
}

func streamFingerprint(v capability.ValueStream) string {
	capability.NormalizeValueStream(&v)
	v.Revision, v.CreatedAt, v.CreatedBy, v.UpdatedAt, v.UpdatedBy = 0, 0, "", 0, ""
	v.Confirmation = capability.Confirmation{}
	data, _ := json.Marshal(v)
	return importplan.Hash(data)
}

func realizationsFingerprint(list []capability.BundledRealization) string {
	data, _ := json.Marshal(capability.NormalizeBundledRealizations(list))
	return importplan.Hash(data)
}

// planSourceArchitecture plans a tree's information models, gaps and realizations.
// Runs on the run loop, inside applySourceTree's turn.
func (s *Server) planSourceArchitecture(app project, found bool, man sourceManifest, byPath map[string][]byte,
	bases map[string]string, decide importplan.Decisions) (architecturePlan, error) {
	plan := architecturePlan{incoming: map[string]string{}}
	parsed, err := treeArchitecture(man, byPath)
	if err != nil {
		return plan, sourceRefusal{http.StatusBadRequest, err.Error()}
	}
	if len(parsed.models) > 0 && s.infomodel == nil {
		return plan, sourceRefusal{http.StatusBadRequest, "the tree carries information models, and this server keeps none"}
	}

	// Information models: compared and decided like any artifact.
	for _, tm := range parsed.models {
		if err := s.infomodel.CheckBundleModel(tm.model); err != nil {
			return plan, sourceRefusal{http.StatusBadRequest, fmt.Sprintf("information model %q: %s", tm.name, err)}
		}
		ref := "infomodel:" + tm.name
		in := importplan.Hash(newlineTerminated(tm.data))
		var stored infomodel.Model
		exists := false
		if found {
			if stored, exists, err = s.infomodel.ApplicationModel(app.ID, tm.name); err != nil {
				return plan, err
			}
		}
		local := ""
		if exists {
			local = importplan.Hash(infomodel.ContentFingerprint(stored))
			// Untouched since the last import wrote it: it stands for the document that
			// import brought.
			if b := bases[modelContentBase(tm.name)]; b != "" && b == local {
				local = bases[ref]
			}
			// A document that says exactly what the stored model says — a tree this
			// server exported, brought back — is the same artifact.
			if infomodel.SameContent(stored, tm.model) {
				local = in
			}
		}
		status := importplan.Classify(exists, local, in, bases[ref])
		outcome := decide.Outcome(ref, status)
		plan.incoming[ref] = in
		if outcome == importplan.Undecided {
			plan.undecided = append(plan.undecided, ref)
		}
		if importplan.Writes(outcome) {
			plan.models = append(plan.models, tm)
		}
		plan.artifacts = append(plan.artifacts, importplan.Artifact{Ref: ref, Name: tm.name, Status: status, Outcome: outcome})
	}

	if parsed.arch == nil {
		return plan, nil
	}
	arch := parsed.arch
	if s.capabilityRecords == nil || s.valueStreamRecords == nil {
		return plan, sourceRefusal{http.StatusBadRequest, "the tree carries an architecture, and this server keeps no capability map"}
	}

	proposedCap := map[string]capability.Capability{}
	for _, c := range arch.Capabilities {
		proposedCap[c.Key] = c
	}
	// Every capability key the tree needs: what it realizes and what it proposes.
	need := map[string]bool{}
	for _, r := range arch.Realizations {
		need[r.Capability] = true
	}
	for k := range proposedCap {
		need[k] = true
	}
	keys := make([]string, 0, len(need))
	for k := range need {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// settled maps every needed key to the key it is filed under: itself (in the map or
	// adopted), the existing key it is mapped onto, or "" when omitted or undecided.
	settled := map[string]string{}
	inMap := func(key string) (capability.Capability, bool, error) { return s.capabilityRecords.Get(key) }
	adopting := map[string]bool{}
	for _, key := range keys {
		ref := "capability:" + key
		prop, proposed := proposedCap[key]
		rec, exists, err := inMap(key)
		if err != nil {
			return plan, err
		}
		if exists {
			settled[key] = key
			if !proposed {
				continue
			}
			a := importplan.Artifact{Ref: ref, Name: rec.Name, Proposed: true, Status: importplan.Linked}
			if proposalFingerprint(rec) == proposalFingerprint(prop) {
				a.Compared, a.Outcome = importplan.Unchanged, importplan.Unchanged
			} else {
				a.Compared, a.Outcome = importplan.Changed, importplan.Kept
				a.Detail = "the map has this capability with other content; the map's record stays as it is, and the bundle's realizations are filed under it"
			}
			plan.artifacts = append(plan.artifacts, a)
			continue
		}
		outcome, mappedTo := decide.GapOutcome(ref)
		a := importplan.Artifact{Ref: ref, Name: key, Status: importplan.Gap, Outcome: outcome, Proposed: proposed, MappedTo: mappedTo}
		if proposed {
			a.Name = prop.Name
		}
		switch outcome {
		case importplan.Created:
			if !proposed {
				return plan, sourceRefusal{http.StatusBadRequest, fmt.Sprintf("%s is named under adopt, and the tree proposes no capability %q to adopt; map it onto a key the map has, or omit it", ref, key)}
			}
			settled[key] = key
			adopting[key] = true
		case importplan.Mapped:
			settled[key] = mappedTo
		case importplan.Undecided:
			plan.undecided = append(plan.undecided, ref)
		}
		plan.artifacts = append(plan.artifacts, a)
	}
	// A key mapped onto must be one the map has, or one this import adopts.
	for _, key := range keys {
		target := settled[key]
		if target == "" || target == key {
			continue
		}
		if adopting[target] {
			continue
		}
		if _, ok, err := inMap(target); err != nil {
			return plan, err
		} else if !ok {
			return plan, sourceRefusal{http.StatusBadRequest, fmt.Sprintf("capability:%s is mapped onto %q, which this server's map does not have", key, target)}
		}
	}
	// A capability a mapped or omitted gap names is not a key the map will have.
	resolve := func(key string) (string, bool) {
		if t, ok := settled[key]; ok {
			return t, t != ""
		}
		_, ok, _ := inMap(key)
		return key, ok
	}
	for _, key := range keys {
		if !adopting[key] {
			continue
		}
		c := proposedCap[key]
		var requires []string
		for _, dep := range c.Requires {
			if t, ok := resolve(dep); ok {
				requires = append(requires, t)
			} else {
				requires = append(requires, dep)
			}
		}
		c.Requires = requires
		plan.capabilities = append(plan.capabilities, c)
	}

	// Value streams: a proposal for a key the map has is linked, never written over;
	// one it lacks is adopted or omitted. Mapping a value stream onto another is not a
	// thing — nothing in the tree points at a value stream.
	for _, v := range arch.ValueStreams {
		ref := "valuestream:" + v.Key
		rec, exists, err := s.valueStreamRecords.Get(v.Key)
		if err != nil {
			return plan, err
		}
		if exists {
			a := importplan.Artifact{Ref: ref, Name: rec.Name, Proposed: true, Status: importplan.Linked}
			if streamFingerprint(rec) == streamFingerprint(v) {
				a.Compared, a.Outcome = importplan.Unchanged, importplan.Unchanged
			} else {
				a.Compared, a.Outcome = importplan.Changed, importplan.Kept
				a.Detail = "the map has this value stream with other content; the map's record stays as it is"
			}
			plan.artifacts = append(plan.artifacts, a)
			continue
		}
		outcome, _ := decide.GapOutcome(ref)
		a := importplan.Artifact{Ref: ref, Name: v.Name, Status: importplan.Gap, Outcome: outcome, Proposed: true}
		switch outcome {
		case importplan.Mapped:
			return plan, sourceRefusal{http.StatusBadRequest, fmt.Sprintf("%s cannot be mapped: a value stream is adopted or omitted", ref)}
		case importplan.Undecided:
			plan.undecided = append(plan.undecided, ref)
		case importplan.Created:
			for i := range v.Stages {
				st := &v.Stages[i]
				for j, k := range st.Capabilities {
					t, ok := resolve(k)
					if !ok {
						return plan, sourceRefusal{http.StatusConflict, fmt.Sprintf(
							"%s is adopted, and its stage %q names capability %q, which this import leaves out of the map; adopt or map that capability too, or omit the value stream",
							ref, st.Name, k)}
					}
					st.Capabilities[j] = t
				}
			}
			plan.valueStreams = append(plan.valueStreams, v)
		}
		plan.artifacts = append(plan.artifacts, a)
	}

	// The application's realizations, as the decisions settle them: under the key each
	// is filed under, and without those whose key was left out.
	final := []capability.BundledRealization{}
	for _, r := range arch.Realizations {
		t := settled[r.Capability]
		if t == "" {
			continue
		}
		r.Capability = t
		final = append(final, r)
	}
	final = capability.NormalizeBundledRealizations(final)
	plan.realizations = final
	ref := "realizations:" + man.Key
	in := realizationsFingerprint(final)
	var current capability.ApplicationRealizations
	exists := false
	if found && s.bundledRealizations != nil {
		if current, exists, err = s.bundledRealizations.Get(app.ID); err != nil {
			return plan, err
		}
	}
	a := importplan.Artifact{Ref: ref, Name: fmt.Sprintf("%d realizations", len(final))}
	switch {
	case !exists:
		a.Status, a.Outcome = importplan.New, importplan.Created
	case realizationsFingerprint(current.Realizations) == in && current.ApplicationKey == man.Key:
		a.Status, a.Outcome = importplan.Unchanged, importplan.Unchanged
	default:
		// The application's own record: an import replaces it as the tree has it,
		// because nobody edits it anywhere else.
		a.Status, a.Outcome = importplan.Owned, importplan.Updated
		a.Detail = "the application's realizations are replaced as the tree states them"
	}
	plan.writeRealizations = a.Outcome != importplan.Unchanged
	plan.artifacts = append(plan.artifacts, a)
	return plan, nil
}

// writeSourceArchitecture writes what the plan decided: models, adopted capabilities
// and value streams, then the application's realizations. It answers the content bases
// of the models it wrote. Runs on the run loop.
func (s *Server) writeSourceArchitecture(app project, plan architecturePlan, actor string) (map[string]string, error) {
	bases := map[string]string{}
	for _, tm := range plan.models {
		saved, err := s.infomodel.FileBundleModel(app.ID, tm.model, actor)
		if err != nil {
			return nil, fmt.Errorf("write information model %q: %w", tm.name, err)
		}
		bases[modelContentBase(tm.name)] = importplan.Hash(infomodel.ContentFingerprint(saved))
	}
	now := time.Now().Unix()
	for _, c := range plan.capabilities {
		if _, exists, err := s.capabilityRecords.Get(c.Key); err != nil {
			return nil, err
		} else if exists {
			// Filed meanwhile by somebody else: the map's record stands.
			continue
		}
		c.Revision, c.CreatedAt, c.CreatedBy, c.UpdatedAt, c.UpdatedBy = 1, now, actor, now, actor
		c.Confirmation = capability.Confirmation{At: now, By: actor}
		if err := s.capabilityRecords.Save(c); err != nil {
			return nil, fmt.Errorf("file capability %q: %w", c.Key, err)
		}
	}
	for _, v := range plan.valueStreams {
		if _, exists, err := s.valueStreamRecords.Get(v.Key); err != nil {
			return nil, err
		} else if exists {
			continue
		}
		v.Revision, v.CreatedAt, v.CreatedBy, v.UpdatedAt, v.UpdatedBy = 1, now, actor, now, actor
		v.Confirmation = capability.Confirmation{At: now, By: actor}
		if err := s.valueStreamRecords.Save(v); err != nil {
			return nil, fmt.Errorf("file value stream %q: %w", v.Key, err)
		}
	}
	if plan.writeRealizations && s.bundledRealizations != nil {
		rec := capability.ApplicationRealizations{
			ApplicationID: app.ID, ApplicationKey: app.Key,
			Realizations: plan.realizations, UpdatedAt: now, UpdatedBy: actor,
		}
		if err := s.bundledRealizations.Save(rec); err != nil {
			return nil, fmt.Errorf("file the application's realizations: %w", err)
		}
	}
	return bases, nil
}

// sourceArchitectureRecords is what an export carries of the architecture.
type sourceArchitectureRecords struct {
	models       []infomodel.Model
	realizations *capability.ApplicationRealizations
	capabilities []capability.Capability
	valueStreams []capability.ValueStream
}

// loadSourceArchitecture reads an application's architecture for an export: its
// information models, its realizations, and — as proposals — the capabilities they
// name and the value streams with a stage one of those capabilities serves. Runs on the
// run loop.
func (s *Server) loadSourceArchitecture(app project) (sourceArchitectureRecords, error) {
	var out sourceArchitectureRecords
	if s.infomodel != nil {
		models, err := s.infomodel.ModelsForApplicationOnLoop(app.ID)
		if err != nil {
			return out, err
		}
		out.models = models
	}
	if s.bundledRealizations == nil || s.capabilityRecords == nil || s.valueStreamRecords == nil {
		return out, nil
	}
	rec, ok, err := s.bundledRealizations.Get(app.ID)
	if err != nil || !ok {
		return out, err
	}
	out.realizations = &rec
	named := map[string]bool{}
	for _, r := range rec.Realizations {
		named[r.Capability] = true
	}
	caps, err := s.capabilityRecords.LoadAll()
	if err != nil {
		return out, err
	}
	for _, c := range caps {
		if named[c.Key] {
			out.capabilities = append(out.capabilities, c)
		}
	}
	streams, err := s.valueStreamRecords.LoadAll()
	if err != nil {
		return out, err
	}
	for _, v := range streams {
		if slices.ContainsFunc(v.Stages, func(st capability.Stage) bool {
			return slices.ContainsFunc(st.Capabilities, func(k string) bool { return named[k] })
		}) {
			out.valueStreams = append(out.valueStreams, v)
		}
	}
	return out, nil
}

// addSourceArchitecture adds the architecture to a rendered tree: the model files, the
// architecture file and their entries in the manifest, which then says format 4. A tree
// without either is returned as it was, so an application without architecture exports
// byte for byte as before. Pure, like buildSourceTree.
func addSourceArchitecture(files []sourceFile, arch sourceArchitectureRecords) ([]sourceFile, error) {
	if len(arch.models) == 0 && arch.realizations == nil {
		return files, nil
	}
	if len(files) == 0 || files[0].Path != manifestPath {
		return nil, fmt.Errorf("the tree does not start with %s", manifestPath)
	}
	var man sourceManifest
	if err := json.Unmarshal(files[0].Data, &man); err != nil {
		return nil, fmt.Errorf("render manifest: %w", err)
	}
	taken := map[string]bool{}
	for _, f := range files {
		taken[f.Path] = true
	}
	models := slices.Clone(arch.models)
	sort.Slice(models, func(i, j int) bool { return models[i].Name < models[j].Name })
	for _, m := range models {
		data, err := infomodel.BundleDocument(m)
		if err != nil {
			return nil, fmt.Errorf("render information model %q: %w", m.Name, err)
		}
		p := uniquePath(taken, informationModelDir+slugify(m.Name, "model")+".model.json")
		man.InformationModels = append(man.InformationModels, sourceInfoModel{Name: m.Name, Path: p})
		files = append(files, sourceFile{Path: p, Data: data})
	}
	if arch.realizations != nil {
		doc := sourceArchitecture{
			Realizations: capability.NormalizeBundledRealizations(arch.realizations.Realizations),
			Capabilities: []capability.Capability{},
			ValueStreams: []capability.ValueStream{},
		}
		caps := slices.Clone(arch.capabilities)
		sort.Slice(caps, func(i, j int) bool { return caps[i].Key < caps[j].Key })
		for _, c := range caps {
			c.Realizations = nil
			c.Revision, c.CreatedAt, c.CreatedBy, c.UpdatedAt, c.UpdatedBy = 0, 0, "", 0, ""
			c.Confirmation = capability.Confirmation{}
			doc.Capabilities = append(doc.Capabilities, c)
		}
		streams := slices.Clone(arch.valueStreams)
		sort.Slice(streams, func(i, j int) bool { return streams[i].Key < streams[j].Key })
		for _, v := range streams {
			v.Revision, v.CreatedAt, v.CreatedBy, v.UpdatedAt, v.UpdatedBy = 0, 0, "", 0, ""
			v.Confirmation = capability.Confirmation{}
			doc.ValueStreams = append(doc.ValueStreams, v)
		}
		data, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("render architecture: %w", err)
		}
		p := uniquePath(taken, architecturePath)
		man.Architecture = p
		files = append(files, sourceFile{Path: p, Data: newlineTerminated(data)})
	}
	man.FormatVersion = sourceFormatVersion
	data, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("render manifest: %w", err)
	}
	files[0] = sourceFile{Path: manifestPath, Data: newlineTerminated(data)}
	return files, nil
}
