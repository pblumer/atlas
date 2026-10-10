package examples

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	shop "github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/compiler"
)

// An example that ships a shop carries it as a catalogue document, katalog.json
// (ADR-0436), beside the processes and forms
// its products are bound to. The installer posts it to POST /api/v1/catalogs/import
// with publish:true, so a document that would not publish is an example whose install
// button fails in the reader's instance. This file proves, without a server, what that
// import will check: the document on its own, every catalogue's publish, the products'
// lifecycle bindings against the example's own compiled processes, and that every
// form a product names is one the example ships.

// The files of a package, beside its processes and forms: the application's
// manifest in the source layout (ADR-0134), the catalogue document, and what each
// placeholder in it asks. `atlas import` reads the same three names
// (ADR-0437).
const (
	packageManifestName = "atlas.json"
	shopDocumentName    = "katalog.json"
	shopQuestionsName   = "fragen.json"
)

// placeholder is a value the installer asks the reader for — an audience group, an
// approver — because it names something in their installation and not in ours.
var placeholder = regexp.MustCompile(`\{\{([a-z0-9-]+)\}\}`)

// shopDocuments finds every example's catalogue document.
func shopDocuments(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == shopDocumentName {
			out = append(out, path)
		}
		return err
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("no example ships a catalogue document; this guard would pass vacuously")
	}
	sort.Strings(out)
	return out
}

// readShopDocument reads a document with every placeholder filled the way an install
// fills it, refusing a field the document type does not have: a misspelt key would
// otherwise vanish on import, and the product would publish without it.
func readShopDocument(t *testing.T, path string) (shop.Document, []string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var names []string
	for _, m := range placeholder.FindAllSubmatch(raw, -1) {
		names = append(names, string(m[1]))
	}
	filled := placeholder.ReplaceAll(raw, []byte("beispiel-$1"))
	dec := json.NewDecoder(bytes.NewReader(filled))
	dec.DisallowUnknownFields()
	var doc shop.Document
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("%s is not a catalogue document: %v", path, err)
	}
	return doc, names
}

// exampleProcesses compiles every process an example directory ships, by process id.
func exampleProcesses(t *testing.T, dir string) map[string]*compiler.CompiledProcess {
	t.Helper()
	out := map[string]*compiler.CompiledProcess{}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.bpmn"))
	for _, path := range matches {
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("open %s: %v", path, err)
		}
		cp, err := compiler.Parse(1, 1, f)
		_ = f.Close()
		if err != nil {
			t.Fatalf("%s does not compile: %v", path, err)
		}
		out[cp.ProcessId()] = cp
	}
	return out
}

// compiledLookup answers the lifecycle checks from an example's own compiled
// processes, as the server answers them from what it has deployed.
type compiledLookup map[string]*compiler.CompiledProcess

func (l compiledLookup) EntryPoints(processID string) ([]string, bool, bool) {
	cp, ok := l[processID]
	if !ok {
		return nil, false, false
	}
	var messages []string
	for _, ms := range cp.MessageStartEvents() {
		messages = append(messages, ms.MessageName)
	}
	hasNone := false
	for _, id := range cp.StartEvents() {
		if cp.Node(id).Type == compiler.TypeStartEvent {
			hasNone = true
		}
	}
	return messages, hasNone, true
}

func (l compiledLookup) ShopOutcomes(processID string) []shop.ShopOutcome {
	cp, ok := l[processID]
	if !ok {
		return nil
	}
	var out []shop.ShopOutcome
	for _, p := range cp.ShopOutcomePoints() {
		out = append(out, shop.ShopOutcome{Element: p.Element, Action: p.Action, Outcome: p.Outcome})
	}
	return out
}

// exampleFormIDs is every form id an example directory ships.
func exampleFormIDs(t *testing.T, dir string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	for _, path := range matches {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var form struct {
			Type       string            `json:"type"`
			ID         string            `json:"id"`
			Components []json.RawMessage `json:"components"`
		}
		if json.Unmarshal(raw, &form) == nil && form.Type == "default" && form.Components != nil {
			out[form.ID] = true
		}
	}
	return out
}

// exampleFormFieldLookup answers which variables an example's forms write, for the
// publish check that refuses a configuration field named like one of the order's own
// variables.
type exampleFormFieldLookup map[string][]shop.FormField

func (l exampleFormFieldLookup) FormFields(id string) ([]shop.FormField, bool) {
	fields, ok := l[id]
	return fields, ok
}

