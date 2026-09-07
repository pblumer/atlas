package examples

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The handbook's examples chapter (api/web/handbuch.html, "Beispiele") shows every
// scenario in this directory: what it is for, how it is built, and what it takes to
// put its workers into service. Each card renders the real diagram and offers to
// install the real artifacts into the reader's own instance.
//
// It must not carry a second copy of the models to do that. The workshop chapter
// already showed why: a copy drifts, the page keeps teaching the old model, and
// nothing notices. So the models travel as one generated asset — api/web/
// examples-catalog.json — which the chapter fetches when it scrolls into view.
// This test is the generator and the guard in one: `go test ./examples -update`
// writes the catalog from the files here, and a plain run fails when the served
// catalog no longer matches them.
//
// Deliberately one-directional, like the workshop's block: the files in this
// directory are the source, the catalog is the copy. Editing the catalog by hand is
// how the drift starts.
const catalogPath = "../api/web/examples-catalog.json"

// catalogSource states which files make up one example, and how the chapter's
// install button files them. Everything else — process ids, decision names, form
// ids, the artifacts themselves — is read from the files, so adding a form to an
// example is a `-update` and not an edit here.
//
// Dir claims a whole directory (recursively); Files claim single models from the
// flat part of examples/, where a dozen independent scenarios share one folder.
// TestCatalogCoversEveryArtifact checks that between them they claim everything.
type catalogSource struct {
	ID    string // the card's anchor in the handbook: id="bsp-<ID>"
	App   string // the application the install creates, and looks up by name to reuse
	Dir   string // a directory whose artifacts are all this example's
	Files []string
	// Main is the model the card leads with: its diagram, its "open in the Modeler",
	// its link into the repository. Without it the order would be alphabetical, and
	// alphabetical is wrong wherever a connection test or a variant sorts ahead of the
	// model the card is about — jira-verbindungstest.bpmn before jira-zugangsantrag.bpmn,
	// the one-step variant of the trip booking before the trip booking. Required as
	// soon as an example carries more than one process.
	Main string
	// Start, when set, gives the card a "run it" button: the process to start and
	// the variables to start it with. Only examples that reach an end event without
	// a configured worker get one — everything else would park a token or raise an
	// incident in the reader's instance, which teaches nothing.
	Start *catalogStart
	// NoInstall marks an example the chapter must not offer to install: one with
	// nothing deployable (a study, a data model), or one the server refuses because it
	// already owns the artifacts (the system processes). It still gets a card, and
	// still opens in the Modeler; it just has no install button.
	NoInstall bool
}

type catalogStart struct {
	ProcessID string
	Variables map[string]any
}

