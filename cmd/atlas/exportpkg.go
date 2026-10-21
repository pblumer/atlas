package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// `atlas export` writes a package from a running atlas: the directory `atlas
// import` installs (ADR-0437), so what was built on one installation reaches
// another with one command on each side
// (ADR-0467).
//
//	atlas.json, *.bpmn, *.form.json, *.dmn  the application's source (ADR-0134)
//	konstanten.json                         its constants, as this server holds them
//	katalog.json                            the catalogues named with --catalog
//	fragen.json                             one question per group and person they name
//	antworten.json                          the answers, as this server names them
//
// The server gains nothing for it: the source comes from the source export, the
// shop from the catalogue export, the names from the directory, all under the
// authority each already asks. What the command adds is what turns their answers
// into a package another installation can take:
//
//   - Every group and person the catalogues name becomes a {{question}}. Its id is
//     this server's and means nothing on the next; its name is the same on both, so
//     the answer is the name, and `atlas import` finds it in the target's directory.
//   - A process a product is bound to travels once: in the application when the
//     application holds it, else in katalog.json, which the catalogue import
//     deploys.
//   - What cannot travel is named, with what it will do on the target, rather than
//     left for the import to discover there.

const packageAnswers = "antworten.json"

// maxExportEntries bounds the files a source archive may unpack to, as the server
// bounds what it reads back.
const maxExportEntries = 2000

// repeatedFlag collects a flag given more than once, each value once.
type repeatedFlag []string

func (r *repeatedFlag) String() string { return strings.Join(*r, ",") }

func (r *repeatedFlag) Set(v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return errors.New("an empty value")
	}
	if !slices.Contains(*r, v) {
		*r = append(*r, v)
	}
	return nil
}

