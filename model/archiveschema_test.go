package model

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// These structs have a second encoding nobody declared, and it is the one an archive is read
// through.
//
// The binary codec is positional and guarded: fields are appended, never reordered, and
// TestAppendRecordIsAppendOnly enforces it. But `opensearch/exporter.go` marshals the same
// values with `encoding/json` and no tags — deliberately, "a search/archival projection, not a
// curated per-value-type schema" (ADR-0114) — so every **Go identifier below is an index
// column name**, and those columns outlive the process that wrote them. Atlas already queries
// eight of them back by name (`api/instancearchive.go`, `api/panoramacontext.go`,
// ADR-0247), and a retained export is read years after the code that produced it.
//
// A rename therefore has consequences no compiler sees: documents written before it keep the
// old column, documents after it get the new one, and any query picks exactly one of the two.
// That is not an argument against renaming — it is an argument for noticing. This test is the
// noticing: the wire schema is written down here, and changing it means changing this table,
// in a diff a reviewer can see. The reasoning is recorded in
// ADR-0409.
//
// What this is not: a reason to add 122 `json` tags. Tags would pin each name against the Go
// identifier and would also overturn ADR-0114's explicit choice of an uncurated projection,
// for the same protection this table gives at one place instead of nineteen.
// archiveColumns reads the recorded schema. It lives in testdata rather than in a Go literal
// for one reason: a repo-wide rename over `*.go` would rewrite a Go literal along with the
// fields it is meant to guard, and the guard would pass. A data file cannot be renamed by
// accident, so the test still fails and the change has to be made deliberately, here.
func archiveColumns(t *testing.T) map[string][]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "archive-columns.txt"))
	if err != nil {
		t.Fatalf("read the recorded column names: %v", err)
	}
	out := map[string][]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, cols, ok := strings.Cut(line, ":")
		if !ok {
			t.Fatalf("malformed line in archive-columns.txt: %q", line)
		}
		var names []string
		for _, c := range strings.Split(cols, ",") {
			if c = strings.TrimSpace(c); c != "" {
				names = append(names, c)
			}
		}
		out[strings.TrimSpace(name)] = names
	}
	if len(out) == 0 {
		t.Fatal("archive-columns.txt records no types")
	}
	return out
}

// exportedValues is one value of every type the exporter can write. The completeness test
// below keeps it in step with the package rather than trusting it.
func exportedValues() []Value {
	return []Value{
		&CompensableValue{}, &DataObjectValue{}, &DecisionEvaluationValue{}, &ElementInstanceValue{},
		&InboundDeliveryValue{}, &IncidentValue{}, &JobValue{}, &MessageFlowValue{},
		&MessageSubscriptionValue{}, &OperatorActionValue{}, &ProcessInstanceValue{},
		&ProcessMigrationValue{}, &SignalSubscriptionValue{}, &TimerValue{},
		&VariableAuditValue{}, &VariableIndexValue{}, &VariableValue{},
	}
}

// TestTheExportedColumnNamesAreWhatTheTableSays marshals every value the way the exporter does
// and compares the keys against the recorded schema.
func TestTheExportedColumnNamesAreWhatTheTableSays(t *testing.T) {
	recorded := archiveColumns(t)
	for _, v := range exportedValues() {
		name := fmt.Sprintf("%T", v)
		want, ok := recorded[name]
		if !ok {
			t.Errorf("%s exports columns that nothing records; add it to archiveColumns", name)
			continue
		}
		got := jsonKeysOf(t, v)
		if strings.Join(got, ",") == strings.Join(want, ",") {
			continue
		}
		added, removed := diffNames(want, got)
		t.Errorf("%s: the exported column names changed.\n  added:   %v\n  removed: %v\n"+
			"Documents already in the index keep the old names, so a query finds one set or the "+
			"other, never both. If the change is intended, record it here.",
			name, added, removed)
	}
}

// TestEveryEncodableValueHasRecordedColumns is the completeness half: a value type that the
// log can carry but this table does not know is a set of column names nothing is watching.
// The check is over the source rather than over the table, because that is the direction in
// which the package actually grows.
func TestEveryEncodableValueHasRecordedColumns(t *testing.T) {
	src, err := os.ReadFile("value.go")
	if err != nil {
		t.Fatalf("read value.go: %v", err)
	}
	encoders := regexp.MustCompile(`(?m)^func \(v \*(\w+)\) encode\(`).FindAllStringSubmatch(string(src), -1)
	if len(encoders) == 0 {
		t.Fatal("found no encode methods in value.go; the scan no longer matches the source")
	}
	covered := map[string]bool{}
	for _, v := range exportedValues() {
		covered[strings.TrimPrefix(fmt.Sprintf("%T", v), "*model.")] = true
	}
	for _, m := range encoders {
		if !covered[m[1]] {
			t.Errorf("%s is written to the log and therefore to the export, but exportedValues() "+
				"does not include it, so its column names are unrecorded.", m[1])
		}
	}
}

func jsonKeysOf(t *testing.T, v Value) []string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal %T: %v", v, err)
	}
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// diffNames reports what got has that want does not, and the reverse. A rename shows up as
// one of each, which is the case worth seeing side by side.
func diffNames(want, got []string) (added, removed []string) {
	in := func(s []string, k string) bool {
		for _, x := range s {
			if x == k {
				return true
			}
		}
		return false
	}
	for _, k := range got {
		if !in(want, k) {
			added = append(added, k)
		}
	}
	for _, k := range want {
		if !in(got, k) {
			removed = append(removed, k)
		}
	}
	return added, removed
}
