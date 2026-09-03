package webscrape

import (
	"os"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// migrosExamplePath is the shipped structured-HTML example. examples/models_test.go
// proves it compiles; this test proves its selectors do what its documentation says
// — against a page shaped like the real one.
const migrosExamplePath = "../../examples/hypothekarzinsen-migrosbank.bpmn"

// ratesPageFixture mirrors the structure of migrosbank.ch's rate page as it actually
// is: several tables with the *same* class, distinguishable only by the product their
// header names, rows carrying Table--row, cells Table--bodyCell. That sameness is the
// whole difficulty — a selector that takes "the first table" takes the wrong rates the
// day the page gains a section, and here it would take two products' rows at once.
const ratesPageFixture = `<!DOCTYPE html>
<html><body>
  <section>
    <h2>Hypothekarzinsen Online-Hypothek</h2>
    <table class="Table table table-hover">
      <thead><tr><th>Laufzeit</th><th>Vorzugszinssatz Online-Hypothek*</th><th>Standardzinssatz</th></tr></thead>
      <tbody>
        <tr class="Table--row"><td class="Table--bodyCell">2 Jahre</td><td class="Table--bodyCell">1.130 %</td><td class="Table--bodyCell">1.430 %</td></tr>
        <tr class="Table--row"><td class="Table--bodyCell">3 Jahre</td><td class="Table--bodyCell">1.240 %</td><td class="Table--bodyCell">1.540 %</td></tr>
        <tr class="Table--row"><td class="Table--bodyCell">10 Jahre</td><td class="Table--bodyCell">1.710 %</td><td class="Table--bodyCell">2.010 %</td></tr>
      </tbody>
    </table>
  </section>
  <section>
    <h2>Hypothekarzinsen Festhypothek</h2>
    <table class="Table table table-hover">
      <thead><tr><th>Laufzeit</th><th>Vorzugszinssatz Eco-Vergünstigung*</th><th>Standardzinssatz</th></tr></thead>
      <tbody>
        <tr class="Table--row"><td class="Table--bodyCell">2 Jahre</td><td class="Table--bodyCell">1.280 %</td><td class="Table--bodyCell">1.430 %</td></tr>
        <tr class="Table--row"><td class="Table--bodyCell">3 Jahre</td><td class="Table--bodyCell">1.390 %</td><td class="Table--bodyCell">1.540 %</td></tr>
      </tbody>
    </table>
  </section>
  <section>
    <h2>Hypothekarzins SARON-Hypothek</h2>
    <table class="Table table table-hover">
      <thead><tr><th>Angebot</th><th>Zinssatz*</th></tr></thead>
      <tbody>
        <tr class="Table--row"><td class="Table--bodyCell">Selbst bewohntes Wohneigentum</td><td class="Table--bodyCell">2,750%</td></tr>
      </tbody>
    </table>
  </section>
</body></html>`

// migrosScrapeRequest reads the request out of the shipped model rather than
// restating it: what is under test is the model's own selector and fields.
func migrosScrapeRequest(t *testing.T) Request {
	t.Helper()
	f, err := os.Open(migrosExamplePath)
	if err != nil {
		t.Fatalf("open %s: %v", migrosExamplePath, err)
	}
	defer f.Close()
	cp, err := compiler.Parse(1, 1, f)
	if err != nil {
		t.Fatalf("compile %s: %v", migrosExamplePath, err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	detail := cp.ConnectorTask(cp.Node(task).Detail)
	if detail == nil {
		t.Fatal("the first task after the timer start is not a worker task")
	}
	fields := make([]Field, 0, len(detail.ScrapeFields))
	for _, f := range detail.ScrapeFields {
		fields = append(fields, Field{
			Name:      cp.Intern(f.Name),
			Selector:  cp.Intern(f.Selector),
			Attribute: cp.Intern(f.Attribute),
		})
	}
	return Request{
		URL:      detail.Url.Literal,
		Selector: detail.ScrapeSelector.Literal,
		Fields:   fields,
		MaxItems: detail.ScrapeMaxItems,
	}
}

// The model's own claim: one object per rate row, from the Online-Hypothek table and
// no other. Six tables share a class on that page, so this is the assertion that
// would fail if the selector were loosened to "the first table" or "every row".
func TestMigrosExampleReadsOnlyTheOnlineHypothekRows(t *testing.T) {
	req := migrosScrapeRequest(t)
	if req.URL != "https://www.migrosbank.ch/de/privatpersonen/hypotheken/aktuelle-hypothekarzinsen.html" {
		t.Errorf("url = %q, want the authored rate page", req.URL)
	}
	if len(req.Fields) != 3 {
		t.Fatalf("fields = %+v, want laufzeit, vorzugszins and standardzins", req.Fields)
	}

	doc, err := parseHTML(strings.NewReader(ratesPageFixture), "text/html")
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	got, err := extractRecords(doc, req, nil)
	if err != nil {
		t.Fatalf("extractRecords: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("rows = %d (%v), want the three Online-Hypothek rows and nothing from the other tables", len(got), got)
	}
	want := []map[string]string{
		{"laufzeit": "2 Jahre", "vorzugszins": "1.130 %", "standardzins": "1.430 %"},
		{"laufzeit": "3 Jahre", "vorzugszins": "1.240 %", "standardzins": "1.540 %"},
		{"laufzeit": "10 Jahre", "vorzugszins": "1.710 %", "standardzins": "2.010 %"},
	}
	for i, w := range want {
		for key, value := range w {
			if got[i][key] != value {
				t.Errorf("row %d %s = %q, want %q (whole row: %v)", i, key, got[i][key], value, got[i])
			}
		}
	}
	// The Eco table repeats "2 Jahre" with a different rate. Taking both would put two
	// contradictory rows for the same term into the sheet, and nothing downstream could
	// tell which product each belonged to.
	for _, row := range got {
		if row["vorzugszins"] == "1.280 %" {
			t.Errorf("the Eco-Vergünstigung table leaked into the result: %v", row)
		}
	}
}