func runExport(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	server := fs.String("server", "http://localhost:8080", "base URL of the atlas server to export from")
	token := fs.String("token", os.Getenv("ATLAS_TOKEN"),
		"bearer token, when the server requires authentication (or ATLAS_TOKEN); it needs the modeler role for the application and productmanager, maintaining each catalogue, for the shop")
	application := fs.String("application", "", "the application to export: its key, its id, or a name only it carries")
	var catalogs repeatedFlag
	fs.Var(&catalogs, "catalog", "a catalogue to export with the application, by id; repeatable")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("name the directory: atlas export --application KEY [flags] DIR")
	}
	if strings.TrimSpace(*application) == "" {
		return errors.New("name the application: atlas export --application KEY [flags] DIR")
	}
	dir := fs.Arg(0)

	// The directory first: one that is somebody else's is refused before the server
	// is asked anything.
	earlier, err := readEarlierPackage(dir)
	if err != nil {
		return err
	}

	c := &exportClient{apiClient: newAPIClient(*server, *token)}
	app, err := c.application(*application)
	if err != nil {
		return err
	}
	files, err := c.source(app.ID)
	if err != nil {
		return err
	}
	man, err := exportManifestOf(files)
	if err != nil {
		return err
	}
	// The constants as this server holds them, as a suggestion: the import sets
	// those the target lacks and asks before it replaces one the target holds
	// otherwise — INT and PROD rightly differ.
	held, err := c.constants(app.ID)
	if err != nil {
		return err
	}
	if len(held.Constants) > 0 {
		sort.Slice(held.Constants, func(i, j int) bool { return held.Constants[i].Name < held.Constants[j].Name })
		data, err := encodeJSON(constantsFile{Constants: held.Constants})
		if err != nil {
			return err
		}
		files = append(files, sourceEntry{name: packageConstants, data: data})
	}

	var s shop
	if len(catalogs) > 0 {
		if s, err = c.shop(catalogs, man, earlier.questions); err != nil {
			return err
		}
		files = append(files, sourceEntry{name: packageCatalogue, data: s.document})
		if len(s.questions) > 0 {
			q, err := encodeJSON(s.questions)
			if err != nil {
				return err
			}
			a, err := encodeJSON(s.answers)
			if err != nil {
				return err
			}
			files = append(files, sourceEntry{name: packageQuestions, data: q}, sourceEntry{name: packageAnswers, data: a})
		}
	}
	if err := writePackage(dir, earlier, files); err != nil {
		return err
	}

	fmt.Fprintf(out, "application %q (%s): %d processes, %d forms, %d decisions, %d decision models\n",
		man.Name, man.Key, len(man.Processes), len(man.Forms), len(man.Decisions), man.models())
	if len(held.Constants) > 0 {
		fmt.Fprintf(out, "constants: %d in %s, as this server holds them; the import sets those the target lacks and asks before it replaces one it holds otherwise\n",
			len(held.Constants), packageConstants)
	}
	// What the processes read from the installation and no package carries: the
	// target has to have it before the application runs there as it does here.
	carried := map[string]bool{}
	for _, k := range held.Constants {
		carried[k.Name] = true
	}
	var needs []packageNeed
	for _, n := range man.Needs {
		if !(n.Kind == "application-constant" && carried[n.Name]) {
			needs = append(needs, n)
		}
	}
	if len(needs) > 0 {
		fmt.Fprintln(out, "the target needs, and the package does not carry:")
		for _, n := range needs {
			fmt.Fprintf(out, "  %s, read by %s: %s\n", n.what(), strings.Join(n.Processes, ", "), n.where())
		}
	}
	install := "atlas import --server <target> " + dir
	if len(catalogs) == 0 {
		fmt.Fprintln(out, "no catalogue: the package installs the application alone")
	} else {
		publish := "not published on import"
		if s.publish {
			publish = "published on import"
		}
		fmt.Fprintf(out, "catalogues: %s (%d products, %s)\n", strings.Join(catalogs, ", "), s.products, publish)
		for _, n := range s.notes {
			fmt.Fprintf(out, "  %s\n", n)
		}
		if len(s.questions) > 0 {
			fmt.Fprintf(out, "questions: %d, answered in %s with the names this server gives them\n", len(s.questions), packageAnswers)
			install = "atlas import --server <target> --answers " + filepath.Join(dir, packageAnswers) + " " + dir
		}
		for _, w := range s.warnings {
			fmt.Fprintf(out, "  warning: %s\n", w)
		}
	}
	fmt.Fprintf(out, "wrote %s\ninstall it with: %s\n", dir, install)
	return nil
}

// exportManifest is what the command reads of the atlas.json the server wrote:
// which artifacts the application holds, and the files they are in.
type exportManifest struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Processes []struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	} `json:"processes"`
	Forms []struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	} `json:"forms"`
	Decisions []struct {
		ID       string `json:"id"`
		ModelRef string `json:"modelRef"`
		Path     string `json:"path"`
	} `json:"decisions"`
	// Needs is what the processes read from the installation, as the server read it
	// from its drafts.
	Needs []packageNeed `json:"needs"`
}

