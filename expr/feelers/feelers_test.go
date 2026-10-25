package feelers_test

import (
	"encoding/json"
	"errors"
	"html"
	"strings"
	"testing"

	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/expr/feelers"
)

func compile(t *testing.T, src string) *feelers.Template {
	t.Helper()
	tpl, err := feelers.Compile(src, expr.CompileAuto)
	if err != nil {
		t.Fatalf("Compile(%q): %v", src, err)
	}
	return tpl
}

// vars builds a scope from JSON, the shape a variable arrives in.
func vars(t *testing.T, js string) feelers.Vars {
	t.Helper()
	var raw map[string]any
	dec := json.NewDecoder(strings.NewReader(js))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		t.Fatal(err)
	}
	out := feelers.Vars{}
	for k, v := range raw {
		out[k] = expr.FromJSON(v)
	}
	return out
}

// TestRenderMatchesFeelers holds the renderer to what feelers 3.1.0 answers for the
// same template and context: the examples of its README first, then the rules its
// interpreter encodes (truthiness, loop shapes, the line breaks the tags swallow).
// A template previewed in a form and sent in a mail must read the same.
func TestRenderMatchesFeelers(t *testing.T) {
	for _, tc := range []struct {
		name, tpl, vars, want string
	}{
		{"plain text", "My simple string", `{}`, "My simple string"},
		{"insert", "I'm sorry {{user}}, I'm afraid I can't do that.", `{"user":"Dave"}`, "I'm sorry Dave, I'm afraid I can't do that."},
		{"insert with FEEL", `Hey there {{user}}, welcome. {{if age >= 18 then "Have a beer!" else "Here's some apple juice."}}`,
			`{"user":"Dave","age":24}`, "Hey there Dave, welcome. Have a beer!"},
		{"conditional", "{{#if count(users) > 1}}There are multiple users{{/if}}", `{"users":["Bob","Dave"]}`, "There are multiple users"},
		{"loop", "{{user}}'s hobbies:\n{{#loop hobbies}}\n- {{this}}\n{{/loop}}", `{"user":"Dave","hobbies":["surfing","coding"]}`,
			"Dave's hobbies:\n- surfing\n- coding\n"},
		{"optional equals in an insert", "{{= 1 + 2}}", `{}`, "3"},
		{"empty insert", "a{{}}b{{=}}c{{  }}d", `{}`, "abcd"},
		{"braces as text", `{{"{{"}}x}}`, `{}`, "{{x}}"},
		{"number as FEEL writes it", "{{total}}", `{"total":10.50}`, "10.5"},
		{"list as FEEL writes it", "{{tags}}", `{"tags":[1,2]}`, "[1, 2]"},
		{"a missing name is nothing", "a{{nope}}b", `{}`, "ab"},
		{"loop fields are names", "{{#loop items}}{{sku}}={{qty}};{{/loop}}", `{"items":[{"sku":"A","qty":2},{"sku":"B","qty":1}]}`, "A=2;B=1;"},
		{"outer names through parent", "{{#loop items}}{{sku}} {{parent.currency}};{{/loop}}", `{"currency":"CHF","items":[{"sku":"A"}]}`, "A CHF;"},
		{"outer names are not inherited", "{{#loop items}}[{{currency}}]{{/loop}}", `{"currency":"CHF","items":[{"sku":"A"}]}`, "[]"},
		{"nested loops reach two up", "{{#loop orders}}{{#loop lines}}{{parent.parent.shop}}/{{parent.id}}/{{this}} {{/loop}}{{/loop}}",
			`{"shop":"S","orders":[{"id":1,"lines":["x","y"]}]}`, "S/1/x S/1/y "},
		{"this at the top is the scope", "{{this.a}}", `{"a":7}`, "7"},
		{"a field called this shadows the keyword", "{{#loop items}}{{this}}|{{_this_.this}}{{/loop}}", `{"items":[{"this":"field"}]}`, "field|field"},
		{"_parent_ at the top is null", "[{{_parent_}}]", `{}`, "[]"},
		{"parent of a scalar loop", "{{#loop xs}}{{this}}{{parent.sep}}{{/loop}}", `{"sep":",","xs":[1,2]}`, "1,2,"},
		{"loop over null renders nothing", "a{{#loop nothing}}x{{/loop}}b", `{"nothing":null}`, "ab"},
		{"loop over a single value runs once", "{{#loop one}}<{{this}}>{{/loop}}", `{"one":"x"}`, "<x>"},
		{"false is not rendered", "{{#if f}}x{{/if}}", `{"f":false}`, ""},
		{"null is not rendered", "{{#if n}}x{{/if}}", `{"n":null}`, ""},
		{"zero is not rendered", "{{#if z}}x{{/if}}", `{"z":0}`, ""},
		{"empty string is not rendered", `{{#if s}}x{{/if}}`, `{"s":""}`, ""},
		{"a fraction is rendered", "{{#if z}}x{{/if}}", `{"z":0.5}`, "x"},
		{"an empty list is rendered", "{{#if l}}x{{/if}}", `{"l":[]}`, "x"},
		{"the string false is rendered", `{{#if s}}x{{/if}}`, `{"s":"false"}`, "x"},
		{"a context is rendered", `{{#if c}}x{{/if}}`, `{"c":{}}`, "x"},
		{"a false section takes its lines with it", "a\n{{#if false}}\nb\n{{/if}}\nc", `{}`, "a\nc"},
		{"a true section keeps its lines", "a\n{{#if true}}\nb\n{{/if}}\nc", `{}`, "a\nb\nc"},
		{"a closing tag puts its line break back", "{{#if true}}x{{/if}}\ny", `{}`, "x\ny"},
		{"an empty loop leaves its line break, as in feelers", "a\n{{#loop []}}\nx\n{{/loop}}\nb", `{}`, "a\n\nb"},
		{"a CRLF after a tag stays", "{{#if true}}\r\nx{{/if}}", `{}`, "\r\nx"},
		{"umlauts pass through", "Grüezi {{name}}", `{"name":"Zoë"}`, "Grüezi Zoë"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := compile(t, tc.tpl).Render(vars(t, tc.vars), nil)
			if got != tc.want {
				t.Errorf("Render(%q) = %q; want %q", tc.tpl, got, tc.want)
			}
		})
	}
}

