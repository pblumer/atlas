package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// The curated source layout (ADR-0134 Phase 4): an application's *source* —
// drafts, forms, and DMN references — rendered as a small tree of files a human
// can read, diff, and review, rather than the sidecar JSON the stores keep on
// disk.
//
//	atlas.json                                # manifest: key, name, format version, index
//	processes/mitarbeiter-onboarding.bpmn     # the draft's XML, verbatim
//	forms/neuer-mitarbeiter.form.json         # the form-js schema, verbatim
//
// Two things are worth stating because they refine the illustrative tree in
// ADR-0134:
//
//   - **The manifest is the index.** Every artifact appears there with its id,
//     its name, and the path of its file. The files themselves hold nothing but
//     native content, so a `.bpmn` opens in any modeler and a `.form.json` opens
//     in any form editor — neither carries an Atlas-specific wrapper, and neither
//     has to be parsed to learn which artifact it is.
//   - **DMN references have no file.** Atlas stores a reference to a model
//     authored in temis, never the model itself (ADR-0014/0034), so there is no
//     `.dmn` content to write; a reference is a name plus a handle, which is
//     manifest data. Emitting a `decisions/*.dmn` would mean copying a
//     temis-owned model into the repository and pretending Atlas owns it.
//
// Deployments and releases deliberately do not travel: they are records of what
// happened on one server, not source an author edits (ADR-0134).
//
// This is design-time state throughout. Nothing here enters the event log, and
// none of it affects recovery (ADR-0002).

// sourceFormatVersion is the layout's format version, carried in every manifest.
// A second serialization can drift from the records it mirrors; the version is
// what lets a later reader say so out loud instead of silently misreading a tree
// written by an older server (ADR-0134).
const sourceFormatVersion = 1

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

