package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/pblumer/atlas/api/importplan"
	"github.com/pblumer/atlas/dmn"
)

// The curated source layout (ADR-0134 Phase 4): an application's *source* —
// drafts, forms, and DMN references — rendered as a small tree of files a human
// can read, diff, and review, rather than the sidecar JSON the stores keep on
// disk.
//
//	atlas.json                                # manifest: key, name, format version, index
//	processes/mitarbeiter-onboarding.bpmn     # the draft's XML, verbatim
//	forms/neuer-mitarbeiter.form.json         # the form-js schema, verbatim
//	decisions/freigabe.dmn                    # a model this server keeps, verbatim
//
// Two things are worth stating because they refine the illustrative tree in
// ADR-0134:
//
//   - **The manifest is the index.** Every artifact appears there with its id,
//     its name, and the path of its file. The files themselves hold nothing but
//     native content, so a `.bpmn` opens in any modeler and a `.form.json` opens
//     in any form editor — neither carries an Atlas-specific wrapper, and neither
//     has to be parsed to learn which artifact it is.
//   - **A DMN reference is manifest data; its model is a file only when Atlas
//     keeps it.** A reference is a name plus a model handle. Where the handle
//     resolves against a remote temis service the model is temis's
//     (ADR-0014/0034), and copying it into the tree would pretend Atlas owns it.
//     Where it resolves against this server's own model folder — the
//     zero-config default, written by the decision editor and the model upload
//     (ADR-0321) — Atlas is the only place the model lives, and a tree without it
//     installs elsewhere as a decision whose model is not there. Such a model
//     travels as `decisions/<handle>.dmn`, once however many of its decisions the
//     application references (ADR-draft-application-package-export).
//
// Deployments and releases deliberately do not travel: they are records of what
// happened on one server, not source an author edits (ADR-0134).
//
// This is design-time state throughout. Nothing here enters the event log, and
// none of it affects recovery (ADR-0002).

// The layout's format version, carried in every manifest. A second serialization
// can drift from the records it mirrors; the version is what lets a later reader
// say so out loud instead of silently misreading a tree written by an older server
// (ADR-0134).
const (
	// sourceFormatVersion is the newest version this server writes and reads.
	// Version 2 carries the decision models this server keeps as files.
	sourceFormatVersion = 2
	// sourceFormatVersionReferencesOnly is version 1, in which decisions are
	// references alone. A tree that carries no model is still written in it: a
	// server that reads only version 1 reads such a tree whole, and would read a
	// version-2 tree by ignoring its models, which is the silent misreading the
	// version exists to prevent.
	sourceFormatVersionReferencesOnly = 1
)

// sourceManifest is atlas.json: what the application is, and what is in the tree.
type sourceManifest struct {
	FormatVersion int `json:"formatVersion"`
	// Key is the portable application key (ADR-0134): the identity that survives a
	// clone. The random per-server application id never appears here.
	Key       string           `json:"key"`
	Name      string           `json:"name"`
	Processes []sourceProcess  `json:"processes"`
	Forms     []sourceForm     `json:"forms"`
	Decisions []sourceDecision `json:"decisions"`
}

// sourceProcess indexes one BPMN draft. ID is the BPMN process id — the key the
// draft store uses — so an import restores the artifact under its own identity
// rather than one derived from a filename someone renamed.
type sourceProcess struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

// sourceForm indexes one form definition.
type sourceForm struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

// sourceDecision is a DMN reference in full: the manifest carries the model handle
// itself. Path names the model's file when the tree carries the model, and is empty
// when the model is not this server's to copy.
type sourceDecision struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ModelRef string `json:"modelRef"`
	Path     string `json:"path,omitempty"`
}

// sourceFile is one file in the tree. The tree is built and consumed in memory:
// the HTTP layer wraps it in a tar, and a later slice hands the same slice to a
// git worktree.
type sourceFile struct {
	Path string
	Data []byte
}

// manifestPath is the fixed name of the manifest at the root of the tree.
const manifestPath = "atlas.json"