// catalogSources is the map from the directory to the chapter. The order here is
// the order of the cards in the page: the credential-free scenarios first, because
// they are the ones a reader can run in the next minute.
var catalogSources = []catalogSource{
	// --- Ohne Anbindung: laufen ohne einen einzigen konfigurierten Worker ---
	{
		ID: "pruefe-auftrag", App: "Beispiel: Auftrag prüfen",
		Files: []string{"pruefe-auftrag.bpmn"},
		// Above the 1000 EUR limit on purpose: the default cart the model falls back
		// to is below it, so a bare start would only ever show the uninteresting half.
		Start: &catalogStart{"proc_check_order", map[string]any{
			"auftrag": map[string]any{"kunde": "Muster GmbH", "betrag": 1500, "artikel": 12},
		}},
	},
	{
		ID: "cart-total", App: "Beispiel: Warenkorb",
		Files: []string{"cart-total.bpmn"},
		Start: &catalogStart{"cart-total", map[string]any{
			"positions": []any{
				map[string]any{"name": "BPMN-Buch", "price": 24.90, "qty": 1},
				map[string]any{"name": "Kaffeetasse", "price": 9.95, "qty": 2},
				map[string]any{"name": "Aufkleber-Set", "price": 4.50, "qty": 3},
			},
			"customerType": "Business",
		}},
	},
	{
		ID: "order-fulfillment", App: "Beispiel: Order Fulfillment",
		Files: []string{"order-fulfillment.bpmn"},
		Start: &catalogStart{"order-fulfillment", map[string]any{}},
	},
	{
		ID: "bonitaet-mockup", App: "Beispiel: Bonitätsprüfung (Mockup)",
		Files: []string{"bonitaet-mockup.bpmn"},
		Start: &catalogStart{"proc_bonitaet_mockup", map[string]any{"betrag": 7500}},
	},
	{
		ID: "reisestorno", App: "Beispiel: Reise-Rückabwicklung",
		Dir: "reisestorno",
		// zahlungOk false on purpose: the compensation path is the half worth seeing,
		// and the other one is a start variable away.
		Start: &catalogStart{"proc_reisestorno", map[string]any{
			"reiseziel": "Lissabon", "reisende": 2, "zahlungOk": false,
		}},
	},
	{
		ID: "mahnwesen", App: "Beispiel: Mahnwesen",
		Dir: "mahnwesen",
		// Seconds instead of the P10D/P20D the model defaults to: a reader watching a
		// dunning run wants to see the escalation open the task, not wait a fortnight.
		Start: &catalogStart{"proc_mahnwesen", map[string]any{
			"rechnungsnummer": "RE-2026-0042", "kunde": "Muster GmbH", "betrag": 1250,
			"zahlungsfrist": "PT10S", "nachfrist": "PT10S",
		}},
	},
	{
		ID: "preisaenderung", App: "Beispiel: Preisänderung",
		Dir: "preisaenderung", Main: "preisaenderung/offerte.bpmn",
		// Starts an offer, which parks at the customer decision — that is the state the
		// signal is thrown into. The card says to start the price list next.
		Start: &catalogStart{"proc_offerte", map[string]any{
			"kunde": "Kunde A", "artikel": "Zaunfeld verzinkt", "menge": 40, "einzelpreis": 89.5,
		}},
	},
	{ID: "pruefung", App: "Beispiel: Prüfung", Dir: "pruefung"},
	{ID: "reisebuchung", App: "Beispiel: Reisebuchung", Dir: "reisebuchung", Main: "reisebuchung/reisebuchung.bpmn"},
	{ID: "bewerbermanagement", App: "Bewerbermanagement", Dir: "bewerbermanagement", Main: "bewerbermanagement/bewerbung.bpmn"},
	{
		ID: "pruefe-datensaetze", App: "Beispiel: Datensätze prüfen",
		Files: []string{"pruefe-datensaetze.bpmn", "pruefe-datensaetze.dmn",
			"csv-upload-form.json", "row-correction-form.json"},
	},
	{ID: "onboarding", App: "Onboarding", Dir: "onboarding"},
	{
		ID: "order-to-cash", App: "Beispiel: Order-to-Cash",
		Files: []string{"order-to-cash.bpmn", "order-to-cash-live.bpmn"},
		Main:  "order-to-cash.bpmn",
	},

	// --- Aus dem Netz lesen: laufen ohne Zugangsdaten, brauchen aber Internet ---
	{
		ID: "blick-schlagzeilen", App: "Beispiel: Schlagzeilen",
		Files: []string{"blick-schlagzeilen.bpmn"},
		Start: &catalogStart{"proc_blick_schlagzeilen", map[string]any{"suchwort": "Schweiz"}},
	},
	{
		ID: "hypothekarzinsen", App: "Beispiel: Hypothekarzinsen",
		Files: []string{"hypothekarzinsen-migrosbank.bpmn"},
	},

	// --- Mit Umsystem: brauchen einen konfigurierten Worker ---
	{ID: "google-sheets-antragseingang", App: "Beispiel: Antragseingang (Sheets)", Dir: "google-sheets-antragseingang",
		Main: "google-sheets-antragseingang/antragseingang.bpmn"},
	{ID: "jira-zugangsantrag", App: "Beispiel: Zugangsantrag (Jira)", Dir: "jira-zugangsantrag",
		Main: "jira-zugangsantrag/jira-zugangsantrag.bpmn"},
	{ID: "jira-ticket-eingang", App: "Beispiel: Ticket-Eingang (Jira)", Dir: "jira-ticket-eingang"},
	{
		ID: "mssql-eintrittsmeldung", App: "Beispiel: Eintrittsmeldung (MS SQL)",
		Files: []string{"mssql-eintrittsmeldung.bpmn"},
	},

	// --- Identität & Verzeichnis ---
	{
		ID: "ad-gruppenzuweisung", App: "Beispiel: AD-Gruppenzuweisung",
		Files: []string{"ad-gruppenzuweisung.bpmn"},
	},
	{ID: "galsync", App: "Beispiel: GALSync", Files: []string{"galsync.bpmn"}},
	{
		ID: "entra-team-onboarding", App: "Beispiel: Team-Onboarding (Entra)",
		Files: []string{"entra-team-onboarding.bpmn", "entra-create-account.bpmn"},
		Main:  "entra-team-onboarding.bpmn",
	},
	{
		ID: "entra-rezertifizierung", App: "Beispiel: Konten-Rezertifizierung (Entra)",
		Files: []string{"entra-konten-rezertifizierung.bpmn"},
	},
	{
		ID: "entra-delta-sync", App: "Beispiel: Delta-Sync (Entra)",
		Files: []string{"entra-delta-sync.bpmn"},
	},
	{ID: "entra-onboarding-selfservice", App: "Beispiel: Onboarding-Self-Service (Entra)", Dir: "entra-onboarding-selfservice"},
	{ID: "account-bestellung", App: "Beispiel: Account-Bestellung (Entra)", Dir: "account-bestellung"},
	// No install: these three ship inside the binary and are deployed into the
	// protected system project at server start, so their forms are protected system
	// forms — saving one answers 403 "protected system form cannot be modified", and
	// an install button could only ever fail. The card says so and offers the Modeler
	// instead.
	{ID: "benutzerverwaltung", Dir: "benutzerverwaltung", NoInstall: true,
		Main: "benutzerverwaltung/benutzer-aufnahme.bpmn"},
	{ID: "identitaet-lebenszyklus", App: "Beispiel: Identitäts-Lebenszyklus", Dir: "identitaet-lebenszyklus",
		Main: "identitaet-lebenszyklus/identitaet-lebenszyklus.bpmn"},
	{ID: "ad-objektmodell", Dir: "ad-objektmodell", NoInstall: true},
}