// paths are the files the manifest names, each once.
func (m exportManifest) paths() []string {
	var out []string
	add := func(p string) {
		if p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	for _, p := range m.Processes {
		add(p.Path)
	}
	for _, f := range m.Forms {
		add(f.Path)
	}
	for _, d := range m.Decisions {
		add(d.Path)
	}
	return out
}

// models counts the decision models the source carries, one per file.
func (m exportManifest) models() int {
	files := map[string]bool{}
	for _, d := range m.Decisions {
		if d.Path != "" {
			files[d.Path] = true
		}
	}
	return len(files)
}

func exportManifestOf(files []sourceEntry) (exportManifest, error) {
	var man exportManifest
	for _, f := range files {
		if f.name == packageManifest {
			if err := json.Unmarshal(f.data, &man); err != nil {
				return man, fmt.Errorf("the server's %s: %w", packageManifest, err)
			}
			return man, nil
		}
	}
	return man, fmt.Errorf("the server's source archive has no %s", packageManifest)
}

// earlierPackage is what an earlier export left in the directory: the files it
// wrote, so this one removes those it no longer writes, and the questions, whose
// wording somebody may have improved since.
type earlierPackage struct {
	files     []string
	questions map[string]packageQuestion
}

// readEarlierPackage reads the directory an export is to write. Absent or empty, it
// is the export's. Holding a package, it is an earlier export's — the way a package
// is kept in git — and is updated. Holding anything else, it is somebody's, and the
// export is refused rather than mixed into it.
func readEarlierPackage(dir string) (earlierPackage, error) {
	info, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return earlierPackage{}, nil
	}
	if err != nil {
		return earlierPackage{}, err
	}
	if !info.IsDir() {
		return earlierPackage{}, fmt.Errorf("%s is not a directory", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return earlierPackage{}, err
	}
	if len(entries) == 0 {
		return earlierPackage{}, nil
	}
	raw, err := os.ReadFile(filepath.Join(dir, packageManifest))
	if errors.Is(err, os.ErrNotExist) {
		return earlierPackage{}, fmt.Errorf("%s is not empty and holds no package (no %s); export into an empty directory or into one an export wrote", dir, packageManifest)
	}
	if err != nil {
		return earlierPackage{}, err
	}
	var man exportManifest
	if err := json.Unmarshal(raw, &man); err != nil {
		return earlierPackage{}, fmt.Errorf("%s: %w", filepath.Join(dir, packageManifest), err)
	}
	earlier := earlierPackage{files: []string{packageCatalogue, packageQuestions, packageAnswers, packageConstants}}
	for _, p := range man.paths() {
		// Only what lies inside: a manifest naming ../x does not get x deleted.
		if filepath.IsLocal(filepath.FromSlash(p)) {
			earlier.files = append(earlier.files, p)
		}
	}
	q, err := readOptional(filepath.Join(dir, packageQuestions))
	if err != nil {
		return earlierPackage{}, err
	}
	if q != nil {
		if err := json.Unmarshal(q, &earlier.questions); err != nil {
			return earlierPackage{}, fmt.Errorf("%s: %w; mend or delete it, and export again", filepath.Join(dir, packageQuestions), err)
		}
	}
	return earlier, nil
}

// writePackage writes the files, then removes what the earlier export wrote and
// this one does not. In that order, a write that fails leaves a file too many,
// never one too few.
func writePackage(dir string, earlier earlierPackage, files []sourceEntry) error {
	written := map[string]bool{}
	for _, f := range files {
		target := filepath.Join(dir, filepath.FromSlash(f.name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, f.data, 0o644); err != nil {
			return err
		}
		written[f.name] = true
	}
	for _, p := range earlier.files {
		if written[p] {
			continue
		}
		if err := os.Remove(filepath.Join(dir, filepath.FromSlash(p))); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		// A folder the export emptied goes too; one with anything else in it stays.
		for d := path.Dir(p); d != "." && d != "/"; d = path.Dir(d) {
			if os.Remove(filepath.Join(dir, filepath.FromSlash(d))) != nil {
				break
			}
		}
	}
	return nil
}

// unpackSource reads the source archive the server answered. It holds the server's
// own layout, but it is still read as data: a path that would land outside the
// package is refused, not written.
func unpackSource(archive []byte) ([]sourceEntry, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, errors.New("the server's source archive is not a gzip stream")
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	var files []sourceEntry
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return files, nil
		}
		if err != nil {
			return nil, errors.New("the server's source archive is corrupt")
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if len(files) >= maxExportEntries {
			return nil, errors.New("the server's source archive has too many files")
		}
		name := path.Clean(hdr.Name)
		if !filepath.IsLocal(filepath.FromSlash(name)) {
			return nil, fmt.Errorf("the server's source archive names %q, which is outside the package", hdr.Name)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("the server's source archive: %s: %w", name, err)
		}
		files = append(files, sourceEntry{name: name, data: data})
	}
}

// encodeJSON writes a package file: indented for a diff, newline-terminated, and
// without escaping < and > — a BPMN model in katalog.json stays readable XML.
func encodeJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// --- the shop -----------------------------------------------------------------

// shop is katalog.json and what goes with it.
type shop struct {
	document  []byte
	questions map[string]packageQuestion
	answers   map[string]string
	publish   bool
	products  int
	notes     []string
	warnings  []string
}

// shop exports the catalogues as one document and makes it a package's: the
// processes the application carries are left to it, the groups and people become
// questions, and what cannot travel is named.
//
// The document is handled as data, not as the server's types, so a field this
// command does not know — written by a newer server — travels as it came.
func (c *exportClient) shop(ids []string, man exportManifest, earlier map[string]packageQuestion) (shop, error) {
	var (
		s          shop
		catalogs   []any
		products   = map[string]any{}
		processes  = map[string]string{}
		missing    = map[string]bool{}
		unreleased []string
	)
	for _, id := range ids {
		part, err := c.catalogue(id)
		if err != nil {
			return s, err
		}
		catalogs = append(catalogs, list(part["catalogs"])...)
		for _, p := range list(part["products"]) {
			if pid := str(object(p)["id"]); products[pid] == nil {
				products[pid] = p
			}
		}
		for _, p := range list(part["processes"]) {
			processes[str(object(p)["id"])] = str(object(p)["xml"])
		}
		for _, m := range list(part["missingProcesses"]) {
			missing[str(m)] = true
		}
		released, err := c.released(id)
		if err != nil {
			return s, err
		}
		if !released {
			unreleased = append(unreleased, id)
		}
	}
	// Sorted, so the same shop is the same file whichever catalogue offered a product
	// first.
	var ordered []any
	for _, pid := range slices.Sorted(maps.Keys(products)) {
		ordered = append(ordered, products[pid])
	}
	s.products = len(ordered)
	doc := map[string]any{"catalogs": catalogs, "products": ordered}
	if s.publish = len(unreleased) == 0; s.publish {
		doc["publish"] = true
	}
	for _, id := range unreleased {
		s.notes = append(s.notes, fmt.Sprintf("catalogue %s has no release here, so the import does not publish; publish it on the target once it is complete", id))
	}

	inApp := map[string]bool{}
	for _, p := range man.Processes {
		inApp[p.ID] = true
	}
	var carried []any
	for _, id := range slices.Sorted(maps.Keys(processes)) {
		if inApp[id] {
			continue
		}
		carried = append(carried, map[string]any{"id": id, "xml": processes[id]})
		s.notes = append(s.notes, fmt.Sprintf("process %s travels in %s: a product is bound to it, and it is not in the application", id, packageCatalogue))
	}
	if len(carried) > 0 {
		doc["processes"] = carried
	}
	var stillMissing []any
	for _, id := range slices.Sorted(maps.Keys(missing)) {
		if inApp[id] {
			continue
		}
		stillMissing = append(stillMissing, id)
		s.warnings = append(s.warnings, fmt.Sprintf("process %s is bound to a product and deployed nowhere on this server; the target refuses to publish the product until a process of that id is deployed there", id))
	}
	if len(stillMissing) > 0 {
		doc["missingProcesses"] = stillMissing
	}
	s.warnings = append(s.warnings, unreachable(catalogs, ordered, man)...)

	dir, err := c.directory()
	if err != nil {
		return s, err
	}
	q := newQuestioner(dir, earlier)
	q.literals(doc)
	q.walk(catalogs, ordered)
	s.questions, s.answers = q.questions, q.answers
	s.warnings = append(s.warnings, q.warnings...)
	if s.document, err = encodeJSON(doc); err != nil {
		return s, err
	}
	return s, nil
}

// unreachable names what a product needs that the package does not carry: a form
// outside the application, and a home catalogue the package leaves out.
func unreachable(catalogs, products []any, man exportManifest) []string {
	inApp := map[string]bool{}
	for _, f := range man.Forms {
		inApp[f.ID] = true
	}
	exported := map[string]bool{}
	for _, c := range catalogs {
		exported[str(object(c)["id"])] = true
	}
	var out []string
	for _, p := range products {
		item := object(p)
		pid := str(item["id"])
		forms := []string{str(item["configForm"])}
		for _, a := range list(item["actions"]) {
			forms = append(forms, str(object(a)["form"]))
		}
		var named []string
		for _, f := range forms {
			if f != "" && !inApp[f] && !slices.Contains(named, f) {
				named = append(named, f)
				out = append(out, fmt.Sprintf("form %s is asked by product %s and is not in the application; the target needs a form of that id", f, pid))
			}
		}
		if home := str(item["homeCatalog"]); home != "" && !exported[home] {
			out = append(out, fmt.Sprintf("product %s is maintained in catalogue %s, which the package does not carry; a server without it refuses the product (add --catalog %s)", pid, home, home))
		}
	}
	return out
}

// questioner turns the groups and people a shop names into questions.
type questioner struct {
	dir       []directoryEntry
	earlier   map[string]packageQuestion
	byRef     map[string]string
	uses      map[string][2][]string
	questions map[string]packageQuestion
	answers   map[string]string
	warnings  []string
}

func newQuestioner(dir []directoryEntry, earlier map[string]packageQuestion) *questioner {
	return &questioner{dir: dir, earlier: earlier, byRef: map[string]string{}, uses: map[string][2][]string{},
		questions: map[string]packageQuestion{}, answers: map[string]string{}}
}

// literals asks for every {{name}} a text of the shop already carries, and answers
// it with itself. Left alone, `atlas import` would take it for a question nobody
// answers and refuse the package; answered with itself, the text arrives as it was.
// The names are taken before any group or person is given one.
func (q *questioner) literals(v any) {
	switch x := v.(type) {
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(x)) {
			q.literals(x[k])
		}
	case []any:
		for _, e := range x {
			q.literals(e)
		}
	case string:
		for _, m := range packagePlaceholder.FindAllStringSubmatch(x, -1) {
			name := m[1]
			if _, asked := q.questions[name]; asked {
				continue
			}
			q.questions[name] = q.keep(name, "text", packageQuestion{Kind: "text",
				Label: map[string]string{"de": "Platzhalter «{{" + name + "}}» in einem Text des Katalogs", "en": `The placeholder "{{` + name + `}}" in a text of the catalogue`},
				Hint: map[string]string{"de": "Der Katalog trägt diesen Text wörtlich; die mitgelieferte Antwort setzt ihn unverändert wieder ein.",
					"en": "The catalogue carries this text literally; the answer that comes with it puts it back unchanged."}})
			q.answers[name] = "{{" + name + "}}"
		}
	}
}