// PersonalVariables answers the publish warning from an example's own compiled
// processes, as the server answers it from what it has deployed.
func (l compiledLookup) PersonalVariables(processID string) ([]string, bool) {
	cp, ok := l[processID]
	if !ok {
		return nil, false
	}
	return cp.PersonalVariables(), true
}

func exampleFormFields(t *testing.T, dir string) exampleFormFieldLookup {
	t.Helper()
	out := exampleFormFieldLookup{}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.form.json"))
	for _, path := range matches {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var form struct {
			ID         string `json:"id"`
			Components []struct {
				Key        string            `json:"key"`
				Properties map[string]string `json:"properties"`
			} `json:"components"`
		}
		if err := json.Unmarshal(raw, &form); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for _, c := range form.Components {
			if c.Key != "" {
				out[form.ID] = append(out[form.ID], shop.FormField{Key: c.Key, NotPersonal: c.Properties["personal"] == "false"})
			}
		}
	}
	return out
}

// TestEveryShopDocumentPublishes is the install button's whole path, checked before a
// reader presses it.
func TestEveryShopDocumentPublishes(t *testing.T) {
	for _, path := range shopDocuments(t) {
		t.Run(path, func(t *testing.T) {
			dir := filepath.Dir(path)
			doc, _ := readShopDocument(t, path)
			if !doc.Publish {
				t.Errorf("%s does not ask to be published; an example's shop is for ordering from", path)
			}
			for _, p := range doc.Check() {
				t.Errorf("%s: %s", p.Subject, p.Problem)
			}

			processes := compiledLookup(exampleProcesses(t, dir))
			forms := exampleFormIDs(t, dir)
			for _, p := range shop.OrderFormProblems(doc.Products, exampleFormFields(t, dir)) {
				t.Errorf("%s", p.String())
			}
			// An example is what people copy: none of its answers reaches a process in
			// the clear unless its form says the answer names nobody.
			for _, p := range shop.AnswerWarnings(doc.Products, exampleFormFields(t, dir), processes) {
				t.Errorf("publishing would warn: %s", p.String())
			}
			byID := map[string]shop.Item{}
			for _, it := range doc.Products {
				byID[it.ID] = it
			}
			for _, c := range doc.Catalogs {
				var items []shop.Item
				for _, id := range c.Items {
					it, ok := byID[id]
					if !ok {
						t.Errorf("catalog %s offers %s, which the document does not carry", c.ID, id)
						continue
					}
					items = append(items, it)
				}
				sort.Slice(items, func(a, b int) bool { return items[a].ID < items[b].ID })
				rel, problems := shop.Publish(shop.Input{CatalogID: c.ID, Catalogs: doc.Catalogs, Items: items, Edges: c.Edges})
				for _, p := range problems {
					t.Errorf("catalog %s would not publish: %s", c.ID, p.String())
				}
				for _, p := range shop.LifecycleProblems(rel.Items, processes) {
					t.Errorf("catalog %s: %s", c.ID, p.String())
				}
			}

			for _, it := range doc.Products {
				// A product bound to two processes starts them by hand, so each must
				// be one this example ships, entered at a start of its own.
				for _, proc := range []string{it.ProvisionProcess, it.DeprovisionProcess} {
					if proc == "" {
						continue
					}
					if _, hasNone, deployed := processes.EntryPoints(proc); !deployed {
						t.Errorf("product %s is bound to %s, which this example does not ship", it.ID, proc)
					} else if !hasNone {
						t.Errorf("product %s is bound to %s, which has no plain start for the order to take", it.ID, proc)
					}
				}
				if it.ConfigForm != "" && !forms[it.ConfigForm] {
					t.Errorf("product %s asks for form %s, which this example does not ship", it.ID, it.ConfigForm)
				}
				for _, a := range it.Actions {
					if a.Form != "" && !forms[a.Form] {
						t.Errorf("action %s of %s asks for form %s, which this example does not ship", a.Key, it.ID, a.Form)
					}
				}
			}
		})
	}
}

// shopQuestion is one entry of fragen.json.
type shopQuestion struct {
	Kind  string            `json:"kind"`
	Label map[string]string `json:"label"`
	Hint  map[string]string `json:"hint"`
}