// formDisplayNames gives a form the name the Modeler and the Tasks inbox list it
// by. A form-js schema carries no name of its own, so it lives here rather than in
// a non-standard key inside the schema file — the same reason handbook_test.go
// carries the workshop's three. Keyed by form id, which
// TestFormIDsAreUniqueAcrossExamples keeps unique. A form with no entry is listed
// under its id.
var formDisplayNames = map[string]string{
	"mahn-entscheid":        "Mahnung – Entscheid",
	"offerte-entscheid":     "Offerte – Kundenentscheid",
	"bw-bewerbung-eingang":  "Bewerbung – Eingang",
	"bw-interview-feedback": "Interview – Feedback",
	"bw-entscheidung":       "Bewerbung – Entscheidung",
	"pruefung-fragen":       "Prüfung – Fragebogen",
	"reise-start":           "Reise – Antrag",
	"reise-visum":           "Reise – Visum",
	"reise-einverstaendnis": "Reise – Einverständnis",
	"reise-bestaetigung":    "Reise – Bestätigung",
	"reise-antrag":          "Reise – Antrag (ein Schritt)",
	"csv-upload-form":       "CSV hochladen",
	"row-correction-form":   "Zeile korrigieren",
	"account-order":         "Account bestellen",
	"account-freigabe":      "Account – Freigabe",
	"onb-start":             "Onboarding – Start",
	"onb-welcome":           "Onboarding – Willkommen",
	"onb-concepts":          "Onboarding – Grundbegriffe",
	"onb-architecture":      "Onboarding – Architektur",
	"onb-devsetup":          "Onboarding – Setup",
	"onb-finish":            "Onboarding – Abschluss",
	"eonb-start":            "Self-Service – Start",
	"eonb-freigabe":         "Self-Service – Freigabe",
	"za-antrag":             "Zugangsantrag",
	"za-freigabe":           "Zugangsantrag – Freigabe",
	"ba-antrag":             "Benutzer aufnehmen – Antrag",
	"ba-konto":              "Benutzer aufnehmen – Konto",
	"off-antrag":            "Offboarding – Antrag",
	"off-sperren":           "Offboarding – Sperren",
	"rev-start":             "Zugriffs-Review – Start",
	"rev-pruefen":           "Zugriffs-Review – Prüfen",
}

