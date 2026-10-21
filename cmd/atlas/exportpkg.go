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
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/apppkg"
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

const packageAnswers = apppkg.AnswersFile

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

	var s apppkg.Shop
	if len(catalogs) > 0 {
		if s, err = c.shop(catalogs, man, earlier.questions); err != nil {
			return err
		}
		files = append(files, sourceEntry{name: packageCatalogue, data: s.Document})
		if len(s.Questions) > 0 {
			q, err := encodeJSON(s.Questions)
			if err != nil {
				return err
			}
			a, err := encodeJSON(s.Answers)
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
		if s.Publish {
			publish = "published on import"
		}
		fmt.Fprintf(out, "catalogues: %s (%d products, %s)\n", strings.Join(catalogs, ", "), s.Products, publish)
		for _, n := range s.Notes {
			fmt.Fprintf(out, "  %s\n", n)
		}
		if len(s.Questions) > 0 {
			fmt.Fprintf(out, "questions: %d, answered in %s with the names this server gives them\n", len(s.Questions), packageAnswers)
			install = "atlas import --server <target> --answers " + filepath.Join(dir, packageAnswers) + " " + dir
		}
		for _, w := range s.Warnings {
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

// encodeJSON writes a package file as every package file is written.
func encodeJSON(v any) ([]byte, error) { return apppkg.EncodeJSON(v) }

// --- the shop -----------------------------------------------------------------

// shop exports the catalogues and makes them a package's (apppkg.BuildShop): the
// processes the application carries are left to it, the groups and people become
// questions, and what cannot travel is named.
func (c *exportClient) shop(ids []string, man exportManifest, earlier map[string]packageQuestion) (apppkg.Shop, error) {
	parts := make([]apppkg.Part, 0, len(ids))
	for _, id := range ids {
		doc, err := c.catalogue(id)
		if err != nil {
			return apppkg.Shop{}, err
		}
		released, err := c.released(id)
		if err != nil {
			return apppkg.Shop{}, err
		}
		parts = append(parts, apppkg.Part{ID: id, Document: doc, Released: released})
	}
	var app apppkg.Application
	for _, p := range man.Processes {
		app.Processes = append(app.Processes, p.ID)
	}
	for _, f := range man.Forms {
		app.Forms = append(app.Forms, f.ID)
	}
	dir, err := c.directory()
	if err != nil {
		return apppkg.Shop{}, err
	}
	return apppkg.BuildShop(parts, app, dir, earlier)
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
	var doc map[string]any
	if err := apppkg.Decode(raw, &doc); err != nil {
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

func (c *exportClient) directory() ([]directoryEntry, error) { return c.apiClient.directory() }
