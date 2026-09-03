package api

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/sqldb"
)

// The three database Worker Types are one capability with three drivers (ADR-0173),
// and the Console has to present them as one. They differ in a driver name, a
// placeholder syntax and — for SQL Server alone — the ability to bind a parameter by
// name; in everything an operator does with them they are the same kind.
//
// Written out per product, the two Console surfaces had already drifted: the Worker
// catalog card told only SQL Server's reader that Atlas supervises the worker for
// them, only PostgreSQL's about the row cap, and none of the three that a database
// task can be tried without a database at all (ADR-0221). Which facts an operator
// learned depended on which of the three they clicked.
//
// Both surfaces are now built from one description per surface. These guards keep them
// that way, because re-inlining one product's entry is a one-line edit that nothing
// else would notice.

func webSource(t *testing.T, name string) string {
	t.Helper()
	body, err := fs.ReadFile(webFS, "web/"+name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(body)
}

// consoleCatalogDescRe matches one Worker catalog entry's id and how its description is
// produced — a shared builder call, or a literal string.
var consoleCatalogDescRe = regexp.MustCompile(`id: "([a-z0-9-]+)", name: "[^"]*", kind: "[^"]*",\s*\n\s*desc: (\w+\(|")`)

// The Worker catalog card on Console › Workers describes every SQL product through the
// same builder, so a sentence added for one reaches all three.
func TestTheConsoleCatalogDescribesEverySQLProductTheSameWay(t *testing.T) {
	src := webSource(t, "app.js")
	found := map[string]string{}
	for _, m := range consoleCatalogDescRe.FindAllStringSubmatch(src, -1) {
		found[m[1]] = m[2]
	}
	if len(found) == 0 {
		t.Fatal("no Worker catalog descriptions found in app.js; the pattern must have changed, and this guard would pass vacuously")
	}
	for _, kind := range sqlConnectorKinds() {
		switch found[kind] {
		case "":
			t.Errorf("the Worker catalog card has no entry for %q", kind)
		case "sqlWorkerTypeDesc(":
		default:
			t.Errorf("the Worker catalog entry for %q writes its own description (%s) instead of building it with sqlWorkerTypeDesc — "+
				"the three databases are one capability with three drivers, and a fact written into one entry is a fact the other two never tell anyone",
				kind, strings.TrimSuffix(found[kind], "("))
		}
	}
}

// And the Modeler's properties panel: one builder for the three, so a field, a hint or
// an operation added to one is added to all of them.
func TestTheModelerBuildsEverySQLPanelFromOneDescription(t *testing.T) {
	catalog := modelerCatalogSource(t)
	if !strings.Contains(catalog, "].map(sqlServiceTaskKind)") {
		t.Fatal("SERVICE_TASK_KINDS no longer builds the database kinds with sqlServiceTaskKind; " +
			"three hand-written panels are three chances for one product to lose a field the other two keep")
	}
	for _, kind := range sqlConnectorKinds() {
		p, ok := sqldb.ProductByName(kind)
		if !ok {
			t.Fatalf("Worker Type %q is not a sqldb product", kind)
		}
		entry := modelerSQLEntryRe(kind).FindStringSubmatch(catalog)
		if entry == nil {
			t.Errorf("the Modeler catalog has no database entry for %q stating a placeholder and an environment prefix", kind)
			continue
		}
		// The placeholder syntax is the one fact about a product an author *has* to see
		// in the panel, because it is what their statement must be written in — and it
		// is the fact the panel would be most quietly wrong about.
		if entry[1] != p.Placeholder {
			t.Errorf("the %s panel offers %q placeholders, but a %s statement binds with %q",
				kind, entry[1], kind, p.Placeholder)
		}
		// And the variable an operator running their own worker actually sets. The
		// panel quoting one the worker does not read is the failure this whole seam is
		// written to avoid, only shown to the author instead of to the operator.
		if want := strings.TrimSuffix(p.EnvPrefix(), "_"); "ATLAS_"+entry[2] != want {
			t.Errorf("the %s panel names ATLAS_%s_<NAME>_DSN, but a %s worker reads %s_<NAME>_DSN",
				kind, entry[2], kind, want)
		}
	}
}

// modelerSQLEntryRe matches one database product's row in the Modeler catalog's product
// table: its id, then the placeholder syntax and environment prefix that row states.
func modelerSQLEntryRe(kind string) *regexp.Regexp {
	return regexp.MustCompile(`id: "` + regexp.QuoteMeta(kind) + `",[\s\S]{0,400}?placeholder: "([^"]+)",\s*envPrefix: "([A-Z0-9_]+)"`)
}

// The Console catalog's descriptions quote the same two product facts, and they are
// written as arguments to the shared builder — so this reads them back off the call and
// holds them to the product table the engine uses. An operator who sets the variable a
// card names and finds the worker still reporting the database as unconfigured has been
// told something no code disagrees with anywhere else.
func TestTheConsoleCatalogQuotesEachSQLProductsOwnVariables(t *testing.T) {
	src := webSource(t, "app.js")
	for _, kind := range sqlConnectorKinds() {
		p, ok := sqldb.ProductByName(kind)
		if !ok {
			t.Fatalf("Worker Type %q is not a sqldb product", kind)
		}
		re := regexp.MustCompile(`id: "` + regexp.QuoteMeta(kind) + `", name: "[^"]*", kind: "[^"]*",\s*\n\s*desc: sqlWorkerTypeDesc\("[^"]*", "([A-Z0-9_]+)", "([^"]+)"`)
		m := re.FindStringSubmatch(src)
		if m == nil {
			t.Errorf("the Worker catalog entry for %q does not build its description from the product's name, environment prefix and placeholder", kind)
			continue
		}
		if want := strings.TrimSuffix(p.EnvPrefix(), "_"); "ATLAS_"+m[1] != want {
			t.Errorf("the %s card names ATLAS_%s_MOCK, but a %s worker reads %s_MOCK", kind, m[1], kind, want)
		}
		if m[2] != p.Placeholder {
			t.Errorf("the %s card says values bind with %q, but a %s statement binds with %q", kind, m[2], kind, p.Placeholder)
		}
	}
}

// The "New worker" form's hint is not mail-only.
//
// connectorShape writes a hint for Active Directory and for the three databases as well
// as for mail, and the form wrote every one of them into an element it had just hidden
// with the mail-only rule — so the sentence saying that a database's whole connection
// string is the credential, and that Atlas supervises its worker, was written for each
// SQL kind and shown for none. The edit dialog always showed it, which is exactly the
// disagreement ADR-0160 put the shape in one place to prevent.
func TestTheNewConnectorFormShowsTheHintForEveryKindThatHasOne(t *testing.T) {
	src := webSource(t, "app.js")
	i := strings.Index(src, "conn-hint")
	if i < 0 {
		t.Fatal("the New worker form has no hint element; the pattern must have changed")
	}
	// The element's class list, back to the opening quote.
	open := strings.LastIndex(src[:i], `class="`)
	if open < 0 {
		t.Fatal("could not read the hint element's classes")
	}
	classes := src[open+len(`class="`) : i+len("conn-hint")]
	if strings.Contains(classes, "mail-only") {
		t.Errorf("the New worker form's hint carries the mail-only class (%q), so the hint connectorShape writes for "+
			"Active Directory and the three database kinds is written into a hidden element", classes)
	}
}
