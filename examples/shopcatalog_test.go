package examples

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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

// shopDocumentName is the file an example's catalogue document lives in.
const shopDocumentName = "katalog.json"

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

// TestTheShopInstallerAsksForEveryPlaceholder: what a document leaves to the reader is
// asked by the shop handbook's installer, which reads its questions from one table. A
// placeholder that table does not know would be imported literally — an audience group
// named "{{zielgruppe}}" reaches nobody.
func TestTheShopInstallerAsksForEveryPlaceholder(t *testing.T) {
	page, err := os.ReadFile("../api/web/shop-handbuch.html")
	if err != nil {
		t.Fatalf("read the shop handbook: %v", err)
	}
	for _, path := range shopDocuments(t) {
		_, names := readShopDocument(t, path)
		if len(names) == 0 {
			t.Errorf("%s leaves nothing to the reader; an audience and the approvers belong to their installation", path)
		}
		for _, name := range names {
			if !strings.Contains(string(page), `"`+name+`": {`) {
				t.Errorf("%s leaves {{%s}} to the reader, and the installer's QUESTIONS table does not ask for it", path, name)
			}
		}
	}
}