// sourceDecision is a DMN reference in full: it has no file, so the manifest
// carries the temis model handle itself.
type sourceDecision struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ModelRef string `json:"modelRef"`
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
// loader's job, not the serializer's.
func buildSourceTree(app project, drafts []draft, forms []form, refs []dmnRef) ([]sourceFile, error) {
	man := sourceManifest{
		FormatVersion: sourceFormatVersion,
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
	for _, rec := range sortedRefs {
		man.Decisions = append(man.Decisions, sourceDecision{ID: rec.ID, Name: rec.Name, ModelRef: rec.ModelRef})
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
	if man.FormatVersion != sourceFormatVersion {
		return sourceManifest{}, nil, fmt.Errorf("unsupported format version %d (this server reads %d)", man.FormatVersion, sourceFormatVersion)
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
	for _, d := range man.Decisions {
		if strings.TrimSpace(d.ID) == "" {
			return sourceManifest{}, nil, fmt.Errorf("%s: a decision entry has no id", manifestPath)
		}
	}
	return man, byPath, nil
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
	all, err := s.projects.loadAll()
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
	if err := s.projects.save(app); err != nil {
		return "", err
	}
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

	drafts, err := s.drafts.loadAll()
	if err != nil {
		return nil, err
	}
	forms, err := s.forms.loadAll()
	if err != nil {
		return nil, err
	}
	refs, err := s.dmnrefs.loadAll()
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
	return buildSourceTree(app, mine, myForms, myRefs)
}

// sourceImportResult reports what an import did, in the terms an operator asked
// the question in: which application it landed in, whether that application had
// to be created, and how many artifacts of each kind it wrote.
type sourceImportResult struct {
	ApplicationID string `json:"applicationId"`
	Key           string `json:"key"`
	Name          string `json:"name"`
	Created       bool   `json:"created"`
	Processes     int    `json:"processes"`
	Forms         int    `json:"forms"`
	Decisions     int    `json:"decisions"`
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

// applySourceTree writes a parsed tree into the stores. It resolves the
// application by the manifest's portable key — creating it when this server has
// never seen it — authorizes the caller against whatever it resolved, and then
// upserts every artifact the manifest lists.
//
// It refuses rather than guesses in four cases, all of which mean the tree and
// this server disagree about who owns something:
//
//   - the caller may not write the application the key resolves to;
//   - the key names a protected system project (ADR-0122), which no import may
//     write into;
//   - a process id in the tree is a draft belonging to a *different* application;
//   - a form or decision id in the tree belongs to a different application.
//
// Adopting such an artifact would move it out of an application somebody else is
// working in, which is exactly the kind of silent damage ADR-0134 refuses when it
// refuses to merge. An *ungrouped* artifact of the same id is adopted rather than
// refused: it belongs to nobody, and the tree naming it is the closest thing to a
// claim there is.
//
// Resolution, authorization, and writing all happen in this one call so they
// happen under one turn of the single writer: authorizing against a project that
// a concurrent request then re-owned is the gap a two-step version would leave.
//
// Runs on the run loop.
func (s *Server) applySourceTree(man sourceManifest, byPath map[string][]byte, ownerID string, authorize func(project) (int, string)) (sourceImportResult, error) {
	all, err := s.projects.loadAll()
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
	res := sourceImportResult{Key: man.Key, Untracked: []string{}}
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
	drafts, err := s.drafts.loadAll()
	if err != nil {
		return sourceImportResult{}, err
	}
	draftOwner := map[string]draft{}
	for _, d := range drafts {
		draftOwner[d.ProcessID] = d
	}
	forms, err := s.forms.loadAll()
	if err != nil {
		return sourceImportResult{}, err
	}
	formOwner := map[string]form{}
	for _, f := range forms {
		formOwner[f.ID] = f
	}
	refs, err := s.dmnrefs.loadAll()
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

	if !found {
		if err := s.projects.save(app); err != nil {
			return sourceImportResult{}, err
		}
	} else if app.Name != man.Name {
		// The repository is the source of truth for the application's name, the same
		// as for its artifacts; the key, not the name, is what identifies it.
		app.Name, app.UpdatedAt = man.Name, now
		if err := s.projects.save(app); err != nil {
			return sourceImportResult{}, err
		}
	}
	res.ApplicationID, res.Name = app.ID, app.Name

	for _, p := range man.Processes {
		rec := draft{
			ProcessID: p.ID, Name: p.Name, ProjectID: app.ID,
			SavedAt: now, XML: string(byPath[p.Path]),
		}
		if err := s.drafts.save(rec); err != nil {
			return sourceImportResult{}, err
		}
		res.Processes++
	}
	for _, f := range man.Forms {
		rec := form{
			ID: f.ID, Name: f.Name, ProjectID: app.ID,
			SavedAt: now, Schema: string(byPath[f.Path]),
		}
		if err := s.forms.save(rec); err != nil {
			return sourceImportResult{}, err
		}
		res.Forms++
	}
	for _, d := range man.Decisions {
		rec := dmnRef{ID: d.ID, Name: d.Name, ModelRef: d.ModelRef, ProjectID: app.ID, CreatedAt: now}
		// Keep the original creation time so the Modeler's ordering does not jump
		// every time a repository is re-imported.
		if prev, ok := refOwner[d.ID]; ok && prev.CreatedAt != 0 {
			rec.CreatedAt = prev.CreatedAt
		}
		if err := s.dmnrefs.save(rec); err != nil {
			return sourceImportResult{}, err
		}
		res.Decisions++
	}

	// Anything still tagged into the application that the tree never mentioned.
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
	for _, d := range drafts {
		if d.ProjectID == app.ID && !inTree["process:"+d.ProcessID] {
			res.Untracked = append(res.Untracked, "process:"+d.ProcessID)
		}
	}
	for _, f := range forms {
		if f.ProjectID == app.ID && !inTree["form:"+f.ID] {
			res.Untracked = append(res.Untracked, "form:"+f.ID)
		}
	}
	for _, rec := range refs {
		if rec.ProjectID == app.ID && !inTree["decision:"+rec.ID] {
			res.Untracked = append(res.Untracked, "decision:"+rec.ID)
		}
	}
	sort.Strings(res.Untracked)
	return res, nil
}

// keyIsFree reports whether no application other than exceptID answers to a key.
// It is what stops an import from pointing two applications at one identity, which
// would make the next clone ambiguous.
//
// Runs on the run loop.
func (s *Server) keyIsFree(key, exceptID string) (bool, error) {
	all, err := s.projects.loadAll()
	if err != nil {
		return false, err
	}
	for _, p := range all {
		if p.Key == key && p.ID != exceptID {
			return false, nil
		}
	}
	return true, nil
}
