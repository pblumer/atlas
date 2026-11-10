package testdaten_test

import (
	"bytes"
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/ldif"
	"github.com/pblumer/atlas/connector/sqldb"
	"github.com/pblumer/atlas/examples/testdaten"
)

// The same n gives the same bytes, and six in every hundred carry a finding.
func TestTheDataIsDeterministic(t *testing.T) {
	a, b := testdaten.Population(500), testdaten.Population(500)
	if !bytes.Equal(testdaten.CSV(a), testdaten.CSV(b)) || !bytes.Equal(testdaten.AccountsLDIF(a), testdaten.AccountsLDIF(b)) {
		t.Fatal("two runs for the same n differ")
	}
	for n, want := range map[int]int{1: 0, 100: 6, 200: 12, 1000: 60, 50000: 3000} {
		if got := testdaten.Defective(n); got != want {
			t.Errorf("Defective(%d) = %d, want %d", n, got, want)
		}
	}
	seen := map[string]bool{}
	for _, p := range testdaten.Population(5000) {
		if seen[p.Account] {
			t.Fatalf("account %q twice", p.Account)
		}
		seen[p.Account] = true
		if len(p.Account) > 20 {
			t.Errorf("account %q is longer than a sAMAccountName may be", p.Account)
		}
	}
}

// Each format is what its reader reads: the file is CSV with the columns the decision
// names, the seed is a mock seed answering the example's statement, and both LDIF files
// parse with the directory-file reader the AD mock uses.
func TestEachFormatIsReadable(t *testing.T) {
	people := testdaten.Population(300)
	rows, err := csv.NewReader(bytes.NewReader(testdaten.CSV(people))).ReadAll()
	if err != nil || len(rows) != 301 || strings.Join(rows[0], ",") != strings.Join(testdaten.Columns, ",") {
		t.Fatalf("csv = %d rows (%v), header %v", len(rows), err, rows[0])
	}
	answers, err := sqldb.ParseMockSeed(testdaten.MSSQLSeed(people))
	if err != nil || len(answers) != 1 || len(answers[0].Rows) != 300 || answers[0].Statement != testdaten.Statement {
		t.Fatalf("seed = %d answers (%v)", len(answers), err)
	}
	for name, data := range map[string][]byte{"people": testdaten.PeopleLDIF(people), "accounts": testdaten.AccountsLDIF(people)} {
		entries, err := ldif.Parse(ldif.FormatLDIF, data)
		if err != nil || len(entries) != 301 {
			t.Fatalf("%s ldif = %d entries (%v)", name, len(entries), err)
		}
	}
}

// The samples the examples ship are the generator's output for 200 people, so the
// numbers their READMEs name are the generator's too. `go test ./examples/testdaten
// -args -update` rewrites them after a change to the generator.
func TestTheShippedSamplesAreTheGenerators(t *testing.T) {
	people := testdaten.Population(200)
	for path, want := range map[string][]byte{
		"../mitarbeitende-pruefen/mitarbeitende.csv": testdaten.CSV(people),
		"../mitarbeitende-pruefen/mssql-seed.json":   testdaten.MSSQLSeed(people),
		"../ldap-personen-pruefen/personen.ldif":     testdaten.PeopleLDIF(people),
	} {
		got, err := os.ReadFile(filepath.FromSlash(path))
		if err != nil || !bytes.Equal(got, want) {
			if len(os.Args) > 0 && hasUpdate() {
				if err := os.WriteFile(filepath.FromSlash(path), want, 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			t.Errorf("%s is not the generator's output for 200 people (%v); run go test ./examples/testdaten -args -update", path, err)
		}
	}
}

func hasUpdate() bool {
	for _, a := range os.Args {
		if a == "-update" {
			return true
		}
	}
	return false
}
