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
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// `atlas import` installs a package into a running atlas from a terminal or a CI
// job, where the shop handbook's installer needs a browser
// (ADR-draft-a-package-is-imported-from-the-command-line). A package is a directory:
//
//	atlas.json           the source manifest (ADR-0134): key, name, and the files below
//	*.bpmn, *.form.json  the processes and forms the manifest names
//	katalog.json         optional: the shop, as a catalogue document
//	fragen.json          optional: what each {{placeholder}} in katalog.json asks for
//
// It takes the steps the installer takes, in its order: the application's source
// goes in through the route a source export comes back through, the application is
// published, and only then is the catalogue imported — its products are bound to
// processes the publish deployed. Everything the command can check before the
// first write it checks first, every answer included, so a package refused for a
// missing answer leaves the server as it was.

// Package file names, beside the manifest.
const (
	packageManifest  = "atlas.json"
	packageCatalogue = "katalog.json"
	packageQuestions = "fragen.json"
)

// packagePlaceholder is a value a catalogue document leaves to the installation it
// is imported into: an audience group, an approver.
var packagePlaceholder = regexp.MustCompile(`\{\{([a-z0-9-]+)\}\}`)

// packageManifestFile is the part of atlas.json the command reads: which files
// travel. The server reads the whole manifest and checks it.
type packageManifestFile struct {
	Processes []struct {
		Path string `json:"path"`
	} `json:"processes"`
	Forms []struct {
		Path string `json:"path"`
	} `json:"forms"`
}

// packageQuestion is one entry of fragen.json. Kind says where an answer must be
// found: "group" and "user" in the server's directory, anything else is taken as
// written.
type packageQuestion struct {
	Kind  string            `json:"kind"`
	Label map[string]string `json:"label"`
	Hint  map[string]string `json:"hint"`
}

// text is the question in English when it has English, else in whatever it has.
func (q packageQuestion) text(name string) string {
	if t := q.Label["en"]; t != "" {
		return t
	}
	for _, lang := range slices.Sorted(maps.Keys(q.Label)) {
		return q.Label[lang]
	}
	return name
}

// importPackage is a package read from disk and ready to send.
type importPackage struct {
	archive   []byte
	catalogue []byte
	questions map[string]packageQuestion
}

// answerFlags collects repeated --set name=value flags.
type answerFlags map[string]string

func (a answerFlags) String() string { return "" }

func (a answerFlags) Set(v string) error {
	name, value, ok := strings.Cut(v, "=")
	if !ok || strings.TrimSpace(name) == "" {
		return fmt.Errorf("--set takes name=value, not %q", v)
	}
	a[strings.TrimSpace(name)] = value
	return nil
}