// TestRenderEscapesInsertsOnly is the HTML body's guarantee: a variable's value cannot
// add markup, while the template's own markup stays markup.
func TestRenderEscapesInsertsOnly(t *testing.T) {
	tpl := compile(t, `<p class="x">Hallo {{name}}</p>{{#loop items}}<li>{{this}}</li>{{/loop}}`)
	got, _ := tpl.Render(vars(t, `{"name":"<b>Ada & \"Co\"</b>","items":["<script>"]}`), html.EscapeString)
	want := `<p class="x">Hallo &lt;b&gt;Ada &amp; &#34;Co&#34;&lt;/b&gt;</p><li>&lt;script&gt;</li>`
	if got != want {
		t.Errorf("Render = %q; want %q", got, want)
	}
}

// TestRenderReportsWhatRendersAsNothing lets a preview tell an author which
// placeholder came out empty — usually a misspelt variable.
func TestRenderReportsWhatRendersAsNothing(t *testing.T) {
	tpl := compile(t, "Hallo {{kunde.nmae}}, {{kunde.name}}")
	got, problems := tpl.Render(vars(t, `{"kunde":{"name":"Ada"}}`), nil)
	if got != "Hallo , Ada" {
		t.Errorf("Render = %q", got)
	}
	if len(problems) != 1 || problems[0].Expression != "kunde.nmae" || !strings.Contains(problems[0].Message, "null") {
		t.Errorf("problems = %+v; want one null report for kunde.nmae", problems)
	}
}

// TestRenderSkipsFalsySectionsQuietly keeps the report to what an author can act
// on: a section that is simply not rendered is not a problem, a null placeholder is.
func TestRenderSkipsFalsySectionsQuietly(t *testing.T) {
	tpl := compile(t, "a{{#if missing}}b{{/if}}{{#loop missing}}c{{/loop}}d")
	got, problems := tpl.Render(feelers.Vars{}, nil)
	if got != "ad" || len(problems) != 0 {
		t.Errorf("Render = %q, %+v; want ad and no problems", got, problems)
	}
}

// TestCompileRefuses holds the deploy-time strictness: every malformed template is
// refused with the place of the tag at fault, where feelers would render a warning.
func TestCompileRefuses(t *testing.T) {
	for _, tc := range []struct {
		name, tpl  string
		line, col  int
		msgContain string
	}{
		{"unclosed placeholder", "ab\ncd {{x", 2, 4, "never closed with }}"},
		{"unclosed section", "x\n{{#if a}}y", 2, 1, "never closed with {{/if}}"},
		{"unclosed section opener", "{{#loop a", 1, 1, "never closed with }}"},
		{"stray end", "a{{/if}}", 1, 2, "no section to close"},
		{"crossed ends", "{{#if a}}{{/loop}}", 1, 10, "{{/if}} should close"},
		{"unknown section", "{{#each items}}x{{/each}}", 1, 1, "not a section this language knows"},
		{"section without a space", "{{#if}}", 1, 1, "not a section this language knows"},
		{"condition missing", "{{#if }}x{{/if}}", 1, 1, "needs a condition"},
		{"list missing", "{{#loop  }}x{{/loop}}", 1, 1, "needs a list"},
		{"FEEL that does not compile", "ä {{ 1 + }}", 1, 3, "{{ 1 + }}"},
		{"condition that does not compile", "{{#if 1 +}}x{{/if}}", 1, 1, "{{#if 1 +}}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := feelers.Compile(tc.tpl, expr.CompileAuto)
			var fe *feelers.Error
			if !errors.As(err, &fe) {
				t.Fatalf("Compile(%q) = %v; want a *feelers.Error", tc.tpl, err)
			}
			if fe.Line != tc.line || fe.Column != tc.col {
				t.Errorf("Compile(%q) at %d:%d; want %d:%d (%v)", tc.tpl, fe.Line, fe.Column, tc.line, tc.col, err)
			}
			if !strings.Contains(err.Error(), tc.msgContain) {
				t.Errorf("Compile(%q) = %q; want it to mention %q", tc.tpl, err, tc.msgContain)
			}
		})
	}
}

// TestCompileWrapsTheFEELError keeps the engine's own message reachable, so the
// compiler can say what is wrong with the expression and not only where it is.
func TestCompileWrapsTheFEELError(t *testing.T) {
	cause := errors.New("refused by the gate")
	_, err := feelers.Compile("{{x}}", func(string) (*expr.Compiled, error) { return nil, cause })
	if !errors.Is(err, cause) {
		t.Errorf("Compile error %v does not wrap the gate's", err)
	}
}

func TestIsTemplateAndSource(t *testing.T) {
	if feelers.IsTemplate("Hallo Welt") || !feelers.IsTemplate("Hallo {{name}}") {
		t.Error("IsTemplate does not tell text from a template")
	}
	if src := compile(t, "a{{b}}").Source(); src != "a{{b}}" {
		t.Errorf("Source = %q", src)
	}
	names := feelers.Vars{"a": expr.Number(1), "b": expr.Number(2)}.Names()
	if len(names) != 2 {
		t.Errorf("Names = %v", names)
	}
}