// buildSourceTree renders an application and its artifacts as the curated source
// layout. It is a pure function of the records handed to it — no store, no clock,
// no server — so the serialization can be exercised directly, which is what makes
// the round-trip stability ADR-0134 promises a test rather than a hope.
//
// The caller passes only artifacts belonging to the application; filtering is the
// loader's job, not the serializer's. models holds, by handle, the models the tree
// carries; a reference whose handle it lacks travels as a reference alone.
func buildSourceTree(app project, drafts []draft, forms []form, refs []dmnRef, models map[string][]byte) ([]sourceFile, error) {
	man := sourceManifest{
		FormatVersion: sourceFormatVersionReferencesOnly,
		Key:           app.Key,
		Name:          app.Name,
		Processes:     []sourceProcess{},
		Forms:         []sourceForm{},
		Decisions:     []sourceDecision{},
	}
	if man.Key == "" {
		return nil, fmt.Errorf("application %q has no portable key", app.Name)
	}

	// Sorted by id throughout, so the same records always produce the same bytes:
	// a tree that reshuffled on every export would show up as a diff in git for no
	// reason a reviewer could act on.
	sorted := append([]draft(nil), drafts...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ProcessID < sorted[j].ProcessID })
	sortedForms := append([]form(nil), forms...)
	sort.Slice(sortedForms, func(i, j int) bool { return sortedForms[i].ID < sortedForms[j].ID })
	sortedRefs := append([]dmnRef(nil), refs...)
	sort.Slice(sortedRefs, func(i, j int) bool { return sortedRefs[i].ID < sortedRefs[j].ID })

	taken := map[string]bool{manifestPath: true}
	files := []sourceFile{}

	for _, d := range sorted {
		p := uniquePath(taken, "processes/"+slugify(d.Name, d.ProcessID)+".bpmn")
		man.Processes = append(man.Processes, sourceProcess{ID: d.ProcessID, Name: d.Name, Path: p})
		files = append(files, sourceFile{Path: p, Data: newlineTerminated([]byte(d.XML))})
	}
	for _, f := range sortedForms {
		p := uniquePath(taken, "forms/"+slugify(f.Name, f.ID)+".form.json")
		man.Forms = append(man.Forms, sourceForm{ID: f.ID, Name: f.Name, Path: p})
		files = append(files, sourceFile{Path: p, Data: newlineTerminated([]byte(f.Schema))})
	}
	modelPaths := map[string]string{}
	for _, rec := range sortedRefs {
		entry := sourceDecision{ID: rec.ID, Name: rec.Name, ModelRef: rec.ModelRef}
		if data, ok := models[rec.ModelRef]; ok {
			p, written := modelPaths[rec.ModelRef]
			if !written {
				p = uniquePath(taken, "decisions/"+slugify(rec.ModelRef, rec.Name)+".dmn")
				modelPaths[rec.ModelRef] = p
				// Verbatim, not newline-terminated: an import compares these bytes with
				// the model already under the handle, and a newline added here would
				// read there as a change to a model other decisions may share.
				files = append(files, sourceFile{Path: p, Data: data})
			}
			entry.Path = p
		}
		man.Decisions = append(man.Decisions, entry)
	}
	if len(modelPaths) > 0 {
		man.FormatVersion = sourceFormatVersion
	}

	// Indented, newline-terminated: the manifest is read in a diff like every other
	// file in the tree, so it is formatted for a human rather than for the wire.
	data, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("render manifest: %w", err)
	}
	return append([]sourceFile{{Path: manifestPath, Data: newlineTerminated(data)}}, files...), nil
}

// newlineTerminated appends the trailing newline a text file is expected to have.
// It is idempotent, which is what keeps the round trip byte-stable: content that
// came back from a tree already ends in one and is left exactly as it is.
func newlineTerminated(b []byte) []byte {
	if len(b) > 0 && b[len(b)-1] == '\n' {
		return b
	}
	return append(append([]byte(nil), b...), '\n')
}

// uniquePath disambiguates a path that two artifacts would otherwise share — two
// drafts may legitimately carry the same name — by appending a counter before the
// extension. Callers iterate in id order, so which one keeps the bare name is
// deterministic.
func uniquePath(taken map[string]bool, want string) string {
	p := want
	if !taken[p] {
		taken[p] = true
		return p
	}
	dir, base := path.Split(want)
	ext := ""
	// Keep compound extensions (".form.json") whole so the suffix lands before all
	// of it and the file still reads as the kind it is.
	if i := strings.Index(base, "."); i >= 0 {
		ext, base = base[i:], base[:i]
	}
	for n := 2; ; n++ {
		p = dir + base + "-" + strconv.Itoa(n) + ext
		if !taken[p] {
			taken[p] = true
			return p
		}
	}
}