func runImport(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	server := fs.String("server", "http://localhost:8080", "base URL of the atlas server to install into")
	token := fs.String("token", os.Getenv("ATLAS_TOKEN"),
		"bearer token, when the server requires authentication (or ATLAS_TOKEN); it needs the modeler role for the application and productmanager for a catalogue")
	answersFile := fs.String("answers", "", "a JSON file of answers to the package's questions, {\"name\": \"value\"}")
	set := answerFlags{}
	fs.Var(set, "set", "answer one question, name=value; repeatable, and wins over --answers")
	note := fs.String("note", "atlas import", "the note the application's release carries")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("name the package: atlas import [flags] DIR")
	}

	pkg, err := readImportPackage(fs.Arg(0))
	if err != nil {
		return err
	}
	answers, err := readAnswers(*answersFile, set)
	if err != nil {
		return err
	}
	c := &importClient{apiClient: newAPIClient(*server, *token)}

	// Everything that can be refused without writing is refused before the first
	// write: the answers are resolved and the document filled now, not after the
	// application is already in.
	var document []byte
	if pkg.catalogue != nil {
		// A server started with --catalogue=false has no catalogue to import into
		// (ADR-0434). Asked first, so the application is not published for a shop
		// that cannot follow it.
		if off, err := c.catalogueOff(); err != nil {
			return err
		} else if off {
			return fmt.Errorf("%s carries a shop, and this server has its catalogue switched off (--catalogue=false); nothing was imported", fs.Arg(0))
		}
		resolved, notes, err := resolveAnswers(pkg, answers, c.directory)
		if err != nil {
			return err
		}
		for _, n := range notes {
			fmt.Fprintln(out, n)
		}
		if document, err = fillDocument(pkg.catalogue, resolved); err != nil {
			return err
		}
	} else if len(answers) > 0 {
		fmt.Fprintf(out, "note: %s has no %s, so its answers are not used\n", fs.Arg(0), packageCatalogue)
	}

	src, err := c.importSource(pkg.archive)
	if err != nil {
		return err
	}
	verb := "updated"
	if src.Created {
		verb = "created"
	}
	fmt.Fprintf(out, "application %q (%s) %s: %d processes, %d forms, %d decisions\n",
		src.Name, src.Key, verb, src.Processes, src.Forms, src.Decisions)
	if len(src.Untracked) > 0 {
		fmt.Fprintf(out, "  left in place, not in the package: %s\n", strings.Join(src.Untracked, ", "))
	}

	pub, err := c.publish(src.ApplicationID, *note)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "published release %d: %d processes, %d decisions deployed\n",
		pub.Release.Version, len(pub.Definitions), len(pub.Decisions))
	for _, w := range pub.Warnings {
		fmt.Fprintf(out, "  warning: %s\n", w)
	}

	if document == nil {
		return nil
	}
	res, err := c.importCatalogue(document)
	if err != nil {
		return err
	}
	if len(res.Created) > 0 {
		fmt.Fprintf(out, "shop created: %s\n", strings.Join(res.Created, ", "))
	}
	if len(res.Updated) > 0 {
		fmt.Fprintf(out, "shop updated: %s\n", strings.Join(res.Updated, ", "))
	}
	fmt.Fprintf(out, "catalogues published: %d\n", len(res.Releases))
	return nil
}

// readImportPackage reads a package directory and packs the application's source
// as the gzip tar POST /api/v1/applications/source reads. Only the manifest and the
// files it names travel; a README beside them is the package's, not the
// application's.
func readImportPackage(dir string) (importPackage, error) {
	raw, err := os.ReadFile(filepath.Join(dir, packageManifest))
	if err != nil {
		return importPackage{}, fmt.Errorf("%s is not a package: %w", dir, err)
	}
	var man packageManifestFile
	if err := json.Unmarshal(raw, &man); err != nil {
		return importPackage{}, fmt.Errorf("%s: %w", filepath.Join(dir, packageManifest), err)
	}
	paths := []string{}
	for _, p := range man.Processes {
		paths = append(paths, p.Path)
	}
	for _, f := range man.Forms {
		paths = append(paths, f.Path)
	}
	sort.Strings(paths)

	files := []sourceEntry{{name: packageManifest, data: raw}}
	for _, p := range paths {
		// The manifest is the package's own, but a path that leaves the directory
		// would send a file the package does not hold.
		if !filepath.IsLocal(filepath.FromSlash(p)) {
			return importPackage{}, fmt.Errorf("%s names %q, which is outside the package", packageManifest, p)
		}
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		if err != nil {
			return importPackage{}, fmt.Errorf("%s names %s: %w", packageManifest, p, err)
		}
		files = append(files, sourceEntry{name: p, data: data})
	}
	archive, err := packSource(files)
	if err != nil {
		return importPackage{}, err
	}
	pkg := importPackage{archive: archive}

	if pkg.catalogue, err = readOptional(filepath.Join(dir, packageCatalogue)); err != nil {
		return importPackage{}, err
	}
	if q, err := readOptional(filepath.Join(dir, packageQuestions)); err != nil {
		return importPackage{}, err
	} else if q != nil {
		if err := json.Unmarshal(q, &pkg.questions); err != nil {
			return importPackage{}, fmt.Errorf("%s: %w", filepath.Join(dir, packageQuestions), err)
		}
	}
	return pkg, nil
}

