package openapimock

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gen builds a generator over a document, for the schema cases below.
func gen(doc map[string]any) *generator {
	if doc == nil {
		doc = map[string]any{}
	}
	return &generator{doc: doc, active: map[string]bool{}}
}

// generated renders what a schema produces, as JSON, so a case reads as the response
// a caller would see.
func generated(t *testing.T, g *generator, schema any) string {
	t.Helper()
	value, err := g.value(schema, 0)
	if err != nil {
		t.Fatalf("value: %v", err)
	}
	out, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(out)
}

func TestGeneratedValues(t *testing.T) {
	cases := map[string]struct {
		schema any
		want   string
	}{
		"a string":                           {map[string]any{"type": "string"}, `"string"`},
		"a date-time":                        {map[string]any{"type": "string", "format": "date-time"}, `"1970-01-01T00:00:00Z"`},
		"an unknown format":                  {map[string]any{"type": "string", "format": "morse"}, `"string"`},
		"a short string grows":               {map[string]any{"type": "string", "minLength": 8}, `"stringxx"`},
		"a long string is cut":               {map[string]any{"type": "string", "maxLength": 3}, `"str"`},
		"an integer":                         {map[string]any{"type": "integer"}, `0`},
		"an integer above a minimum":         {map[string]any{"type": "integer", "minimum": 5}, `5`},
		"an exclusive minimum, 3.0 spelling": {map[string]any{"type": "integer", "minimum": 5, "exclusiveMinimum": true}, `6`},
		"an exclusive minimum, 3.1 spelling": {map[string]any{"type": "integer", "exclusiveMinimum": 5}, `6`},
		"a negative maximum":                 {map[string]any{"type": "integer", "maximum": -4}, `-4`},
		"a number":                           {map[string]any{"type": "number"}, `0`},
		"a boolean":                          {map[string]any{"type": "boolean"}, `false`},
		"an explicit null":                   {map[string]any{"type": "null"}, `null`},
		"a nullable 3.1 type":                {map[string]any{"type": []any{"null", "integer"}}, `0`},
		"only null":                          {map[string]any{"type": []any{"null"}}, `null`},
		"a const wins":                       {map[string]any{"type": "string", "const": "fixed"}, `"fixed"`},
		"an enum picks the first":            {map[string]any{"enum": []any{"a", "b"}}, `"a"`},
		"an array":                           {map[string]any{"type": "array", "items": map[string]any{"type": "boolean"}}, `[false]`},
		"an array without items":             {map[string]any{"type": "array"}, `[]`},
		"minItems is honoured":               {map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "minItems": 3}, `[0,0,0]`},
		"minItems is capped":                 {map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "minItems": 99}, `[0,0,0,0,0]`},
		"an implied array":                   {map[string]any{"items": map[string]any{"type": "string"}}, `["string"]`},
		"an implied object":                  {map[string]any{"properties": map[string]any{"a": map[string]any{"type": "string"}}}, `{"a":"string"}`},
		"a free-form object":                 {map[string]any{"type": "object"}, `{}`},
		"oneOf takes the first":              {map[string]any{"oneOf": []any{map[string]any{"type": "boolean"}, map[string]any{"type": "string"}}}, `false`},
		"anyOf takes the first":              {map[string]any{"anyOf": []any{map[string]any{"type": "integer"}}}, `0`},
		"allOf of non-objects takes one":     {map[string]any{"allOf": []any{map[string]any{"type": "string"}}}, `"string"`},
		"an empty allOf falls through":       {map[string]any{"allOf": []any{}, "type": "boolean"}, `false`},
		"a schema of true":                   {true, `null`},
		"no type at all":                     {map[string]any{}, `null`},
		"an unknown type":                    {map[string]any{"type": "widget"}, `null`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := generated(t, gen(nil), tc.schema); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestGeneratedValuesStopAtTheDepthLimit(t *testing.T) {
	// A schema nested deeper than any response needs is cut off rather than followed:
	// the document is somebody else's, and it may be pathological.
	schema := map[string]any{"type": "string"}
	for range maxDepth + 2 {
		schema = map[string]any{"type": "object", "properties": map[string]any{"next": schema}}
	}
	got := generated(t, gen(nil), schema)
	if want := `{"next":`; got[:len(want)] != want {
		t.Fatalf("got %s", got)
	}
	if len(got) > 200 {
		t.Errorf("the depth limit did not stop the walk: %s", got)
	}
}

func TestResolveFollowsAndRefusesPointers(t *testing.T) {
	doc := map[string]any{
		"components": map[string]any{
			"schemas": map[string]any{
				"Pet":       map[string]any{"type": "string"},
				"odd/name":  map[string]any{"type": "boolean"},
				"not-a-map": "scalar",
			},
		},
	}
	g := gen(doc)
	if got := generated(t, g, map[string]any{"$ref": "#/components/schemas/Pet"}); got != `"string"` {
		t.Errorf("resolved ref = %s", got)
	}
	// A pointer escapes "/" as ~1, so a component name may contain one.
	if got := generated(t, g, map[string]any{"$ref": "#/components/schemas/odd~1name"}); got != "false" {
		t.Errorf("escaped ref = %s", got)
	}
	for name, ref := range map[string]string{
		"remote":           "https://example.com/spec.yaml#/Pet",
		"missing":          "#/components/schemas/Nope",
		"through a scalar": "#/components/schemas/not-a-map/deeper",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := g.value(map[string]any{"$ref": ref}, 0); err == nil {
				t.Errorf("want an error for %q", ref)
			}
		})
	}
}