// slugify turns a display name into a filename stem: lowercase ASCII, words
// joined by hyphens. German umlauts and ß are transliterated rather than dropped,
// so "Prüfung" becomes "pruefung" and not "pr-fung". Anything left that is not
// a letter or digit becomes a separator. When a name yields nothing usable the
// fallback (the artifact's id) is slugified instead, and failing that the stem is
// a fixed literal — a file must always have a name.
func slugify(name, fallback string) string {
	s := slugifyOne(name)
	if s == "" {
		s = slugifyOne(fallback)
	}
	if s == "" {
		s = "artifact"
	}
	return s
}

// slugifyOne is slugify's single-input half: it returns "" when nothing usable
// survives, leaving the choice of fallback to the caller.
func slugifyOne(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch r {
		case 'ä':
			b.WriteString("ae")
			dash = false
			continue
		case 'ö':
			b.WriteString("oe")
			dash = false
			continue
		case 'ü':
			b.WriteString("ue")
			dash = false
			continue
		case 'ß':
			b.WriteString("ss")
			dash = false
			continue
		}
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// parseSourceTree validates a tree and returns its manifest together with the
// file contents indexed by path. It refuses a tree it cannot read whole: a
// missing manifest, an unsupported format version, an artifact whose file is not
// in the tree. Being strict here is what keeps a partial import from happening at
// all — the same all-or-nothing posture the bundle import takes (ADR-0129).
func parseSourceTree(files []sourceFile) (sourceManifest, map[string][]byte, error) {
	byPath := make(map[string][]byte, len(files))
	for _, f := range files {
		byPath[f.Path] = f.Data
	}
	raw, ok := byPath[manifestPath]
	if !ok {
		return sourceManifest{}, nil, fmt.Errorf("tree has no %s", manifestPath)
	}
	var man sourceManifest
	if err := json.Unmarshal(raw, &man); err != nil {
		return sourceManifest{}, nil, fmt.Errorf("invalid %s: %w", manifestPath, err)
	}
	if man.FormatVersion < sourceFormatVersionReferencesOnly || man.FormatVersion > sourceFormatVersion {
		return sourceManifest{}, nil, fmt.Errorf("unsupported format version %d (this server reads %d to %d)",
			man.FormatVersion, sourceFormatVersionReferencesOnly, sourceFormatVersion)
	}
	if strings.TrimSpace(man.Key) == "" {
		return sourceManifest{}, nil, fmt.Errorf("%s has no application key", manifestPath)
	}
	if strings.TrimSpace(man.Name) == "" {
		return sourceManifest{}, nil, fmt.Errorf("%s has no application name", manifestPath)
	}
	for _, p := range man.Processes {
		if strings.TrimSpace(p.ID) == "" {
			return sourceManifest{}, nil, fmt.Errorf("%s: a process entry has no id", manifestPath)
		}
		if _, ok := byPath[p.Path]; !ok {
			return sourceManifest{}, nil, fmt.Errorf("process %q names %q, which is not in the tree", p.ID, p.Path)
		}
	}
	for _, f := range man.Forms {
		if strings.TrimSpace(f.ID) == "" {
			return sourceManifest{}, nil, fmt.Errorf("%s: a form entry has no id", manifestPath)
		}
		if _, ok := byPath[f.Path]; !ok {
			return sourceManifest{}, nil, fmt.Errorf("form %q names %q, which is not in the tree", f.ID, f.Path)
		}
	}
	modelPaths := map[string]string{}
	for _, d := range man.Decisions {
		if strings.TrimSpace(d.ID) == "" {
			return sourceManifest{}, nil, fmt.Errorf("%s: a decision entry has no id", manifestPath)
		}
		if d.Path == "" {
			continue
		}
		if !modelHandleSafe(d.ModelRef) {
			return sourceManifest{}, nil, fmt.Errorf("decision %q carries a model under %q, which is not a model handle", d.ID, d.ModelRef)
		}
		if _, ok := byPath[d.Path]; !ok {
			return sourceManifest{}, nil, fmt.Errorf("decision %q names %q, which is not in the tree", d.ID, d.Path)
		}
		if prev, ok := modelPaths[d.ModelRef]; ok && prev != d.Path {
			return sourceManifest{}, nil, fmt.Errorf("decision model %q is carried twice, as %q and %q", d.ModelRef, prev, d.Path)
		}
		modelPaths[d.ModelRef] = d.Path
	}
	return man, byPath, nil
}

// modelHandleSafe reports whether a handle names exactly one file in the model
// folder. A tree's handle is the name its model is written under, so it is held to
// more than the resolver's own traversal guard: nothing a file system on either
// platform reads as a path, a drive or a stream.
func modelHandleSafe(h string) bool {
	if h == "" || h != strings.TrimSpace(h) || h == "." || h == ".." {
		return false
	}
	return !strings.ContainsFunc(h, func(r rune) bool {
		return r < 0x20 || strings.ContainsRune(`/\:*?"<>|`, r)
	})
}

// deriveApplicationKey mints a portable key for an application from its name.
// Keys are unique per server, so a name two applications share resolves to
// distinct keys ("onboarding", "onboarding-2") and the identity stays one-to-one.
//
// The key is derived once and then kept: renaming an application must not change
// it, because it is the identity a clone is matched on. That is why every caller
// checks for an existing key before asking for one.
//
// Runs on the run loop (the project store is loop-owned).
func (s *Server) deriveApplicationKey(app project) (string, error) {
	all, err := s.projects.LoadAll()
	if err != nil {
		return "", err
	}
	taken := map[string]bool{}
	for _, p := range all {
		if p.Key != "" && p.ID != app.ID {
			taken[p.Key] = true
		}
	}
	base := slugify(app.Name, app.ID)
	key := base
	for n := 2; taken[key]; n++ {
		key = base + "-" + strconv.Itoa(n)
	}
	return key, nil
}

// applicationKeyFor returns the application's portable key, deriving and saving
// one when it has none.
//
// Applications created since ADR-0134 get their key at creation; this is the path
// for every application that predates it. Backfilling lazily rather than in a
// startup migration means no pass over the store at boot and no record rewritten
// before something actually needs it.
//
// Runs on the run loop.
func (s *Server) applicationKeyFor(app project) (string, error) {
	if app.Key != "" {
		return app.Key, nil
	}
	key, err := s.deriveApplicationKey(app)
	if err != nil {
		return "", err
	}
	app.Key = key
	app.UpdatedAt = time.Now().Unix()
	if err := s.projects.Save(app); err != nil {
		return "", err
	}
	s.noteApplication(app)
	return key, nil
}

// exportApplicationSource loads an application's artifacts and renders the tree.
// Runs on the run loop.
func (s *Server) exportApplicationSource(app project) ([]sourceFile, error) {
	key, err := s.applicationKeyFor(app)
	if err != nil {
		return nil, err
	}
	app.Key = key

	drafts, err := s.drafts.LoadAll()
	if err != nil {
		return nil, err
	}
	forms, err := s.forms.LoadAll()
	if err != nil {
		return nil, err
	}
	refs, err := s.dmnrefs.LoadAll()
	if err != nil {
		return nil, err
	}
	mine := []draft{}
	for _, d := range drafts {
		if d.ProjectID == app.ID {
			mine = append(mine, d)
		}
	}
	myForms := []form{}
	for _, f := range forms {
		if f.ProjectID == app.ID {
			myForms = append(myForms, f)
		}
	}
	myRefs := []dmnRef{}
	for _, rec := range refs {
		if rec.ProjectID == app.ID {
			myRefs = append(myRefs, rec)
		}
	}
	models, err := s.localDecisionModels(myRefs)
	if err != nil {
		return nil, err
	}
	return buildSourceTree(app, mine, myForms, myRefs, models)
}

// localDecisionModels reads, by handle, the model behind each reference when this
// server keeps its models in its own folder. With a remote temis service behind the
// resolver it reads nothing: the model is temis's, and the export does not reach
// out to copy it. A handle that resolves to nothing is left out — the reference is
// broken here already, and travels as the reference it is.
//
// Runs on the run loop. It reads local files only, one per model the application
// references, which is the cost of the store reads beside it.
func (s *Server) localDecisionModels(refs []dmnRef) (map[string][]byte, error) {
	if _, local := s.dmnModelDir(); !local {
		return nil, nil
	}
	models := map[string][]byte{}
	for _, rec := range refs {
		if _, done := models[rec.ModelRef]; done || !modelHandleSafe(rec.ModelRef) {
			continue
		}
		data, err := s.dmnResolver.Resolve(context.Background(), rec.ModelRef)
		switch {
		case errors.Is(err, dmn.ErrNotFound):
			continue
		case err != nil:
			return nil, err
		}
		models[rec.ModelRef] = data
	}
	return models, nil
}

// checkSourceModels compiles every model a tree carries, once per file, before
// anything is written: a model that does not compile, stored under a handle every
// reference and every publish resolves, would break each of them at once. It runs
// off the run loop, because compiling is CPU work the single writer must not wait
// on.
func (s *Server) checkSourceModels(ctx context.Context, man sourceManifest, byPath map[string][]byte) error {
	checked := map[string]bool{}
	for _, d := range man.Decisions {
		if d.Path == "" || checked[d.Path] {
			continue
		}
		checked[d.Path] = true
		if res := s.dmnValidator.ValidateXML(ctx, byPath[d.Path]); !res.Valid {
			return fmt.Errorf("decision model %q (%s) is not a valid DMN model: %s", d.ModelRef, d.Path, res.Message)
		}
	}
	return nil
}

// sourceImportResult reports what an import did, in the terms an operator asked
// the question in: which application it landed in, whether that application had
// to be created, and what became of each artifact the tree carries.
type sourceImportResult struct {
	ApplicationID string `json:"applicationId"`
	Key           string `json:"key"`
	Name          string `json:"name"`
	Created       bool   `json:"created"`
	// DryRun says nothing was written: the answer is the plan, with the outcome
	// each artifact would have under the decisions the request gave.
	DryRun    bool `json:"dryRun,omitempty"`
	Processes int  `json:"processes"`
	Forms     int  `json:"forms"`
	Decisions int  `json:"decisions"`
	// Models counts the decision models the tree carried that are, or would be,
	// under their handles as the tree has them: written, or already identical.
	Models int `json:"models"`
	// ModelsSkipped names the handles of models the tree carried and this server did
	// not write, because its models come from a remote temis service. The references
	// are in; whether temis has the models is temis's to answer.
	ModelsSkipped []string `json:"modelsSkipped,omitempty"`
	// Artifacts is every artifact of the tree, the application itself first, with
	// how it compares to this server's and what the import did with it. A ref is
	// application:<key>, process:<id>, form:<id>, decision:<id> or model:<handle>.
	Artifacts []importplan.Artifact `json:"artifacts"`
	// Untracked names local artifacts that stayed behind because the tree does not
	// mention them. An import never deletes: a tree is not proof that an artifact
	// was removed on purpose (it may simply have been written by an older server),
	// and silently dropping a diagram is not a mistake anyone can undo. Naming them
	// leaves the decision with the operator.
	Untracked []string `json:"untracked"`
}

// sourceRefusal is an import the server declines on the caller's behalf rather
// than fails at: the tree is readable, but applying it would take something that
// is not the caller's or is not the tree's to take. It carries the HTTP status
// the handler should report, so the reason survives the trip out.
type sourceRefusal struct {
	status int
	msg    string
}

func (e sourceRefusal) Error() string { return e.msg }

// sourceUndecided is an import stopped because artifacts it would change were
// decided neither way. It carries the plan, so the caller can show it and ask.
type sourceUndecided struct {
	plan sourceImportResult
	refs []string
}

func (e sourceUndecided) Error() string {
	return fmt.Sprintf("%d artifacts differ from this server's and were not decided (%s); name each under overwrite or keep",
		len(e.refs), strings.Join(e.refs, ", "))
}

// applySourceTree writes a parsed tree into the stores. It resolves the
// application by the manifest's portable key — creating it when this server has
// never seen it — authorizes the caller against whatever it resolved, compares
// every artifact the manifest lists with this server's, and writes what is new
// and what the caller decided to overwrite.
//
// It is idempotent: an artifact that is already as the tree has it is not written
// again, so importing the same tree twice changes nothing the second time. And it
// never replaces work silently: an artifact that differs from this server's is
// written only when the caller named it under overwrite, kept when named under
// keep, and stops the import, with the plan, when named under neither.
//
// It refuses rather than guesses in five cases, all of which mean the tree and
// this server disagree about who owns something:
//
//   - the caller may not write the application the key resolves to;
//   - the key names a protected system project (ADR-0122), which no import may
//     write into;
//   - a process id in the tree is a draft belonging to a *different* application;
//   - a form or decision id in the tree belongs to a different application;
//   - a model the tree carries would replace a different model under a handle that
//     a decision outside the application resolves.
//
// Adopting such an artifact would move it out of an application somebody else is
// working in, which is exactly the kind of silent damage ADR-0134 refuses when it
// refuses to merge. An *ungrouped* artifact of the same id is adopted rather than
// refused: it belongs to nobody, and the tree naming it is the closest thing to a
// claim there is. Its content is still compared, and decided, like any other.
//
// Resolution, authorization, the plan and writing all happen in this one call so
// they happen under one turn of the single writer: a plan checked against a store
// a concurrent request then changed is the gap a two-step version would leave.
//
// Runs on the run loop.
func (s *Server) applySourceTree(man sourceManifest, byPath map[string][]byte, ownerID string, authorize func(project) (int, string), decide importplan.Decisions) (sourceImportResult, error) {
	all, err := s.projects.LoadAll()
	if err != nil {
		return sourceImportResult{}, err
	}
	var app project
	found := false
	for _, p := range all {
		if p.Key != "" && p.Key == man.Key {
			app, found = p, true
			break
		}
	}
	now := time.Now().Unix()
	res := sourceImportResult{Key: man.Key, DryRun: decide.DryRun, Artifacts: []importplan.Artifact{}, Untracked: []string{}}
	if !found {
		id, err := newID()
		if err != nil {
			return sourceImportResult{}, err
		}
		app = project{
			ID: id, Name: man.Name, Key: man.Key, OwnerID: ownerID,
			Visibility: VisibilityPrivate, CreatedAt: now, UpdatedAt: now,
		}
		res.Created = true
	} else {
		if app.Protected {
			return sourceImportResult{}, sourceRefusal{http.StatusForbidden, "protected system project cannot be modified"}
		}
		if code, msg := authorize(app); code != 0 {
			return sourceImportResult{}, sourceRefusal{code, msg}
		}
	}

	// Ownership conflicts are checked across every artifact before anything is
	// written, so a refused import leaves the stores exactly as it found them.
	drafts, err := s.drafts.LoadAll()
	if err != nil {
		return sourceImportResult{}, err
	}
	draftOwner := map[string]draft{}
	for _, d := range drafts {
		draftOwner[d.ProcessID] = d
	}
	forms, err := s.forms.LoadAll()
	if err != nil {
		return sourceImportResult{}, err
	}
	formOwner := map[string]form{}
	for _, f := range forms {
		formOwner[f.ID] = f
	}
	refs, err := s.dmnrefs.LoadAll()
	if err != nil {
		return sourceImportResult{}, err
	}
	refOwner := map[string]dmnRef{}
	for _, rec := range refs {
		refOwner[rec.ID] = rec
	}

	for _, p := range man.Processes {
		if d, ok := draftOwner[p.ID]; ok && d.ProjectID != "" && d.ProjectID != app.ID {
			return sourceImportResult{}, sourceRefusal{http.StatusConflict, fmt.Sprintf("process %q already belongs to another application", p.ID)}
		}
	}
	for _, f := range man.Forms {
		if rec, ok := formOwner[f.ID]; ok && rec.ProjectID != "" && rec.ProjectID != app.ID {
			return sourceImportResult{}, sourceRefusal{http.StatusConflict, fmt.Sprintf("form %q already belongs to another application", f.ID)}
		}
	}
	for _, d := range man.Decisions {
		if rec, ok := refOwner[d.ID]; ok && rec.ProjectID != "" && rec.ProjectID != app.ID {
			return sourceImportResult{}, sourceRefusal{http.StatusConflict, fmt.Sprintf("decision %q already belongs to another application", d.ID)}
		}
	}

	// The plan: every artifact compared with this server's, and what the decisions
	// make of it. writes holds what is to be written, by ref.
	writes := map[string]bool{}
	var undecided []string
	note := func(ref, name, status string, regroup bool) {
		outcome := decide.Outcome(ref, status)
		// An ungrouped artifact the tree names joins the application even when its
		// content is already the tree's: that moves nobody's work, it files it.
		if importplan.Writes(outcome) || outcome == importplan.Unchanged && regroup {
			writes[ref] = true
		}
		if outcome == importplan.Undecided {
			undecided = append(undecided, ref)
		}
		res.Artifacts = append(res.Artifacts, importplan.Artifact{Ref: ref, Name: name, Status: status, Outcome: outcome})
	}
	appStatus := importplan.New
	if found {
		appStatus = importplan.Compare(app.Name == man.Name)
	}
	note("application:"+man.Key, man.Name, appStatus, false)
	for _, p := range man.Processes {
		d, ok := draftOwner[p.ID]
		status := importplan.New
		if ok {
			status = importplan.Compare(d.Name == p.Name && bytes.Equal(newlineTerminated([]byte(d.XML)), byPath[p.Path]))
		}
		note("process:"+p.ID, p.Name, status, ok && d.ProjectID == "")
	}
	for _, f := range man.Forms {
		rec, ok := formOwner[f.ID]
		status := importplan.New
		if ok {
			status = importplan.Compare(rec.Name == f.Name && bytes.Equal(newlineTerminated([]byte(rec.Schema)), byPath[f.Path]))
		}
		note("form:"+f.ID, f.Name, status, ok && rec.ProjectID == "")
	}
	for _, d := range man.Decisions {
		rec, ok := refOwner[d.ID]
		status := importplan.New
		if ok {
			status = importplan.Compare(rec.Name == d.Name && rec.ModelRef == d.ModelRef)
		}
		note("decision:"+d.ID, d.Name, status, ok && rec.ProjectID == "")
	}
	models, err := s.planSourceModels(man, byPath)
	if err != nil {
		return sourceImportResult{}, err
	}
	res.ModelsSkipped = models.skipped
	for _, m := range models.models {
		note("model:"+m.handle, m.handle, m.status, false)
		if outcome := res.Artifacts[len(res.Artifacts)-1].Outcome; outcome != importplan.Kept && outcome != importplan.Undecided {
			res.Models++
		}
	}
	res.Processes, res.Forms, res.Decisions = len(man.Processes), len(man.Forms), len(man.Decisions)
	res.ApplicationID, res.Name = app.ID, man.Name
	if found && !writes["application:"+man.Key] {
		res.Name = app.Name
	}
	if res.Created {
		// A plan for an application this server has never seen names no id: none is
		// taken until it is written.
		if decide.DryRun {
			res.ApplicationID = ""
		}
	}
	res.Untracked = untrackedArtifacts(app.ID, man, drafts, forms, refs)

	// A model replaced under a handle that a decision outside the application
	// resolves would change that decision without its author knowing; only a model
	// that is actually to be written is held to it, so keeping the local model is
	// always a way through.
	inTree := map[string]bool{}
	for _, d := range man.Decisions {
		inTree[d.ID] = true
	}
	for _, m := range models.models {
		if m.status != importplan.Changed || !writes["model:"+m.handle] {
			continue
		}
		for _, rec := range refs {
			if rec.ModelRef == m.handle && !inTree[rec.ID] && rec.ProjectID != app.ID {
				return sourceImportResult{}, sourceRefusal{http.StatusConflict, fmt.Sprintf(
					"decision model %q is stored here with other content, and a decision outside this application uses it; keep this server's model, or rename the model in the tree or on this server", m.handle)}
			}
		}
	}
	if decide.DryRun {
		return res, nil
	}
	if len(undecided) > 0 {
		return res, sourceUndecided{plan: res, refs: undecided}
	}

	// The models go first: a model that cannot be written then leaves no reference
	// behind pointing at it.
	for _, m := range models.models {
		if writes["model:"+m.handle] {
			if err := writeModelInPlace(models.dir, m.handle, m.data); err != nil {
				return sourceImportResult{}, fmt.Errorf("write decision model %q: %w", m.handle, err)
			}
		}
	}
	if !found {
		if err := s.projects.Save(app); err != nil {
			return sourceImportResult{}, err
		}
		s.noteApplication(app)
	} else if writes["application:"+man.Key] {
		// The repository is the source of truth for the application's name, the same
		// as for its artifacts; the key, not the name, is what identifies it.
		app.Name, app.UpdatedAt = man.Name, now
		if err := s.projects.Save(app); err != nil {
			return sourceImportResult{}, err
		}
		s.noteApplication(app)
	}

	for _, p := range man.Processes {
		if !writes["process:"+p.ID] {
			continue
		}
		rec := draft{
			ProcessID: p.ID, Name: p.Name, ProjectID: app.ID,
			SavedAt: now, SavedBy: ownerID, XML: string(byPath[p.Path]),
		}
		if err := s.drafts.Save(rec); err != nil {
			return sourceImportResult{}, err
		}
	}
	for _, f := range man.Forms {
		if !writes["form:"+f.ID] {
			continue
		}
		rec := form{
			ID: f.ID, Name: f.Name, ProjectID: app.ID,
			SavedAt: now, Schema: string(byPath[f.Path]),
		}
		if err := s.forms.Save(rec); err != nil {
			return sourceImportResult{}, err
		}
	}
	for _, d := range man.Decisions {
		if !writes["decision:"+d.ID] {
			continue
		}
		rec := dmnRef{ID: d.ID, Name: d.Name, ModelRef: d.ModelRef, ProjectID: app.ID, CreatedAt: now}
		// Keep the original creation time so the Modeler's ordering does not jump
		// every time a repository is re-imported.
		if prev, ok := refOwner[d.ID]; ok && prev.CreatedAt != 0 {
			rec.CreatedAt = prev.CreatedAt
		}
		if err := s.dmnrefs.Save(rec); err != nil {
			return sourceImportResult{}, err
		}
	}
	return res, nil
}

// untrackedArtifacts names what is filed in the application here and not in the
// tree, sorted.
func untrackedArtifacts(appID string, man sourceManifest, drafts []draft, forms []form, refs []dmnRef) []string {
	inTree := map[string]bool{}
	for _, p := range man.Processes {
		inTree["process:"+p.ID] = true
	}
	for _, f := range man.Forms {
		inTree["form:"+f.ID] = true
	}
	for _, d := range man.Decisions {
		inTree["decision:"+d.ID] = true
	}
	out := []string{}
	for _, d := range drafts {
		if d.ProjectID == appID && !inTree["process:"+d.ProcessID] {
			out = append(out, "process:"+d.ProcessID)
		}
	}
	for _, f := range forms {
		if f.ProjectID == appID && !inTree["form:"+f.ID] {
			out = append(out, "form:"+f.ID)
		}
	}
	for _, rec := range refs {
		if rec.ProjectID == appID && !inTree["decision:"+rec.ID] {
			out = append(out, "decision:"+rec.ID)
		}
	}
	sort.Strings(out)
	return out
}

// sourceModelPlan is how the models a tree carries compare with this server's.
type sourceModelPlan struct {
	dir     string
	models  []sourceModel
	skipped []string
}

type sourceModel struct {
	handle string
	data   []byte
	status string
}

// planSourceModels compares each model the tree carries, by handle, with the model
// stored under that handle. A server whose models come from a remote temis service
// writes none, and names them as skipped.
//
// Runs on the run loop, beside the store reads it extends; it reads one local file
// per model.
func (s *Server) planSourceModels(man sourceManifest, byPath map[string][]byte) (sourceModelPlan, error) {
	var handles []string
	pathOf := map[string]string{}
	for _, d := range man.Decisions {
		if d.Path == "" {
			continue
		}
		if _, seen := pathOf[d.ModelRef]; !seen {
			handles = append(handles, d.ModelRef)
		}
		pathOf[d.ModelRef] = d.Path
	}
	sort.Strings(handles)
	var plan sourceModelPlan
	if len(handles) == 0 {
		return plan, nil
	}
	dir, local := s.dmnModelDir()
	if !local {
		plan.skipped = handles
		return plan, nil
	}
	plan.dir = dir
	for _, h := range handles {
		m := sourceModel{handle: h, data: byPath[pathOf[h]], status: importplan.New}
		existing, err := s.dmnResolver.Resolve(context.Background(), h)
		switch {
		case errors.Is(err, dmn.ErrNotFound):
		case err != nil:
			return sourceModelPlan{}, err
		default:
			m.status = importplan.Compare(bytes.Equal(existing, m.data))
		}
		plan.models = append(plan.models, m)
	}
	return plan, nil
}