// --- the served shape ---

// catalog is what api/web/examples-catalog.json holds and the chapter fetches.
type catalog struct {
	Examples []catalogExample `json:"examples"`
}

type catalogExample struct {
	ID string `json:"id"`
	// Path is where the example lives under examples/ — a directory for the ones that
	// have their own, the model file for the ones that share the flat folder. The card
	// links there.
	Path string `json:"path"`
	// App is the application the install creates. Empty for a study with nothing to
	// deploy; the page renders that card without its buttons.
	App       string            `json:"app,omitempty"`
	Processes []catalogProcess  `json:"processes,omitempty"`
	Decisions []catalogDecision `json:"decisions,omitempty"`
	Forms     []catalogForm     `json:"forms,omitempty"`
	Start     *catalogStartJSON `json:"start,omitempty"`
}

type catalogProcess struct {
	ProcessID string `json:"processId"`
	Name      string `json:"name"`
	File      string `json:"file"`
	// Layout is true for a model that ships without BPMN-DI: the page has to ask
	// POST /api/v1/layout for coordinates before it can render it. Four of the
	// older models are in that state; a model authored since then carries its own
	// diagram (AGENTS.md, "Authoring BPMN models").
	Layout bool   `json:"layout,omitempty"`
	XML    string `json:"xml"`
}

type catalogDecision struct {
	Name string `json:"name"`
	// Handle is the key the model is stored under in the local DMN folder, and it is
	// deliberately not the name: the upload folds a handle to lower case with every run
	// of non-alphanumerics collapsed to "-" (api/dmnupload.go, sanitizeHandle). Sending
	// "Notenschluessel" therefore stores "notenschluessel", and a reference pointing at
	// the unfolded name resolves against nothing — the publish then refuses the whole
	// application with "no temis model matches this reference". Folding it here means the
	// handle survives the upload unchanged; the page additionally prefers the modelRef
	// the upload reports back, so the two cannot disagree.
	Handle string `json:"handle"`
	File   string `json:"file"`
	XML    string `json:"xml"`
}

type catalogForm struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	File   string          `json:"file"`
	Schema json.RawMessage `json:"schema"`
}

type catalogStartJSON struct {
	ProcessID string         `json:"processId"`
	Variables map[string]any `json:"variables"`
}

// TestExampleCatalogMatchesTheFiles keeps the served catalog equal to this
// directory. Run with -update after adding or changing a model, a decision or a
// form.
func TestExampleCatalogMatchesTheFiles(t *testing.T) {
	want := buildCatalog(t)

	got, err := os.ReadFile(catalogPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read %s: %v", catalogPath, err)
	}
	if string(got) == want {
		return
	}
	if *update {
		if err := os.WriteFile(catalogPath, []byte(want), 0o644); err != nil {
			t.Fatalf("write %s: %v", catalogPath, err)
		}
		t.Logf("regenerated %s (%d bytes)", catalogPath, len(want))
		return
	}
	t.Errorf("%s no longer matches the files in examples/ (served %d bytes, files produce %d). "+
		"The files are the source: run `go test ./examples -update` and commit the regenerated catalog.",
		catalogPath, len(got), len(want))
}

