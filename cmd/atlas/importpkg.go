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
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/apppkg"
	"github.com/pblumer/atlas/api/importplan"
)

// `atlas import` installs a package into a running atlas from a terminal or a CI
// job, where the shop handbook's installer needs a browser
// (ADR-0437). A package is a directory:
//
//	atlas.json           the source manifest (ADR-0134): key, name, and the files below
//	*.bpmn, *.form.json  the processes and forms the manifest names
//	*.dmn                the decision models it names, when the package carries them
//	katalog.json         optional: the shop, as a catalogue document
//	fragen.json          optional: what each {{placeholder}} in katalog.json asks for
//	konstanten.json      optional: the application's constants, as a suggestion
//
// It takes the steps the installer takes, in its order: the application's source
// goes in through the route a source export comes back through, the application is
// published, and only then is the catalogue imported — its products are bound to
// processes the publish deployed. Everything the command can check before the
// first write it checks first, every answer included, so a package refused for a
// missing answer leaves the server as it was. That includes the plan
// (importplan.go): what differs from the server's is replaced only when the
// operator decided so, and a package installed again changes nothing.

// Package file names, beside the manifest.
const (
	packageManifest  = "atlas.json"
	packageCatalogue = apppkg.CatalogueFile
	packageQuestions = apppkg.QuestionsFile
)

// packagePlaceholder is a value a catalogue document leaves to the installation it
// is imported into: an audience group, an approver.
var packagePlaceholder = apppkg.Placeholder

// packageManifestFile is the part of atlas.json the command reads: which files
// travel. The server reads the whole manifest and checks it.
type packageManifestFile struct {
	Processes []struct {
		Path string `json:"path"`
	} `json:"processes"`
	Forms []struct {
		Path string `json:"path"`
	} `json:"forms"`
	// Decisions name a path only when the package carries the decision's model, and
	// several decisions of one model name the same file.
	Decisions []struct {
		Path string `json:"path"`
	} `json:"decisions"`
	// Constants names the file a tree carries its constants in, as a Console
	// download with its constants does.
	Constants string `json:"constants"`
}

// packageQuestion is one entry of fragen.json.
type packageQuestion = apppkg.Question

// importPackage is a package read from disk and ready to send.
type importPackage struct {
	archive   []byte
	catalogue []byte
	questions map[string]packageQuestion
	// answers are antworten.json's: the names the exporting server gives, answering
	// every question this server's directory or its table of names knows them by.
	answers map[string]string
	// constants are konstanten.json's, nil when the package carries none.
	constants []packageConstant
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
	overwrite, keep := newRefFlag(), newRefFlag()
	fs.Var(overwrite, "overwrite",
		"replace artifacts that differ from the server's with the package's: refs such as process:ID, form:ID, model:HANDLE, constant:NAME or product:ID, comma-separated, or all; repeatable")
	fs.Var(keep, "keep", "leave artifacts that differ from the server's as the server has them: refs, comma-separated, or all; repeatable")
	dryRun := fs.Bool("dry-run", false, "show what the import would create, leave and change, and write nothing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("name the package: atlas import [flags] DIR")
	}
	decide, err := decisions(overwrite, keep)
	if err != nil {
		return err
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
	var (
		document []byte
		remember []apppkg.Mapping
	)
	if pkg.catalogue != nil {
		// A server started with --catalogue=false has no catalogue to import into
		// (ADR-0434). Asked first, so the application is not published for a shop
		// that cannot follow it.
		if off, err := c.catalogueOff(); err != nil {
			return err
		} else if off {
			return fmt.Errorf("%s carries a shop, and this server has its catalogue switched off (--catalogue=false); nothing was imported", fs.Arg(0))
		}
		mappings, err := c.mappings()
		if err != nil {
			return err
		}
		resolved, notes, learned, err := resolveAnswers(pkg, answers, mappings, c.directory)
		if err != nil {
			return err
		}
		for _, n := range notes {
			fmt.Fprintln(out, n)
		}
		remember = learned
		if document, err = fillDocument(pkg.catalogue, resolved); err != nil {
			return err
		}
	} else if len(answers) > 0 {
		fmt.Fprintf(out, "note: %s has no %s, so its answers are not used\n", fs.Arg(0), packageCatalogue)
	}

	// The plan, before the first write: what is new, what is unchanged, and what
	// differs from the server's and needs a decision.
	plan, err := c.plan(pkg, document)
	if err != nil {
		return err
	}
	plan.decide(decide)
	plan.show(out, *dryRun)
	if *dryRun {
		fmt.Fprintln(out, "dry run: nothing was written")
		return nil
	}
	if err := plan.ask(out); err != nil {
		return err
	}
	decided := plan.query()

	src, err := c.importSource(pkg.archive, decided)
	if err != nil {
		return err
	}
	verb := "unchanged"
	switch {
	case src.Created:
		verb = "created"
	case sourceChanged(src):
		verb = "updated"
	}
	fmt.Fprintf(out, "application %q (%s) %s: %d processes, %d forms, %d decisions",
		src.Name, src.Key, verb, src.Processes, src.Forms, src.Decisions)
	if src.Models > 0 {
		fmt.Fprintf(out, ", %d decision models", src.Models)
	}
	fmt.Fprintln(out)
	if k := kept(src.Artifacts); len(k) > 0 {
		fmt.Fprintf(out, "  kept as the server has them: %s\n", strings.Join(k, ", "))
	}
	if len(src.ModelsSkipped) > 0 {
		fmt.Fprintf(out, "  decision models not written, the server reads its models from temis: %s\n", strings.Join(src.ModelsSkipped, ", "))
	}
	if len(src.Untracked) > 0 {
		fmt.Fprintf(out, "  left in place, not in the package: %s\n", strings.Join(src.Untracked, ", "))
	}
	if len(src.ConstantsSet) > 0 {
		fmt.Fprintf(out, "constants set: %s\n", strings.Join(src.ConstantsSet, ", "))
	}
	if len(src.ConstantsNotSet) > 0 {
		fmt.Fprintf(out, "  warning: constants not set — setting them is an administrator's, and a token never carries that role; set them in the Console under the application's constants: %s\n",
			strings.Join(src.ConstantsNotSet, ", "))
	}
	// The constants go in before the publish, so what it warns about is what is
	// still missing, not what the next step sets.
	if err := c.installConstants(out, src.ApplicationID, pkg.constants, decided); err != nil {
		return err
	}

	// Published only when the import changed the application, or it was never
	// published here: a package installed every night is not a release every night.
	latest, err := c.latestRelease(src.ApplicationID)
	if err != nil {
		return err
	}
	if latest > 0 && !sourceChanged(src) {
		fmt.Fprintf(out, "the application is unchanged, so it is not published again; release %d stays\n", latest)
	} else {
		pub, err := c.publish(src.ApplicationID, *note)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "published release %d: %d processes, %d decisions deployed\n",
			pub.Release.Version, len(pub.Definitions), len(pub.Decisions))
		for _, w := range pub.Warnings {
			fmt.Fprintf(out, "  warning: %s\n", w)
		}
	}
	if document != nil {
		if err := c.installShop(out, document, decided); err != nil {
			return err
		}
		// What an answer chose differently from the names is remembered on this
		// server once the shop is in, so the next import needs no --set for it.
		if len(remember) > 0 {
			if err := c.rememberMappings(remember); err != nil {
				return err
			}
			for _, m := range remember {
				fmt.Fprintf(out, "remembered on this server: %s %q is %s\n", m.Kind, m.Answer, m.ID)
			}
		}
	}
	return c.reportConstants(out, src.ApplicationID)
}