func TestDerefLeavesEverythingElseAlone(t *testing.T) {
	g := gen(map[string]any{"x": map[string]any{"value": 1}})
	for name, node := range map[string]any{
		"a scalar":       "text",
		"a plain object": map[string]any{"value": 1},
	} {
		t.Run(name, func(t *testing.T) {
			got, leave, err := g.deref(node)
			if err != nil {
				t.Fatalf("deref: %v", err)
			}
			defer leave()
			if got == nil {
				t.Errorf("deref returned nothing")
			}
		})
	}
	if _, _, err := g.deref(map[string]any{"$ref": "nowhere"}); err == nil {
		t.Error("want an error for an unresolvable ref")
	}
}

func TestNumberReadsEveryDecodersShape(t *testing.T) {
	cases := map[string]struct {
		value any
		want  float64
		ok    bool
	}{
		"yaml int":     {7, 7, true},
		"int64":        {int64(7), 7, true},
		"json float":   {7.0, 7, true},
		"json.Number":  {json.Number("7"), 7, true},
		"not a number": {json.Number("seven"), 0, false},
		"a string":     {"7", 0, false},
		"absent":       {nil, 0, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, ok := number(tc.value)
			if got != tc.want || ok != tc.ok {
				t.Errorf("number(%v) = %v %v, want %v %v", tc.value, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestATreeOfFilesIsBounded(t *testing.T) {
	// The bound is a backstop against a generated or symlinked tree. It is exercised
	// against a small limit rather than by writing ten thousand files: the constant's
	// value is a judgement call, the refusal is the behaviour.
	dir := t.TempDir()
	for _, name := range []string{"a.yml", "b.yml"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("Thing: {type: string}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files := &documents{root: dir, byPath: map[string]map[string]any{}, limit: 1}
	if _, err := files.load(filepath.Join(dir, "a.yml"), "a.yml#/Thing"); err != nil {
		t.Fatalf("the first file: %v", err)
	}
	// The same file again is the copy in hand, not a second read against the bound.
	if _, err := files.load(filepath.Join(dir, "a.yml"), "a.yml#/Thing"); err != nil {
		t.Fatalf("the same file again: %v", err)
	}
	_, err := files.load(filepath.Join(dir, "b.yml"), "b.yml#/Thing")
	if err == nil || !strings.Contains(err.Error(), "as far as this mock will follow") {
		t.Errorf("err = %v, want the bound to refuse the second file", err)
	}
}