// TestEveryPlaceholderIsAsked: what a document leaves to the reader is asked by the
// package's own questions, which the shop handbook's installer and `atlas import`
// both read. A placeholder without a question would be imported literally — an
// audience group named "{{zielgruppe}}" reaches nobody — and a question no
// placeholder uses is one the reader answers for nothing.
func TestEveryPlaceholderIsAsked(t *testing.T) {
	for _, path := range shopDocuments(t) {
		_, names := readShopDocument(t, path)
		if len(names) == 0 {
			t.Errorf("%s leaves nothing to the reader; an audience and the approvers belong to their installation", path)
		}
		qpath := filepath.Join(filepath.Dir(path), shopQuestionsName)
		raw, err := os.ReadFile(qpath)
		if err != nil {
			t.Fatalf("%s: %v", qpath, err)
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		var questions map[string]shopQuestion
		if err := dec.Decode(&questions); err != nil {
			t.Fatalf("%s: %v", qpath, err)
		}
		used := map[string]bool{}
		for _, name := range names {
			used[name] = true
			q, ok := questions[name]
			switch {
			case !ok:
				t.Errorf("%s leaves {{%s}} to the reader, and %s does not ask for it", path, name, qpath)
			case q.Kind != "group" && q.Kind != "user" && q.Kind != "text":
				t.Errorf("%s: {{%s}} is a %q question; a question asks for a group, a user or text", qpath, name, q.Kind)
			case q.Label["de"] == "" || q.Label["en"] == "":
				t.Errorf("%s: {{%s}} is not asked in both languages", qpath, name)
			}
		}
		for name := range questions {
			if !used[name] {
				t.Errorf("%s asks for %s, which %s does not leave open", qpath, name, path)
			}
		}
	}
}

// packageManifest is the part of atlas.json these tests hold to the directory.
type packageManifest struct {
	FormatVersion int    `json:"formatVersion"`
	Key           string `json:"key"`
	Name          string `json:"name"`
	Processes     []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Path string `json:"path"`
	} `json:"processes"`
	Forms []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Path string `json:"path"`
	} `json:"forms"`
	Decisions []json.RawMessage `json:"decisions"`
}

func readPackageManifest(t *testing.T, dir string) packageManifest {
	t.Helper()
	path := filepath.Join(dir, packageManifestName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("a shop example is a package, and %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var man packageManifest
	if err := dec.Decode(&man); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return man
}

// applicationKey is a key as the server derives one from a name (api/appsource.go
// slugify): lower case, umlauts spelled out, every other run of characters a dash.
var applicationKey = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// TestEveryPackageManifestNamesWhatItShips: the source import writes what the
// manifest lists, under the ids the manifest gives, and does not open a file to
// check them. So a process the manifest forgets is never deployed, and one listed
// under an id its BPMN does not carry is saved as a draft the catalogue's binding
// cannot find. The manifest is therefore held to the directory: every process and
// every form in it, each under its own id.
func TestEveryPackageManifestNamesWhatItShips(t *testing.T) {
	for _, path := range shopDocuments(t) {
		dir := filepath.Dir(path)
		man := readPackageManifest(t, dir)
		if man.FormatVersion != 1 || man.Name == "" || !applicationKey.MatchString(man.Key) {
			t.Errorf("%s: format %d, key %q, name %q — want format 1, a slug key and a name", dir, man.FormatVersion, man.Key, man.Name)
		}

		listed := map[string]string{}
		for _, p := range man.Processes {
			listed[p.Path] = p.ID
			raw, err := os.ReadFile(filepath.Join(dir, p.Path))
			if err != nil {
				t.Errorf("%s lists process %s at %s: %v", dir, p.ID, p.Path, err)
				continue
			}
			cp, err := compiler.Parse(1, 1, bytes.NewReader(raw))
			if err != nil {
				t.Errorf("%s: %v", p.Path, err)
			} else if cp.ProcessId() != p.ID {
				t.Errorf("%s lists %s as process %s; its BPMN says %s", dir, p.Path, p.ID, cp.ProcessId())
			}
		}
		bpmns, _ := filepath.Glob(filepath.Join(dir, "*.bpmn"))
		for _, b := range bpmns {
			if _, ok := listed[filepath.Base(b)]; !ok {
				t.Errorf("%s ships %s, which its manifest does not list", dir, filepath.Base(b))
			}
		}

		forms := exampleFormIDs(t, dir)
		inManifest := map[string]bool{}
		for _, f := range man.Forms {
			inManifest[f.ID] = true
			raw, err := os.ReadFile(filepath.Join(dir, f.Path))
			var form struct {
				ID string `json:"id"`
			}
			if err != nil || json.Unmarshal(raw, &form) != nil || form.ID != f.ID {
				t.Errorf("%s lists %s as form %s; the file says %q (%v)", dir, f.Path, f.ID, form.ID, err)
			}
		}
		for id := range forms {
			if !inManifest[id] {
				t.Errorf("%s ships form %s, which its manifest does not list", dir, id)
			}
		}
	}
}