// TestCatalogCoversEveryArtifact is the reason the chapter cannot quietly fall
// behind the directory. Every deployable artifact here must be claimed by exactly
// one catalogSource — so a new example that nobody wrote a card for fails the
// build instead of shipping unmentioned, which is how examples/README.md came to
// be missing five of them.
func TestCatalogCoversEveryArtifact(t *testing.T) {
	claimed := map[string]string{}
	for _, src := range catalogSources {
		for _, f := range artifactsOf(t, src) {
			if other, dup := claimed[f]; dup {
				t.Errorf("%s is claimed by both %q and %q — an artifact belongs to one example", f, other, src.ID)
				continue
			}
			claimed[f] = src.ID
		}
	}
	for _, f := range allArtifacts(t) {
		if _, ok := claimed[f]; !ok {
			t.Errorf("%s belongs to no example in catalogSources — add it to an existing entry or "+
				"give it one of its own, and write its card in the handbook's Beispiele chapter", f)
		}
	}
}

// TestEveryExampleHasAHandbookCard ties the catalog to the page in both
// directions: an example with no card is undocumented, and a card with no example
// points at a model the reader cannot install.
func TestEveryExampleHasAHandbookCard(t *testing.T) {
	page, err := os.ReadFile(handbookPath)
	if err != nil {
		t.Fatalf("read %s: %v", handbookPath, err)
	}
	chapter := exampleChapter(t, string(page))

	inPage := map[string]bool{}
	for _, id := range cardIDs(chapter) {
		inPage[id] = true
	}
	for _, src := range catalogSources {
		if !inPage[src.ID] {
			t.Errorf("example %q has no card in the handbook's Beispiele chapter — expected "+
				`<div class="bsp" id="bsp-%s">`, src.ID, src.ID)
		}
		delete(inPage, src.ID)
	}
	for id := range inPage {
		t.Errorf("the handbook has a card bsp-%s, but no example of that id is in catalogSources — "+
			"the card's install button has nothing to install", id)
	}
}

// TestFormIDsAreUniqueAcrossExamples guards a collision the reader meets and the
// author never does: a form is stored under its id alone (api/formstore.go), with
// no project in the key. Two examples shipping the same form id means installing
// the second one silently overwrites the first one's form, and its user task then
// renders somebody else's fields.
func TestFormIDsAreUniqueAcrossExamples(t *testing.T) {
	seen := map[string]string{}
	for _, src := range catalogSources {
		for _, f := range formsOf(t, src) {
			if other, dup := seen[f.ID]; dup {
				t.Errorf("form id %q is used by both %s and %s — forms are stored by id alone, so "+
					"installing both examples overwrites one of them", f.ID, other, f.File)
				continue
			}
			seen[f.ID] = f.File
		}
	}
}

// --- building the catalog ---

func buildCatalog(t *testing.T) string {
	t.Helper()
	cat := catalog{}
	for _, src := range catalogSources {
		files := artifactsOf(t, src)
		ex := catalogExample{ID: src.ID, Path: src.Dir}
		if ex.Path == "" {
			ex.Path = mainOf(t, src, files)
		}
		if !src.NoInstall {
			ex.App = src.App
		}
		for _, f := range files {
			switch {
			case strings.HasSuffix(f, ".bpmn"):
				id, name := processIdentity(t, f)
				ex.Processes = append(ex.Processes, catalogProcess{
					ProcessID: id, Name: name, File: f,
					Layout: !strings.Contains(readFile(t, f), "BPMNDiagram"),
					XML:    strings.TrimRight(readFile(t, f), "\n"),
				})
			case strings.HasSuffix(f, ".dmn"):
				name := decisionName(t, f)
				ex.Decisions = append(ex.Decisions, catalogDecision{
					Name: name, Handle: foldHandle(name), File: f,
					XML: strings.TrimRight(readFile(t, f), "\n"),
				})
			default:
				form := readForm(t, f)
				ex.Forms = append(ex.Forms, form)
			}
		}
		if src.Start != nil {
			vars := src.Start.Variables
			if vars == nil {
				vars = map[string]any{}
			}
			ex.Start = &catalogStartJSON{ProcessID: src.Start.ProcessID, Variables: vars}
		}
		cat.Examples = append(cat.Examples, ex)
	}
	out, err := json.MarshalIndent(cat, "", "  ")
	if err != nil {
		t.Fatalf("marshal catalog: %v", err)
	}
	return string(out) + "\n"
}

