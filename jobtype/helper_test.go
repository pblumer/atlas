package jobtype_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/jobtype"
)

// indexInFile reads the index out of one persisted registry record, so a test can
// target a specific entry's file without depending on the filename scheme.
func indexInFile(path string) (int32, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var rec struct {
		Index int32 `json:"index"`
	}
	if err := json.Unmarshal(body, &rec); err != nil {
		return 0, err
	}
	return rec.Index, nil
}

// writeRecord plants one registry record on disk the way the store would, so a
// test can set up a table state the API would not produce.
func writeRecord(t *testing.T, dir string, e jobtype.Entry) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := filepath.Join(dir, hex.EncodeToString([]byte(e.Name))+".json")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
