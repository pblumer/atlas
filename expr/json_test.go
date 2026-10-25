package expr_test

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/expr"
)

// TestClassifyStructured checks that objects and lists reduce to KindJSON with a
// canonical, deterministic JSON encoding (object keys sorted), so replay of the
// stored text is stable.
func TestClassifyStructured(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want string
	}{
		{"empty-list", "[]", "[]"},
		{"number-list", "[1, 2, 3]", "[1,2,3]"},
		{"mixed-list", `[1, "a", true, null]`, `[1,"a",true,null]`},
		{"context", `{b: 2, a: 1}`, `{"a":1,"b":2}`},
		{"nested", `{items: [1, 2], who: {name: "acme", n: 3}}`, `{"items":[1,2],"who":{"n":3,"name":"acme"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := expr.Compile(tc.src)
			if err != nil {
				t.Fatalf("Compile(%q): %v", tc.src, err)
			}
			v, err := c.Eval(nil)
			if err != nil {
				t.Fatalf("Eval(%q): %v", tc.src, err)
			}
			kind, b, text := expr.Classify(v)
			if kind != expr.KindJSON || b {
				t.Fatalf("Classify kind = (%d,%v), want (KindJSON,false)", kind, b)
			}
			if text != tc.want {
				t.Errorf("Classify text = %q, want %q", text, tc.want)
			}
		})
	}
}

// TestStructuredRoundTrip proves Classify and FromStored are inverses for
// structured values: a context/list survives storage and rebinds so its members
// remain FEEL-accessible.
func TestStructuredRoundTrip(t *testing.T) {
	c, err := expr.Compile(`{amount: 10, tags: ["x", "y"], nested: {ok: true}}`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	orig, err := c.Eval(nil)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	kind, b, text := expr.Classify(orig)
	got := expr.FromStored(kind, b, text)

	// Re-classify the reconstructed value: canonical text must match, proving the
	// structure (and its member kinds) survived the round trip intact.
	_, _, gotText := expr.Classify(got)
	if gotText != text {
		t.Errorf("round trip: %q -> %q", text, gotText)
	}

	// A downstream expression can read a member of the reconstructed value.
	read, err := expr.Compile("c.amount + 5", "c")
	if err != nil {
		t.Fatalf("Compile reader: %v", err)
	}
	res, err := read.Eval(map[string]expr.Value{"c": got})
	if err != nil {
		t.Fatalf("Eval reader: %v", err)
	}
	if k, _, txt := expr.Classify(res); k != expr.KindNumber || txt != "15" {
		t.Errorf("c.amount + 5 = (%d,%q), want (number,15)", k, txt)
	}
}

// TestFromJSON builds FEEL values from decoded JSON (the shape the API hands in
// from a start-variable request), covering each JSON type and nesting.
func TestFromJSON(t *testing.T) {
	var in any
	dec := json.NewDecoder(strings.NewReader(`{"n": 3.5, "s": "hi", "b": false, "z": null, "list": [1, {"deep": 2}]}`))
	dec.UseNumber()
	if err := dec.Decode(&in); err != nil {
		t.Fatalf("decode: %v", err)
	}
	v := expr.FromJSON(in)
	// Read a deeply nested member through FEEL to confirm the whole tree is real.
	c, err := expr.Compile("root.list[2].deep", "root")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	res, err := c.Eval(map[string]expr.Value{"root": v})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if k, _, txt := expr.Classify(res); k != expr.KindNumber || txt != "2" {
		t.Errorf("root.list[2].deep = (%d,%q), want (number,2)", k, txt)
	}
}

// TestFromStoredBadJSON documents that unparseable stored JSON degrades to FEEL
// null rather than panicking — the same defensive posture as a bad number.
func TestFromStoredBadJSON(t *testing.T) {
	if got := expr.FromStored(expr.KindJSON, false, "{not json").String(); got != "null" {
		t.Errorf("FromStored(json, bad) = %q, want null", got)
	}
}

func TestFromJSONFloat64(t *testing.T) {
	got := expr.FromJSON(float64(42.5))
	k, _, txt := expr.Classify(got)
	if k != expr.KindNumber || txt != "42.5" {
		t.Errorf("FromJSON(float64) = (%d,%q), want (number,42.5)", k, txt)
	}
}

func TestFromJSONBadNumber(t *testing.T) {
	got := expr.FromJSON(json.Number("not-a-number"))
	if got.String() != "null" {
		t.Errorf("FromJSON(bad json.Number) = %q, want null", got.String())
	}
}

func TestFromJSONBadFloat64(t *testing.T) {
	got := expr.FromJSON(math.NaN())
	if got.String() != "null" {
		t.Errorf("FromJSON(NaN) = %q, want null", got.String())
	}
}

func TestFromJSONUnknownType(t *testing.T) {
	got := expr.FromJSON(struct{}{})
	if got.String() != "null" {
		t.Errorf("FromJSON(struct{}) = %q, want null", got.String())
	}
}

func TestValueToGoTemporal(t *testing.T) {
	c, err := expr.Compile(`date("2024-01-15")`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	v, err := c.Eval(nil)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	text, ok := expr.ToJSON(v)
	if !ok {
		t.Fatalf("ToJSON returned false for temporal")
	}
	if text == "" {
		t.Error("ToJSON returned empty for temporal")
	}
}

func TestClassifyListContainingTemporal(t *testing.T) {
	c, err := expr.Compile(`[date("2024-01-15"), 1]`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	v, err := c.Eval(nil)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	k, _, text := expr.Classify(v)
	if k != expr.KindJSON {
		t.Fatalf("kind = %d, want KindJSON", k)
	}
	if text == "" {
		t.Error("text is empty")
	}
}

// A context assembled from values already built — an instant among them, which has no
// JSON image to come from — reads its members back by name.
func TestAContextOfBuiltValuesReadsBack(t *testing.T) {
	at := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	ctx := expr.ContextOf(map[string]expr.Value{"time": expr.DateTime(at), "who": expr.String("alice")})
	c, err := expr.Compile(`context.who = "alice" and context.time = date and time("2026-10-23T09:00:00Z")`, "context")
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.Eval(map[string]expr.Value{"context": ctx})
	if err != nil {
		t.Fatal(err)
	}
	if !expr.IsTrue(v) {
		t.Fatalf("result = %v", v)
	}
}

// TestContextMemberAndKeys pins the two readers the template language uses on a loop
// item: a context answers its members and its keys in order, anything else answers
// that it is not a context.
func TestContextMemberAndKeys(t *testing.T) {
	ctx := expr.FromJSON(map[string]any{"name": "Ada", "age": json.Number("36")})
	v, ok := expr.ContextMember(ctx, "name")
	if !ok || v.String() != "Ada" {
		t.Fatalf("expr.ContextMember(name) = %v, %v; want Ada, true", v, ok)
	}
	if _, ok := expr.ContextMember(ctx, "missing"); ok {
		t.Error("expr.ContextMember(missing) reported a member")
	}
	if _, ok := expr.ContextMember(expr.String("x"), "name"); ok {
		t.Error("ContextMember on a string reported a member")
	}
	keys, ok := expr.ContextKeys(ctx)
	if !ok || len(keys) != 2 || keys[0] != "age" || keys[1] != "name" {
		t.Fatalf("ContextKeys = %v, %v; want [age name], true", keys, ok)
	}
	if _, ok := expr.ContextKeys(expr.ListOf(expr.Number(1))); ok {
		t.Error("ContextKeys on a list reported a context")
	}
}