// artifactsOf returns the example's deployable files, relative to this directory,
// in a stable order: processes, decisions, forms, each sorted by path. A
// directory-backed example is walked; a file-backed one is taken as listed.
func artifactsOf(t *testing.T, src catalogSource) []string {
	t.Helper()
	var files []string
	if src.Dir != "" {
		err := filepath.WalkDir(src.Dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && isArtifact(path) {
				files = append(files, filepath.ToSlash(path))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", src.Dir, err)
		}
	}
	for _, f := range src.Files {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("example %q lists %s, which is not here: %v", src.ID, f, err)
		}
		files = append(files, f)
	}
	// Rank first (processes, then decisions, then forms), and inside a rank the
	// example's Main model leads. Everything else keeps a stable order: the listed
	// order for Files, path order for a walked directory.
	order := map[string]int{}
	for i, f := range files {
		order[f] = i
	}
	main := mainOf(t, src, files)
	sort.SliceStable(files, func(i, j int) bool {
		if ri, rj := artifactRank(files[i]), artifactRank(files[j]); ri != rj {
			return ri < rj
		}
		if files[i] == main != (files[j] == main) {
			return files[i] == main
		}
		if src.Dir != "" {
			return files[i] < files[j]
		}
		return order[files[i]] < order[files[j]]
	})
	return files
}

// mainOf returns the model the example leads with, checking that a stated one is
// really there — a Main naming a file that was renamed would otherwise silently
// fall back to whatever sorts first, which is the bug this field exists to prevent.
func mainOf(t *testing.T, src catalogSource, files []string) string {
	t.Helper()
	var models []string
	for _, f := range files {
		if strings.HasSuffix(f, ".bpmn") {
			models = append(models, f)
		}
	}
	if src.Main != "" {
		for _, f := range models {
			if f == src.Main {
				return src.Main
			}
		}
		t.Fatalf("example %q: Main names %s, which is not one of its models %v", src.ID, src.Main, models)
	}
	if len(models) > 1 {
		t.Fatalf("example %q carries %d models %v but states no Main — the card would lead with "+
			"whichever sorts first, which is how a connection test ends up illustrating the model "+
			"it only tests", src.ID, len(models), models)
	}
	if len(models) == 1 {
		return models[0]
	}
	return ""
}

