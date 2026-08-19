package webscrape

import (
	"errors"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

// errReader fails every read, so a parse over it surfaces the read error.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

// A read failure while parsing the page body is surfaced as an error (the job is
// retried), not swallowed as an empty result.
func TestExtractParseError(t *testing.T) {
	_, err := extract(errReader{}, ".x", "")
	if err == nil || !strings.Contains(err.Error(), "parse html") {
		t.Fatalf("extract error = %v, want a parse-html error", err)
	}
}

const sampleHTML = `<!DOCTYPE html>
<html><body>
  <ul class="list">
    <li class="item"><a href="/a">First</a></li>
    <li class="item"><a href="/b">  Second  </a></li>
    <li class="item"><span>No link here</span></li>
  </ul>
</body></html>`

func TestExtractText(t *testing.T) {
	got, err := extract(strings.NewReader(sampleHTML), ".item a", "")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	want := []string{"First", "Second"}
	if len(got) != len(want) {
		t.Fatalf("matches = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] { // text is trimmed
			t.Errorf("match[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestExtractAttribute(t *testing.T) {
	// The attribute mode reads href from each anchor; the third .item has no anchor,
	// so a selector on .item with an href attribute yields only the two links.
	got, err := extract(strings.NewReader(sampleHTML), ".item a", "href")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	want := []string{"/a", "/b"}
	if len(got) != len(want) {
		t.Fatalf("hrefs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("href[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// A match without the requested attribute is skipped, so a mixed selection yields
// only the elements that actually carry it.
func TestExtractAttributeSkipsMissing(t *testing.T) {
	got, err := extract(strings.NewReader(sampleHTML), ".item *", "href")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(got) != 2 { // two anchors carry href; the span does not
		t.Fatalf("hrefs = %v, want the two that carry href", got)
	}
}

// A selector that matches nothing is not an error — it yields an empty, non-nil
// slice so the task writes an empty JSON array rather than null.
func TestExtractNoMatch(t *testing.T) {
	got, err := extract(strings.NewReader(sampleHTML), ".nope", "")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("matches = %v (nil=%t), want a non-nil empty slice", got, got == nil)
	}
}

// An invalid CSS selector is surfaced as an error rather than silently yielding an
// empty result, so a modeling mistake becomes an incident.
func TestExtractInvalidSelector(t *testing.T) {
	_, err := extract(strings.NewReader(sampleHTML), "a[unclosed", "")
	if err == nil || !strings.Contains(err.Error(), "invalid selector") {
		t.Fatalf("extract error = %v, want an invalid-selector error", err)
	}
}

// toExprKind and toVarKind mirror the stored/expr enums both directions across every
// kind (they evolve independently, so each arm is pinned).
func TestKindMappings(t *testing.T) {
	pairs := []struct {
		v model.VarKind
		e expr.ValueKind
	}{
		{model.VarBool, expr.KindBool},
		{model.VarNumber, expr.KindNumber},
		{model.VarString, expr.KindString},
		{model.VarJSON, expr.KindJSON},
		{model.VarNull, expr.KindNull},
	}
	for _, p := range pairs {
		if got := toExprKind(p.v); got != p.e {
			t.Errorf("toExprKind(%v) = %v, want %v", p.v, got, p.e)
		}
		if got := toVarKind(p.e); got != p.v {
			t.Errorf("toVarKind(%v) = %v, want %v", p.e, got, p.v)
		}
	}
}

// bindVars binds only the names an expression reads: a present variable by its stored
// kind, the reserved processInstanceKey to the scope's key, and an absent name not at
// all. An empty name list binds nothing.
func TestBindVars(t *testing.T) {
	scopeVars := map[string]model.VariableValue{
		"count": {Name: "count", Kind: model.VarNumber, Text: "3"},
	}
	m := bindVars(42, scopeVars, []string{"count", builtinProcessInstanceKey, "absent"})
	if len(m) != 2 {
		t.Fatalf("bindVars bound %d names, want 2 (count + processInstanceKey)", len(m))
	}
	if _, ok := m["absent"]; ok {
		t.Error("bindVars bound an absent name, want it left unbound (FEEL null)")
	}
	if _, _, text := expr.Classify(m[builtinProcessInstanceKey]); text != "42" {
		t.Errorf("processInstanceKey bound to %q, want the scope key 42", text)
	}
	if bindVars(1, scopeVars, nil) != nil {
		t.Error("bindVars with no names = non-nil, want nil")
	}
}

// resolveValue returns a literal verbatim, evaluates a FEEL expression to its string
// form, and yields "" for a FEEL null (an unbound variable), matching the engine's
// null-propagating contract.
func TestResolveValue(t *testing.T) {
	if got := resolveValue(compiler.RestExpr{Literal: "verbatim"}, 1, nil); got != "verbatim" {
		t.Errorf("literal = %q, want verbatim", got)
	}
	e, err := expr.CompileAuto(`"x-" + name`)
	if err != nil {
		t.Fatalf("CompileAuto: %v", err)
	}
	vars := map[string]model.VariableValue{"name": {Name: "name", Kind: model.VarString, Text: "y"}}
	if got := resolveValue(compiler.RestExpr{Expr: e}, 1, vars); got != "x-y" {
		t.Errorf("FEEL value = %q, want x-y", got)
	}
	// An unbound variable makes the concatenation FEEL-null → "".
	if got := resolveValue(compiler.RestExpr{Expr: e}, 1, nil); got != "" {
		t.Errorf("FEEL null = %q, want empty", got)
	}
}
