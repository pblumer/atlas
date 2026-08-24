package mail

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

// TestToExprKind asserts the stored-kind → expr-kind mapping over every variable
// kind, including the null/default fallthrough, so a FEEL binding sees the right type.
func TestToExprKind(t *testing.T) {
	cases := map[model.VarKind]expr.ValueKind{
		model.VarBool:     expr.KindBool,
		model.VarNumber:   expr.KindNumber,
		model.VarString:   expr.KindString,
		model.VarJSON:     expr.KindJSON,
		model.VarNull:     expr.KindNull,
		model.VarKind(99): expr.KindNull, // unknown kind falls through to null
	}
	for in, want := range cases {
		if got := toExprKind(in); got != want {
			t.Errorf("toExprKind(%v) = %v, want %v", in, got, want)
		}
	}
}

// TestResolveValueBranches covers both arms of resolveValue: a literal is returned
// verbatim, a compiled FEEL expression is evaluated over the bound scope variables,
// and an unbound reference (FEEL null) coerces to the empty string.
func TestResolveValueBranches(t *testing.T) {
	if got := resolveValue(compiler.RestExpr{Literal: "plain@example.com"}, 1, nil); got != "plain@example.com" {
		t.Errorf("literal resolveValue = %q, want the literal", got)
	}
	e, err := expr.CompileAuto(`"to:" + who`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	vars := map[string]model.VariableValue{"who": {Name: "who", Kind: model.VarString, Text: "ada"}}
	if got := resolveValue(compiler.RestExpr{Expr: e}, 7, vars); got != "to:ada" {
		t.Errorf("expr resolveValue = %q, want the evaluated string", got)
	}
	// An unbound reference evaluates to FEEL null → "".
	missing, err := expr.CompileAuto(`nope`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if got := resolveValue(compiler.RestExpr{Expr: missing}, 7, nil); got != "" {
		t.Errorf("unbound resolveValue = %q, want empty", got)
	}
}

// TestBindVarsEmpty covers bindVars' early return for an expression that reads no
// variables (a constant), which needs no binding.
func TestBindVarsEmpty(t *testing.T) {
	if got := bindVars(1, nil, nil); got != nil {
		t.Errorf("bindVars with no names = %v, want nil", got)
	}
}

// TestBuildRFC822PlainOnly pins the shape a text-only message keeps: one text/plain
// part, no multipart framing. This is every message authored before HTML bodies
// existed, so a change here changes what already-deployed processes send.
func TestBuildRFC822PlainOnly(t *testing.T) {
	got := string(buildRFC822(Message{
		To: []string{"ops@example.com"}, Subject: "Order shipped", Body: "On its way.", MessageID: "42",
	}, "atlas@example.com"))

	for _, want := range []string{
		"To: ops@example.com\r\n",
		"Content-Type: text/plain; charset=utf-8\r\n",
		"\r\nOn its way.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("plain message is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "multipart") || strings.Contains(got, "text/html") {
		t.Errorf("a text-only message must not be framed as multipart or HTML:\n%s", got)
	}
}

// TestBuildRFC822HTMLOnly: an HTML body with no plain-text alternative is sent as a
// single text/html part — not as a degenerate multipart with an empty half.
func TestBuildRFC822HTMLOnly(t *testing.T) {
	got := string(buildRFC822(Message{
		To: []string{"ops@example.com"}, Subject: "Order shipped", HTML: "<p>On its way.</p>", MessageID: "42",
	}, "atlas@example.com"))

	if !strings.Contains(got, "Content-Type: text/html; charset=utf-8\r\n") {
		t.Errorf("HTML-only message is not framed as text/html:\n%s", got)
	}
	if strings.Contains(got, "multipart") {
		t.Errorf("HTML-only message must not be multipart:\n%s", got)
	}
	if !strings.HasSuffix(got, "<p>On its way.</p>") {
		t.Errorf("HTML body is not the message body:\n%s", got)
	}
}

// TestBuildRFC822Alternative: both bodies produce multipart/alternative with the
// plain text first and the HTML last — the order that makes a client pick the
// richest part it can render (RFC 2046 §5.1.4).
func TestBuildRFC822Alternative(t *testing.T) {
	got := string(buildRFC822(Message{
		To: []string{"ops@example.com"}, Subject: "Order shipped",
		Body: "On its way.", HTML: "<p>On its way.</p>", MessageID: "42",
	}, "atlas@example.com"))

	if !strings.Contains(got, "Content-Type: multipart/alternative; boundary=\"") {
		t.Fatalf("message is not multipart/alternative:\n%s", got)
	}
	plain := strings.Index(got, "text/plain")
	html := strings.Index(got, "text/html")
	if plain < 0 || html < 0 || plain > html {
		t.Errorf("want the plain part before the HTML part, got plain=%d html=%d:\n%s", plain, html, got)
	}
	if !strings.Contains(got, "\r\nOn its way.\r\n") || !strings.Contains(got, "\r\n<p>On its way.</p>\r\n") {
		t.Errorf("both bodies must appear verbatim:\n%s", got)
	}
	// The closing delimiter is what makes the message parseable at all.
	b := got[strings.Index(got, `boundary="`)+len(`boundary="`):]
	b = b[:strings.Index(b, `"`)]
	if !strings.HasSuffix(got, "--"+b+"--\r\n") {
		t.Errorf("multipart message does not end with its closing delimiter:\n%s", got)
	}
	// Deterministic framing: same message in, same bytes out (no clock, no random).
	again := string(buildRFC822(Message{
		To: []string{"ops@example.com"}, Subject: "Order shipped",
		Body: "On its way.", HTML: "<p>On its way.</p>", MessageID: "42",
	}, "atlas@example.com"))
	if got != again {
		t.Error("buildRFC822 is not deterministic for the same message")
	}
}

// TestBuildRFC822BoundaryAvoidsContent: a body that happens to contain the boundary
// text would end the part early and truncate the mail. The boundary is extended
// until it appears in neither body.
func TestBuildRFC822BoundaryAvoidsContent(t *testing.T) {
	m := Message{To: []string{"ops@example.com"}, MessageID: "42"}
	m.Body = "see --" + mimeBoundary(m) + " below"
	m.HTML = "<p>hi</p>"

	got := string(buildRFC822(m, "atlas@example.com"))
	b := got[strings.Index(got, `boundary="`)+len(`boundary="`):]
	b = b[:strings.Index(b, `"`)]
	if strings.Contains(m.Body, b) || strings.Contains(m.HTML, b) {
		t.Errorf("boundary %q still occurs in a body part:\n%s", b, got)
	}
	if strings.Count(got, "--"+b) != 3 { // two part delimiters + the closing one
		t.Errorf("want three boundary delimiters, got %d:\n%s", strings.Count(got, "--"+b), got)
	}
}
