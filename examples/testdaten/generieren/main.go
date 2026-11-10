// Command generieren writes the test data for the quality-check examples: n made-up
// employees as an upload file, a SQL Server mock seed, an LDAP LDIF and an AD mock
// seed (examples/testdaten). The output is the same on every run for the same n.
//
//	go run ./examples/testdaten/generieren -n 50000 -out /tmp/testdaten
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pblumer/atlas/examples/testdaten"
)

func main() {
	n := flag.Int("n", 50000, "how many employees to generate")
	out := flag.String("out", ".", "the directory the four files are written to")
	flag.Parse()
	if err := run(*n, *out); err != nil {
		fmt.Fprintln(os.Stderr, "generieren:", err)
		os.Exit(1)
	}
}

func run(n int, dir string) error {
	if n < 1 {
		return fmt.Errorf("-n must be at least 1, not %d", n)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	people := testdaten.Population(n)
	files := []struct {
		name, what string
		data       []byte
	}{
		{"mitarbeitende.csv", "upload for Mitarbeitende prüfen (Datei)", testdaten.CSV(people)},
		{"mssql-seed.json", "seed for the MS SQL mock, Mitarbeitende prüfen (SQL Server)", testdaten.MSSQLSeed(people)},
		{"personen.ldif", "for an LDAP server, LDAP-Personen prüfen", testdaten.PeopleLDIF(people)},
		{"benutzer.ldif", "seed for the AD mock, AD-Benutzer prüfen", testdaten.AccountsLDIF(people)},
	}
	for _, f := range files {
		path := filepath.Join(dir, f.name)
		if err := os.WriteFile(path, f.data, 0o644); err != nil {
			return err
		}
		fmt.Printf("%-60s %9d bytes  %s\n", path, len(f.data), f.what)
	}
	bad := testdaten.Defective(n)
	fmt.Printf("\n%d records per source: %d in order, %d with a finding.\n", n, n-bad, bad)
	return nil
}