// walk asks for every principal the catalogues and products name, in the order a
// reader meets them, and puts the question in its place.
func (q *questioner) walk(catalogs, products []any) {
	for _, c := range catalogs {
		cat := object(c)
		de, en := textIn(cat, "de"), textIn(cat, "en")
		groups := list(cat["groups"])
		for i, g := range groups {
			groups[i] = q.ask("group", str(g), "Zielgruppe des Katalogs «"+de+"»", `audience of the catalogue "`+en+`"`)
		}
		for _, m := range list(cat["members"]) {
			member := object(m)
			ref, role := object(member["ref"]), str(member["role"])
			if ref != nil {
				ref["id"] = q.ask(str(ref["type"]), str(ref["id"]),
					"Mitglied ("+role+") des Katalogs «"+de+"»", "member ("+role+`) of the catalogue "`+en+`"`)
			}
		}
	}
	for _, p := range products {
		item := object(p)
		de, en := textIn(item, "de"), textIn(item, "en")
		if approval := object(item["approval"]); approval != nil {
			kind := map[string]string{"fixed": "user", "role": "group"}[str(approval["kind"])]
			if kind != "" {
				approval["ref"] = q.ask(kind, str(approval["ref"]), "genehmigt «"+de+"»", `approves "`+en+`"`)
			}
		}
		eligible := list(item["eligible"])
		for i, g := range eligible {
			eligible[i] = q.ask("group", str(g), "darf «"+de+"» erhalten", `may receive "`+en+`"`)
		}
	}
	for name, uses := range q.uses {
		question := q.questions[name]
		if _, kept := q.earlier[name]; kept && q.earlier[name].Kind == question.Kind {
			continue
		}
		noun := map[string][2]string{"group": {"Gruppe", "Group"}, "user": {"Person", "Person"}}[question.Kind]
		if noun[0] == "" {
			noun = [2]string{question.Kind, question.Kind}
		}
		question.Label = map[string]string{"de": noun[0] + ": " + strings.Join(uses[0], "; "), "en": noun[1] + ": " + strings.Join(uses[1], "; ")}
		q.questions[name] = question
	}
}