// isArtifact reports whether a path is something an install files into an
// application. The .json test is deliberately narrow: this directory also holds an
// information model and an identity export, which are not forms.
func isArtifact(path string) bool {
	switch {
	case strings.HasSuffix(path, ".bpmn"), strings.HasSuffix(path, ".dmn"):
		return true
	case strings.HasSuffix(path, ".json"):
		var probe struct {
			Type       string          `json:"type"`
			Components json.RawMessage `json:"components"`
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		if json.Unmarshal(b, &probe) != nil {
			return false
		}
		return probe.Type == "default" && len(probe.Components) > 0
	}
	return false
}

func artifactRank(path string) int {
	switch {
	case strings.HasSuffix(path, ".bpmn"):
		return 0
	case strings.HasSuffix(path, ".dmn"):
		return 1
	default:
		return 2
	}
}

// allArtifacts walks this directory for everything an example could claim.
func allArtifacts(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && isArtifact(path) {
			out = append(out, filepath.ToSlash(path))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk examples: %v", err)
	}
	return out
}

func formsOf(t *testing.T, src catalogSource) []catalogForm {
	t.Helper()
	var out []catalogForm
	for _, f := range artifactsOf(t, src) {
		if !strings.HasSuffix(f, ".bpmn") && !strings.HasSuffix(f, ".dmn") {
			out = append(out, readForm(t, f))
		}
	}
	return out
}

func readForm(t *testing.T, path string) catalogForm {
	t.Helper()
	raw := readFile(t, path)
	var doc struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("%s is not valid JSON: %v", path, err)
	}
	if doc.ID == "" {
		t.Fatalf("%s has no id — a user task binds a form by id, so a form without one binds to nothing", path)
	}
	name := formDisplayNames[doc.ID]
	if name == "" {
		name = doc.Name
	}
	if name == "" {
		name = doc.ID
	}
	return catalogForm{ID: doc.ID, Name: name, File: path, Schema: json.RawMessage(compactJSON(t, raw))}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// processIdentity reads the <process> id and name straight out of the XML. Both
// are attributes on one element, so a small scan beats pulling in a parser: the
// compiler already proves these files are well-formed (models_test.go).
func processIdentity(t *testing.T, path string) (id, name string) {
	t.Helper()
	src := readFile(t, path)
	for _, open := range []string{"<process ", "<bpmn:process "} {
		i := strings.Index(src, open)
		if i < 0 {
			continue
		}
		tag := src[i:]
		if end := strings.Index(tag, ">"); end >= 0 {
			tag = tag[:end]
		}
		return attr(tag, "id"), attr(tag, "name")
	}
	t.Fatalf("%s has no <process> element", path)
	return "", ""
}

func decisionName(t *testing.T, path string) string {
	t.Helper()
	src := readFile(t, path)
	i := strings.Index(src, "<decision ")
	if i < 0 {
		t.Fatalf("%s has no <decision> element", path)
	}
	tag := src[i:]
	if end := strings.Index(tag, ">"); end >= 0 {
		tag = tag[:end]
	}
	// Atlas resolves a decision by name, and the shipped models keep id and name
	// identical for exactly that reason. Prefer the name, fall back to the id.
	if n := attr(tag, "name"); n != "" {
		return n
	}
	return attr(tag, "id")
}

// foldHandle mirrors the fold the upload applies to ?handle= (api/dmnupload.go,
// sanitizeHandle): lower case, every run of non-alphanumerics collapsed to a single
// "-", no leading or trailing "-". Applying it here rather than sending the raw name
// makes the upload a no-op fold, so the stored handle is exactly what the reference
// names.
func foldHandle(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
			continue
		}
		dash = true
	}
	return b.String()
}

func attr(tag, name string) string {
	i := strings.Index(tag, " "+name+`="`)
	if i < 0 {
		return ""
	}
	rest := tag[i+len(name)+3:]
	if end := strings.Index(rest, `"`); end >= 0 {
		return rest[:end]
	}
	return ""
}

// --- reading the chapter ---

const cardOpen = `<div class="bsp" id="bsp-`

func exampleChapter(t *testing.T, page string) string {
	t.Helper()
	i := strings.Index(page, `<section id="beispiele">`)
	if i < 0 {
		t.Fatalf("%s has no <section id=\"beispiele\"> — the chapter this test guards is gone", handbookPath)
	}
	rest := page[i:]
	end := strings.Index(rest, "</section>")
	if end < 0 {
		t.Fatalf("the beispiele section is not closed")
	}
	return rest[:end]
}

func cardIDs(chapter string) []string {
	var out []string
	rest := chapter
	for {
		i := strings.Index(rest, cardOpen)
		if i < 0 {
			return out
		}
		rest = rest[i+len(cardOpen):]
		end := strings.Index(rest, `"`)
		if end < 0 {
			return out
		}
		out = append(out, rest[:end])
	}
}