// installShop imports the catalogue document with the decisions the plan settled.
func (c *importClient) installShop(out io.Writer, document []byte, decided string) error {
	res, err := c.importCatalogue(document, decided)
	if err != nil {
		return err
	}
	if len(res.Created)+len(res.Updated) == 0 {
		fmt.Fprintln(out, "shop unchanged")
	}
	if len(res.Created) > 0 {
		fmt.Fprintf(out, "shop created: %s\n", strings.Join(res.Created, ", "))
	}
	if len(res.Updated) > 0 {
		fmt.Fprintf(out, "shop updated: %s\n", strings.Join(res.Updated, ", "))
	}
	if k := kept(res.Artifacts); len(k) > 0 {
		fmt.Fprintf(out, "  kept as the server has them: %s\n", strings.Join(k, ", "))
	}
	fmt.Fprintf(out, "catalogues published: %d\n", len(res.Releases))
	for _, w := range res.Warnings {
		fmt.Fprintf(out, "  warning: %s: %s\n", w.Subject, w.Problem)
	}
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
	for _, d := range man.Decisions {
		if d.Path != "" && !slices.Contains(paths, d.Path) {
			paths = append(paths, d.Path)
		}
	}
	if man.Constants != "" {
		paths = append(paths, man.Constants)
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
	if a, err := readOptional(filepath.Join(dir, apppkg.AnswersFile)); err != nil {
		return importPackage{}, err
	} else if a != nil {
		if err := json.Unmarshal(a, &pkg.answers); err != nil {
			return importPackage{}, fmt.Errorf("%s: %w", filepath.Join(dir, apppkg.AnswersFile), err)
		}
	}
	if q, err := readOptional(filepath.Join(dir, packageQuestions)); err != nil {
		return importPackage{}, err
	} else if q != nil {
		if err := json.Unmarshal(q, &pkg.questions); err != nil {
			return importPackage{}, fmt.Errorf("%s: %w", filepath.Join(dir, packageQuestions), err)
		}
	}
	if k, err := readOptional(filepath.Join(dir, packageConstants)); err != nil {
		return importPackage{}, err
	} else if k != nil {
		var file constantsFile
		if err := json.Unmarshal(k, &file); err != nil {
			return importPackage{}, fmt.Errorf("%s: %w", filepath.Join(dir, packageConstants), err)
		}
		pkg.constants = file.Constants
		if pkg.constants == nil {
			pkg.constants = []packageConstant{}
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
type directoryEntry = apppkg.DirectoryEntry

// resolveAnswers answers every placeholder the catalogue document leaves
// (apppkg.Resolve): what --answers and --set give, else what this server's table of
// names remembers for the package's own answer, else that answer in the directory. It
// says each problem in the terms of this command, an unanswered question with the flag
// that answers it, and names the answers worth remembering.
func resolveAnswers(pkg importPackage, answers map[string]string, mappings []apppkg.Mapping, directory func() ([]directoryEntry, error)) (map[string]string, []string, []apppkg.Mapping, error) {
	res, err := apppkg.Resolve(pkg.catalogue, pkg.questions, apppkg.Answers{Packaged: pkg.answers, Chosen: answers, Mappings: mappings}, directory)
	if err != nil {
		return nil, nil, nil, err
	}
	if problems := res.Problems; len(problems) > 0 {
		lines := make([]string, 0, len(problems))
		for _, p := range problems {
			if p.Reason == apppkg.ProblemUnanswered {
				lines = append(lines, fmt.Sprintf("%s is not answered: %s (--set %s=...)", p.Name, p.Question, p.Name))
				continue
			}
			lines = append(lines, p.Name+": "+p.Detail)
		}
		return nil, nil, nil, fmt.Errorf("the package's questions are not all answered; nothing was imported:\n  %s",
			strings.Join(lines, "\n  "))
	}
	return res.Values, res.Notes, res.Learned, nil
}

// fillDocument puts the answers in place of the placeholders (apppkg.Fill).
func fillDocument(doc []byte, answers map[string]string) ([]byte, error) {
	return apppkg.Fill(doc, answers)
}

// --- the client ---------------------------------------------------------------

type importClient struct {
	apiClient
}

type sourceImportBody struct {
	ApplicationID string                `json:"applicationId"`
	Key           string                `json:"key"`
	Name          string                `json:"name"`
	Created       bool                  `json:"created"`
	Processes     int                   `json:"processes"`
	Forms         int                   `json:"forms"`
	Decisions     int                   `json:"decisions"`
	Models        int                   `json:"models"`
	ModelsSkipped []string              `json:"modelsSkipped"`
	Artifacts     []importplan.Artifact `json:"artifacts"`
	Untracked     []string              `json:"untracked"`
	// ConstantsSet and ConstantsNotSet are about constants the tree itself carries,
	// as a Console download with its constants does.
	ConstantsSet    []string      `json:"constantsSet"`
	ConstantsNotSet []string      `json:"constantsNotSet"`
	Needs           []packageNeed `json:"needs"`
	NeedsUnread     []string      `json:"needsUnread"`
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
	Created   []string              `json:"created"`
	Updated   []string              `json:"updated"`
	Artifacts []importplan.Artifact `json:"artifacts"`
	Releases  []json.RawMessage     `json:"releases"`
	// Warnings are what the published catalogues leave exposed: answers that reach a
	// process in the clear. The import went through; they say what to change.
	Warnings []struct {
		Subject string `json:"subject"`
		Problem string `json:"problem"`
	} `json:"warnings"`
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

func (c *importClient) directory() ([]directoryEntry, error) { return c.apiClient.directory() }

// mappings reads this server's table of names. A server from before the table has
// none to read, and answers 404 for it.
func (c *importClient) mappings() ([]apppkg.Mapping, error) {
	var table []apppkg.Mapping
	err := c.do(http.MethodGet, "/api/v1/catalogs/import/mappings", nil, &table)
	var status *statusError
	if errors.As(err, &status) && status.status == http.StatusNotFound {
		return nil, nil
	}
	return table, err
}

// rememberMappings adds entries to this server's table of names.
func (c *importClient) rememberMappings(entries []apppkg.Mapping) error {
	body, err := json.Marshal(map[string]any{"mappings": entries})
	if err != nil {
		return err
	}
	return c.do(http.MethodPost, "/api/v1/catalogs/import/mappings", body, nil)
}

// importSource sends the application's source with a query: ?dryRun=true for the
// plan, or the decisions.
func (c *importClient) importSource(archive []byte, query string) (sourceImportBody, error) {
	var res sourceImportBody
	if err := c.send(http.MethodPost, "/api/v1/applications/source"+query, "application/gzip", archive, &res); err != nil {
		return res, err
	}
	// A plan for an application the server has never seen names no id: none is
	// taken until it is written.
	if res.ApplicationID == "" && !(query == "?dryRun=true" && res.Created) {
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

// importCatalogue sends the catalogue document with a query: ?dryRun=true for the
// plan, or the decisions.
func (c *importClient) importCatalogue(doc []byte, query string) (catalogueImportBody, error) {
	var res catalogueImportBody
	err := c.do(http.MethodPost, "/api/v1/catalogs/import"+query, doc, &res)
	return res, err
}