// ask returns the {{question}} that stands for a principal, asking it the first
// time the principal is met and noting each use. The kind is the directory's when
// the directory has the id, else the one the field implies. A value that is empty or
// already a question is left as it is.
func (q *questioner) ask(kind, id, useDE, useEN string) string {
	if id == "" || packagePlaceholder.MatchString(id) && "{{"+packagePlaceholder.FindStringSubmatch(id)[1]+"}}" == id {
		return id
	}
	entry, found := findByID(q.dir, kind, id)
	if found {
		kind = entry.Type
	}
	name, asked := q.byRef[kind+"\x00"+id]
	if !asked {
		base := ""
		if found {
			base = packageSlug(entry.Name)
		}
		if base == "" {
			base = packageSlug(id)
		}
		if base == "" {
			base = kind
		}
		name = base
		for n := 2; q.taken(name); n++ {
			name = fmt.Sprintf("%s-%d", base, n)
		}
		q.byRef[kind+"\x00"+id] = name
		hint := map[string]string{"de": "Auf der exportierenden Instanz: «" + entry.Name + "».", "en": `On the exporting instance: "` + entry.Name + `".`}
		if found {
			q.answers[name] = entry.Name
			if same := sameName(q.dir, entry); same > 1 {
				q.warnings = append(q.warnings, fmt.Sprintf("%d %ss are named %q here; if the target has as many, answer %s with an id", same, kind, entry.Name, name))
			}
		} else {
			hint = map[string]string{"de": "Die exportierende Instanz kennt die ID «" + id + "» nicht; die Frage ist unbeantwortet.",
				"en": `The exporting instance does not know the id "` + id + `"; the question has no answer.`}
			q.warnings = append(q.warnings, fmt.Sprintf("%s %s is not in this server's directory; %s in %s has no answer", kind, id, name, packageQuestions))
		}
		q.questions[name] = q.keep(name, kind, packageQuestion{Kind: kind, Hint: hint})
	}
	uses := q.uses[name]
	if !slices.Contains(uses[0], useDE) {
		uses[0], uses[1] = append(uses[0], useDE), append(uses[1], useEN)
		q.uses[name] = uses
	}
	return "{{" + name + "}}"
}

