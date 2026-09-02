package api

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The Modeler's Worker picker.
//
// A connector task names one *configured Worker* — a target and identity an operator
// registered on this server, not a Worker Type and not a Worker Instance (ADR-0203).
// The panel therefore offers the server's configured Workers of the task's type as a
// dropdown, because the one thing an author cannot know from the model is which names
// exist in the Console.
//
// Two ways that quietly stops working, both guarded here: a picker named for a Worker
// Type this server does not manage matches no record and silently offers nothing, and
// a Worker field added without a picker is back to a free-text field — which, in the
// panel, looks exactly like a dropdown that happens to be empty.

// modelerPickerKindRe matches the `datalist: "jira"` of a catalog field, and
// modelerPickerCallRe the Worker Type passed to fillWorkerDatalist by a panel that
// builds its own markup (the business rule task's temis Worker).
var (
	modelerPickerKindRe = regexp.MustCompile(`datalist:\s*"([a-z0-9]+)"`)
	modelerPickerCallRe = regexp.MustCompile(`fillWorkerDatalist\(.*?"([a-z0-9]+)"\s*\)`)
)

// TestEveryModelerWorkerPickerNamesAManagedWorkerType keeps the dropdown's filter and
// the server's Worker Types on the same names. The picker filters the configured
// Workers by kind, so a typo there is not a visible error: the field just never
// suggests anything, which is what a server with nothing configured also looks like.
func TestEveryModelerWorkerPickerNamesAManagedWorkerType(t *testing.T) {
	src := modelerSource(t)
	managed := make(map[string]bool, len(managedConnectorKinds))
	for _, k := range managedConnectorKinds {
		managed[k.name] = true
	}
	// The three database kinds contribute nothing here, on purpose: sqlServiceTaskKind
	// derives their datalist from the kind's own id rather than repeating the name, so
	// the typo this test exists to catch cannot be written for them.
	// TestEveryModelerWorkerFieldOffersThePicker still checks that their shared field
	// declares a picker at all.
	var pickers, unknown []string
	for _, re := range []*regexp.Regexp{modelerPickerKindRe, modelerPickerCallRe} {
		for _, m := range re.FindAllStringSubmatch(src, -1) {
			pickers = append(pickers, m[1])
			if !managed[m[1]] {
				unknown = append(unknown, m[1])
			}
		}
	}
	if len(pickers) == 0 {
		t.Fatal("no Worker picker found in editor.js; the datalist wiring must have been renamed")
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		t.Errorf("api/web/editor.js offers a Worker picker for %d Worker Type(s) this server does not manage: %s\n\n"+
			"The dropdown filters the configured Workers by that name, so it will always be empty. "+
			"Use a name from managedConnectorKinds in api/connectorkinds.go.",
			len(unknown), strings.Join(unknown, ", "))
	}
}

// workerFieldsWithoutAPicker records, per catalog kind, why its Worker field offers no
// dropdown. An entry is a reason, not a permanent exemption.
var workerFieldsWithoutAPicker = map[string]string{
	"entra": "the field is fx-capable so one process can serve several tenants, and an fx " +
		"field renders as the FEEL editor's textarea, which cannot carry a datalist",
}

// TestEveryModelerWorkerFieldOffersThePicker is the other half: a Worker field is the
// one field whose value an author cannot derive from the model, so it offers the names
// the server actually has. A new kind that ships without the picker sends its author
// back to guessing, and nothing else in the panel says so.
func TestEveryModelerWorkerFieldOffersThePicker(t *testing.T) {
	// The whole file, not only the catalog array: the three database kinds are built by
	// a shared function (sqlServiceTaskKind) that declares their Worker field once, and
	// scanning only the array body would quietly stop covering the field three Worker
	// Types depend on.
	fields := modelerWorkerFields(t, modelerSource(t))

	var missing []string
	has := map[string]bool{}
	for _, f := range fields {
		has[f.kind] = true
		if strings.Contains(f.body, "datalist:") {
			if _, exempt := workerFieldsWithoutAPicker[f.kind]; exempt {
				t.Errorf("catalog kind %q offers a Worker picker but is still listed in "+
					"workerFieldsWithoutAPicker; drop the entry", f.kind)
			}
			continue
		}
		if _, exempt := workerFieldsWithoutAPicker[f.kind]; exempt {
			continue
		}
		missing = append(missing, f.kind)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("api/web/editor.js has %d Worker field(s) with no picker: %s\n\n"+
			"The field then asks for the name of a configured Worker the author has no way to "+
			"see. Add `datalist: \"<worker type>\"` to the field, or record why it cannot have "+
			"one in workerFieldsWithoutAPicker.",
			len(missing), strings.Join(missing, ", "))
	}
	for kind := range workerFieldsWithoutAPicker {
		if !has[kind] {
			t.Errorf("workerFieldsWithoutAPicker names %q, which has no Worker field in the "+
				"catalog any more; the entry is stale", kind)
		}
	}
}

// modelerWorkerField is one catalog field that names a configured Worker: the id of
// the kind that declares it, and the field's own source.
type modelerWorkerField struct{ kind, body string }

// workerFieldKeyRe matches the `key: "connector"` that opens such a field. The
// attribute keeps its pre-ADR-0203 spelling because it is the BPMN attribute name.
var workerFieldKeyRe = regexp.MustCompile(`key:\s*"connector"`)

// modelerWorkerFields cuts every Worker field out of the Modeler source. A field runs
// from its own `key:` to the next one, which is enough to see whether it declares a
// picker without teaching the test to parse JavaScript.
func modelerWorkerFields(t *testing.T, catalog string) []modelerWorkerField {
	t.Helper()
	var out []modelerWorkerField
	for _, loc := range workerFieldKeyRe.FindAllStringIndex(catalog, -1) {
		body := catalog[loc[0]:]
		if next := strings.Index(body[loc[1]-loc[0]:], `key: "`); next >= 0 {
			body = body[:loc[1]-loc[0]+next]
		}
		out = append(out, modelerWorkerField{kind: modelerCatalogKindAt(catalog, loc[0]), body: body})
	}
	if len(out) < 5 {
		t.Fatalf("found only %d Worker field(s) in SERVICE_TASK_KINDS; the pattern must have changed", len(out))
	}
	return out
}

// modelerCatalogKindAt names the kind a position in the Modeler source belongs to: the
// last `id:` declared before it, since a kind opens with its id.
//
// A field can also live in a function that builds several kinds — sqlServiceTaskKind
// declares the Worker field the three database Worker Types share — and there the last
// `id:` before it names something else entirely. So a function declared *after* that id
// wins: the answer is then the builder's own name, which is where a reader has to go to
// fix the field.
func modelerCatalogKindAt(catalog string, idx int) string {
	kind := "(before the first kind)"
	at := -1
	if m := catalogKindIDRe.FindAllStringSubmatchIndex(catalog[:idx], -1); len(m) > 0 {
		last := m[len(m)-1]
		kind, at = catalog[last[2]:last[3]], last[0]
	}
	if f := catalogBuilderRe.FindAllStringSubmatchIndex(catalog[:idx], -1); len(f) > 0 {
		last := f[len(f)-1]
		if last[0] > at {
			return "the shared field builder " + catalog[last[2]:last[3]]
		}
	}
	return kind
}

// catalogBuilderRe matches the `function sqlServiceTaskKind(` of a function that builds
// catalog entries rather than writing them out.
var catalogBuilderRe = regexp.MustCompile(`\bfunction ([A-Za-z0-9_]+)\(`)