// readOptional reads a file a package may leave out: absent is nil, not an error.
func readOptional(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

type sourceEntry struct {
	name string
	data []byte
}

// packSource writes the entries as a gzip tar, the archive a source export is.
func packSource(files []sourceEntry) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		hdr := &tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.data)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write(f.data); err != nil {
			return nil, err
		}
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// readAnswers merges the answers file with the --set flags, which win: a CI job
// keeps the file for the shared answers and overrides one per environment.
func readAnswers(file string, set answerFlags) (map[string]string, error) {
	answers := map[string]string{}
	if file != "" {
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read answers: %w", err)
		}
		if err := json.Unmarshal(raw, &answers); err != nil {
			return nil, fmt.Errorf("%s is not a JSON object of answers, {\"name\": \"value\"}: %w", file, err)
		}
	}
	for k, v := range set {
		answers[k] = v
	}
	return answers, nil
}

// directoryEntry is one principal GET /api/v1/principals lists.
type directoryEntry struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

// resolveAnswers answers every placeholder the catalogue document leaves. An answer
// to a group or person question must be one the server's directory has, given by
// its id or by its name: an audience group that does not exist reaches nobody, and
// the import would succeed without saying so. Names are accepted because ids are
// minted per server, so an answers file that names "Alle Mitarbeitenden" serves the
// test installation and the production one alike. Every problem is reported at once.
func resolveAnswers(pkg importPackage, answers map[string]string, directory func() ([]directoryEntry, error)) (map[string]string, []string, error) {
	var names []string
	seen := map[string]bool{}
	for _, m := range packagePlaceholder.FindAllSubmatch(pkg.catalogue, -1) {
		if name := string(m[1]); !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	var (
		problems []string
		notes    []string
		dir      []directoryEntry
		dirRead  bool
	)
	resolved := map[string]string{}
	for _, name := range names {
		q := pkg.questions[name]
		value := strings.TrimSpace(answers[name])
		if value == "" {
			problems = append(problems, fmt.Sprintf("%s is not answered: %s (--set %s=...)", name, q.text(name), name))
			continue
		}
		if q.Kind != "group" && q.Kind != "user" {
			resolved[name] = value
			continue
		}
		if !dirRead {
			var err error
			if dir, err = directory(); err != nil {
				return nil, nil, err
			}
			dirRead = true
		}
		id, why := findPrincipal(dir, q.Kind, value)
		if why != "" {
			problems = append(problems, name+": "+why)
			continue
		}
		if id != value {
			notes = append(notes, fmt.Sprintf("%s: %q is %s %s", name, value, q.Kind, id))
		}
		resolved[name] = id
	}
	for _, k := range slices.Sorted(maps.Keys(answers)) {
		if !seen[k] {
			notes = append(notes, fmt.Sprintf("note: the package does not ask for %s; that answer is not used", k))
		}
	}
	if len(problems) > 0 {
		return nil, nil, fmt.Errorf("the package's questions are not all answered; nothing was imported:\n  %s",
			strings.Join(problems, "\n  "))
	}
	return resolved, notes, nil
}

// findPrincipal finds a principal of a kind by id, then by name. A name two
// principals share is refused rather than guessed.
func findPrincipal(dir []directoryEntry, kind, value string) (string, string) {
	var byName []directoryEntry
	for _, e := range dir {
		if e.Type != kind {
			continue
		}
		if e.ID == value {
			return e.ID, ""
		}
		if e.Name == value {
			byName = append(byName, e)
		}
	}
	switch len(byName) {
	case 1:
		return byName[0].ID, ""
	case 0:
		return "", fmt.Sprintf("no %s with the id or name %q on this server", kind, value)
	default:
		ids := make([]string, 0, len(byName))
		for _, e := range byName {
			ids = append(ids, e.ID)
		}
		return "", fmt.Sprintf("%d %ss are named %q; answer with the id of one: %s", len(byName), kind, value, strings.Join(ids, ", "))
	}
}

// fillDocument puts the answers in place of the placeholders. Each goes in escaped
// as a JSON string, so an answer cannot break the document it is put in.
func fillDocument(doc []byte, answers map[string]string) ([]byte, error) {
	filled := packagePlaceholder.ReplaceAllFunc(doc, func(m []byte) []byte {
		name := string(packagePlaceholder.FindSubmatch(m)[1])
		quoted, _ := json.Marshal(answers[name])
		return quoted[1 : len(quoted)-1]
	})
	if !json.Valid(filled) {
		return nil, fmt.Errorf("%s is not JSON", packageCatalogue)
	}
	return filled, nil
}

// --- the client ---------------------------------------------------------------

type importClient struct {
	apiClient
}

type sourceImportBody struct {
	ApplicationID string   `json:"applicationId"`
	Key           string   `json:"key"`
	Name          string   `json:"name"`
	Created       bool     `json:"created"`
	Processes     int      `json:"processes"`
	Forms         int      `json:"forms"`
	Decisions     int      `json:"decisions"`
	Untracked     []string `json:"untracked"`
}

type publishBody struct {
	Deployed    bool              `json:"deployed"`
	Reason      string            `json:"reason"`
	Definitions []json.RawMessage `json:"definitions"`
	Decisions   []json.RawMessage `json:"decisions"`
	Warnings    []string          `json:"warnings"`
	Release     struct {
		Version int `json:"version"`
	} `json:"release"`
}

type catalogueImportBody struct {
	Created  []string          `json:"created"`
	Updated  []string          `json:"updated"`
	Releases []json.RawMessage `json:"releases"`
}

// catalogueOff reports whether the server says its catalogue is switched off. An
// info without the field is a server from before the switch, whose catalogue is on.
func (c *importClient) catalogueOff() (bool, error) {
	var info struct {
		Catalogue *bool `json:"catalogue"`
	}
	if err := c.do(http.MethodGet, "/api/v1/info", nil, &info); err != nil {
		return false, err
	}
	return info.Catalogue != nil && !*info.Catalogue, nil
}

func (c *importClient) directory() ([]directoryEntry, error) {
	var dir []directoryEntry
	err := c.do(http.MethodGet, "/api/v1/principals", nil, &dir)
	return dir, err
}

func (c *importClient) importSource(archive []byte) (sourceImportBody, error) {
	var res sourceImportBody
	if err := c.send(http.MethodPost, "/api/v1/applications/source", "application/gzip", archive, &res); err != nil {
		return res, err
	}
	if res.ApplicationID == "" {
		return res, errors.New("the server imported the source without naming the application")
	}
	return res, nil
}

// publish deploys the application. A publish the server declines answers 2xx with
// deployed false and its reason, which is a failure here: the catalogue that follows
// would be refused for processes that are not deployed, a step later and for a
// reason further from the cause.
func (c *importClient) publish(id, note string) (publishBody, error) {
	body, err := json.Marshal(map[string]string{"note": note})
	if err != nil {
		return publishBody{}, err
	}
	var res publishBody
	if err := c.do(http.MethodPost, "/api/v1/applications/"+url.PathEscape(id)+"/publish", body, &res); err != nil {
		return res, err
	}
	if !res.Deployed {
		return res, fmt.Errorf("the application was not published: %s", res.Reason)
	}
	return res, nil
}

func (c *importClient) importCatalogue(doc []byte) (catalogueImportBody, error) {
	var res catalogueImportBody
	err := c.do(http.MethodPost, "/api/v1/catalogs/import", doc, &res)
	return res, err
}