func (q *questioner) taken(name string) bool {
	_, ok := q.questions[name]
	return ok
}

// keep answers the earlier export's question of that name when it asks the same
// kind: its wording may have been improved by hand, and an export must not undo it.
func (q *questioner) keep(name, kind string, fresh packageQuestion) packageQuestion {
	if prev, ok := q.earlier[name]; ok && prev.Kind == kind {
		return prev
	}
	return fresh
}

// findByID finds a principal by id, preferring the kind the field implies.
func findByID(dir []directoryEntry, kind, id string) (directoryEntry, bool) {
	var other *directoryEntry
	for i, e := range dir {
		if e.ID != id {
			continue
		}
		if e.Type == kind {
			return e, true
		}
		if other == nil {
			other = &dir[i]
		}
	}
	if other != nil {
		return *other, true
	}
	return directoryEntry{}, false
}

func sameName(dir []directoryEntry, entry directoryEntry) int {
	n := 0
	for _, e := range dir {
		if e.Type == entry.Type && e.Name == entry.Name {
			n++
		}
	}
	return n
}

// packageSlug is a question's name from a principal's: lower-case ASCII letters and
// digits joined by hyphens, umlauts written out — the shape `atlas import` reads
// as a placeholder.
func packageSlug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if t, ok := map[rune]string{'ä': "ae", 'ö': "oe", 'ü': "ue", 'ß': "ss"}[r]; ok {
			b.WriteString(t)
			dash = false
			continue
		}
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
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

