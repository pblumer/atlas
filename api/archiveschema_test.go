package api

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/pblumer/atlas/model"
)

// The exported event log is a schema the moment something queries it by name, and something
// already does.
//
// `opensearch/exporter.go` marshals a record's value as `any` with no `json` tags — a
// deliberate choice (ADR-0114: "a search/archival projection, not a curated per-value-type
// schema"). The consequence is that every field name in the index is a **Go identifier**, and
// this server sends those identifiers back as query paths: `value.Name.keyword`,
// `value.ProcessDefKey` and six more, in `instancearchive.go` and `panoramacontext.go`.
//
// Renaming such a field in Go compiles, keeps every existing test green — the query builder
// still emits the same literal string — and breaks the feature in production: new documents
// are indexed under the new name, the query still asks for the old one, and ADR-0247's own
// words describe the result, an empty list "indistinguishable from *no such instance ever
// existed*". Documents already written keep the old name and become unreachable by the new
// query.
//
// The envelope is already guarded this way: `document`'s fields carry `json` tags and a test
// pins them. The payload had neither. These two tests close that gap from both ends — every
// path names a field that really exists, and every path in the source is in the table. The
// reasoning, and why the column names are recorded rather than tagged, is in ADR-0409.

// archiveQueryPath is one field path this server sends to the exported event log, with a
// value of the type whose documents it selects.
type archiveQueryPath struct {
	path  string      // as written in the query, including any .keyword suffix
	value model.Value // a value of the type those documents carry
}

var archiveQueryPaths = []archiveQueryPath{
	{"value.Name.keyword", &model.VariableValue{}},
	{"value.Text.keyword", &model.VariableValue{}},
	{"value.ScopeKey", &model.VariableValue{}},
	{"value.ProcessDefKey", &model.ProcessInstanceValue{}},
	{"value.State", &model.ProcessInstanceValue{}},
	{"value.CreatedAt", &model.ProcessInstanceValue{}},
	{"value.CompletedAt", &model.ProcessInstanceValue{}},
	{"value.CorrelationKey", &model.ProcessInstanceValue{}},
}

// TestEveryArchiveQueryPathNamesAFieldThatIsExported marshals each value exactly as the
// exporter does and insists the queried key is in the result. A rename then fails here rather
// than in an operator's empty search result.
func TestEveryArchiveQueryPathNamesAFieldThatIsExported(t *testing.T) {
	for _, q := range archiveQueryPaths {
		// The exporter marshals the value into the document's `value` field, so what lands
		// in the index is exactly this object's keys.
		raw, err := json.Marshal(q.value)
		if err != nil {
			t.Fatalf("%s: marshal %T: %v", q.path, q.value, err)
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: unmarshal %T: %v", q.path, q.value, err)
		}
		field := fieldOfQueryPath(q.path)
		if _, ok := doc[field]; !ok {
			have := make([]string, 0, len(doc))
			for k := range doc {
				have = append(have, k)
			}
			sort.Strings(have)
			t.Errorf("query path %q asks for %T's field %q, which it no longer exports.\n"+
				"Exported keys are the Go field names: %s.\n"+
				"A rename here renames an index column: documents already written keep the old "+
				"name and the query matches nothing.",
				q.path, q.value, field, strings.Join(have, ", "))
		}
	}
}

// queryPathLiteral finds the `"value.Something"` strings the query builders embed.
var queryPathLiteral = regexp.MustCompile(`"value\.[A-Za-z][A-Za-z0-9.]*"`)

// TestTheArchiveQueryPathTableIsComplete keeps the table above from going stale. A new query
// path that nobody adds here is a field nothing checks, which is the state this whole file
// exists to end — so the scan is over the source rather than over the table.
func TestTheArchiveQueryPathTableIsComplete(t *testing.T) {
	known := map[string]bool{}
	for _, q := range archiveQueryPaths {
		known[q.path] = true
	}
	for _, file := range []string{"instancearchive.go", "panoramacontext.go"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, lit := range queryPathLiteral.FindAllString(string(src), -1) {
			path := strings.Trim(lit, `"`)
			if !known[path] {
				t.Errorf("%s queries %q, which is not in archiveQueryPaths.\n"+
					"Add it with the value type whose documents it selects, so a rename of "+
					"that Go field fails a test instead of an operator's search.", file, path)
			}
		}
	}
}

// fieldOfQueryPath strips the document's `value.` prefix and any `.keyword` sub-field, both
// of which belong to the index rather than to the Go struct.
func fieldOfQueryPath(path string) string {
	field := strings.TrimPrefix(path, "value.")
	return strings.TrimSuffix(field, ".keyword")
}
