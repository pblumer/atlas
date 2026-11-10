package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The command writes the four files, and refuses a population of nobody.
func TestRunWritesTheFourFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	if err := run(10, dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mitarbeitende.csv", "mssql-seed.json", "personen.ldif", "benutzer.ldif"} {
		if st, err := os.Stat(filepath.Join(dir, name)); err != nil || st.Size() == 0 {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := run(0, dir); err == nil {
		t.Error("n = 0 was accepted")
	}
	if err := run(1, filepath.Join(dir, "mitarbeitende.csv", "x")); err == nil {
		t.Error("an output directory under a file was accepted")
	}
}