// textIn is a catalogue's or product's text in a language, else in the other of
// the two, else in any it has, else its id.
func textIn(v map[string]any, lang string) string {
	texts := object(v["texts"])
	other := map[string]string{"de": "en", "en": "de"}[lang]
	for _, l := range []string{lang, other} {
		if t := str(texts[l]); t != "" {
			return t
		}
	}
	for _, l := range slices.Sorted(maps.Keys(texts)) {
		if t := str(texts[l]); t != "" {
			return t
		}
	}
	return str(v["id"])
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func object(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// --- the client ---------------------------------------------------------------

type exportClient struct {
	apiClient
}

type applicationEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Key  string `json:"key"`
}

// application finds the application by id, then by key, then by a name only one
// application carries.
func (c *exportClient) application(want string) (applicationEntry, error) {
	var apps []applicationEntry
	if err := c.do(http.MethodGet, "/api/v1/applications", nil, &apps); err != nil {
		return applicationEntry{}, err
	}
	for _, a := range apps {
		if a.ID == want {
			return a, nil
		}
	}
	for _, a := range apps {
		if a.Key != "" && a.Key == want {
			return a, nil
		}
	}
	var named []applicationEntry
	for _, a := range apps {
		if a.Name == want {
			named = append(named, a)
		}
	}
	switch len(named) {
	case 1:
		return named[0], nil
	case 0:
		return applicationEntry{}, fmt.Errorf("no application with the key, id or name %q on this server that you may read", want)
	default:
		ids := make([]string, 0, len(named))
		for _, a := range named {
			ids = append(ids, a.ID)
		}
		sort.Strings(ids)
		return applicationEntry{}, fmt.Errorf("%d applications are named %q; name one by its id: %s", len(named), want, strings.Join(ids, ", "))
	}
}

func (c *exportClient) source(id string) ([]sourceEntry, error) {
	raw, err := c.fetch("/api/v1/applications/" + url.PathEscape(id) + "/source")
	if err != nil {
		return nil, err
	}
	return unpackSource(raw)
}

// catalogue is the catalogue's document with the processes its products are bound
// to, decoded as data: numbers stay as the server wrote them.
func (c *exportClient) catalogue(id string) (map[string]any, error) {
	p := "/api/v1/catalogs/" + url.PathEscape(id) + "/document?processes=true"
	raw, err := c.fetch(p)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("GET %s: decode response: %w", p, err)
	}
	return doc, nil
}

// released reports whether the catalogue has a release here. Only a shop whose
// catalogues are all published here is published by the import: one still being
// built would otherwise be refused there for what is unfinished here.
func (c *exportClient) released(id string) (bool, error) {
	var releases []json.RawMessage
	err := c.do(http.MethodGet, "/api/v1/catalogs/"+url.PathEscape(id)+"/releases", nil, &releases)
	return len(releases) > 0, err
}

func (c *exportClient) directory() ([]directoryEntry, error) {
	var dir []directoryEntry
	err := c.do(http.MethodGet, "/api/v1/principals", nil, &dir)
	return dir, err
}
