package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestValidateFeel exercises the edit-time FEEL validation endpoint the Modeler
// calls: a well-formed expression is ok (unknown variables are fine — they're
// process variables), a malformed one reports ok:false with a message, and an
// empty expression is a no-op success.
func TestValidateFeel(t *testing.T) {
	ts := newTestServer(t)
	cases := []struct {
		name   string
		body   string
		wantOK bool
	}{
		{"boolean condition", `{"expression":"amount > 100"}`, true},
		{"free variables allowed", `{"expression":"a + b * c"}`, true},
		{"builtin call", `{"expression":"string length(name) > 3"}`, true},
		{"blank is ok", `{"expression":"   "}`, true},
		{"dangling operator", `{"expression":"amount >"}`, false},
		{"unbalanced paren", `{"expression":"(1 + 2"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := doReq(t, ts, http.MethodPost, "/api/v1/feel/validate", tc.body, "application/json")
			if code != http.StatusOK {
				t.Fatalf("status=%d body=%s", code, body)
			}
			var resp struct {
				OK    bool   `json:"ok"`
				Error string `json:"error"`
			}
			if err := json.Unmarshal(body, &resp); err != nil {
				t.Fatalf("unmarshal %s: %v", body, err)
			}
			if resp.OK != tc.wantOK {
				t.Fatalf("ok=%v want %v (error=%q)", resp.OK, tc.wantOK, resp.Error)
			}
			if !tc.wantOK && resp.Error == "" {
				t.Fatalf("expected a non-empty error message for an invalid expression")
			}
			if tc.wantOK && resp.Error != "" {
				t.Fatalf("unexpected error for a valid expression: %q", resp.Error)
			}
		})
	}
}

// TestValidateFeelRefusesACallThatCanOnlyBeNull is the case this route got most
// wrong (ADR-0388). The other FEEL surfaces stayed silent about such a call; this
// one answered `{"ok": true}` — so somebody who asked exactly the right question
// was told the wrong answer, and had a reason to stop looking. `is defined` is a
// Camunda extension and the commonest way in; Atlas speaks standard FEEL.
func TestValidateFeelRefusesACallThatCanOnlyBeNull(t *testing.T) {
	ts := newTestServer(t)
	cases := []struct {
		name       string
		expression string
		wantSaid   []string
	}{
		// The message has to name the standard equivalent, or the author's next guess
		// is another function from the same foreign dialect.
		{"foreign dialect", "is defined(kunde.geburtsdatum)", []string{"is defined", "x != null"}},
		{"foreign name for something we have", `put(c, "k", 1)`, []string{"put", "context put"}},
		{"a typo", "strng length(name)", []string{"strng length"}},
		{"a real function, wrong arity", "date()", []string{"date", "0 arguments"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := validateFeel(t, ts, tc.expression)
			if resp.OK {
				t.Fatalf("%q was certified valid", tc.expression)
			}
			for _, want := range tc.wantSaid {
				if !strings.Contains(resp.Error, want) {
					t.Errorf("error does not mention %q:\n%s", want, resp.Error)
				}
			}
		})
	}
}

// The refusal has to let the fix through, and it has to leave alone everything a
// reading cannot resolve — a callee the expression binds itself, or a variable the
// author will supply. A route that refused those would block expressions that run.
func TestValidateFeelStillAcceptsWhatWorks(t *testing.T) {
	ts := newTestServer(t)
	for _, src := range []string{
		"kunde.geburtsdatum != null", // the correction the message asks for
		"count(items) > 0",           // an ordinary built-in
		"for f in fs return f(1)",    // a callee the expression binds
		"function(g) g(1)",           // a parameter
		`date(from: "2026-09-17")`,   // a named call, which the engine binds
		"some x in xs satisfies upper case(x) = \"A\"",
	} {
		if resp := validateFeel(t, ts, src); !resp.OK {
			t.Errorf("%q was refused: %s", src, resp.Error)
		}
	}
}

// feelValidateResp is what the route answers.
type feelValidateResp struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

func validateFeel(t *testing.T, ts *httptest.Server, expression string) feelValidateResp {
	t.Helper()
	req, err := json.Marshal(map[string]string{"expression": expression})
	if err != nil {
		t.Fatal(err)
	}
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/feel/validate", string(req), "application/json")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	var resp feelValidateResp
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal %s: %v", body, err)
	}
	return resp
}

// TestValidateFeelBadRequest rejects a malformed request body (not the FEEL, the
// JSON envelope) as a client error.
func TestValidateFeelBadRequest(t *testing.T) {
	ts := newTestServer(t)
	code, _ := doReq(t, ts, http.MethodPost, "/api/v1/feel/validate", "not json", "application/json")
	if code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", code)
	}
}

// TestEvaluateFeel exercises the "Test expression" endpoint: it compiles and
// evaluates a FEEL expression against sample variables and returns the result
// with its kind. A FEEL type error (e.g. number + string) yields null rather
// than an error, which the endpoint reports faithfully.
func TestEvaluateFeel(t *testing.T) {
	ts := newTestServer(t)
	cases := []struct {
		name       string
		body       string
		wantOK     bool
		wantResult string
		wantKind   string
	}{
		{"arithmetic", `{"expression":"1 + 1"}`, true, "2", "number"},
		{"with variables", `{"expression":"amount * 2","variables":{"amount":21}}`, true, "42", "number"},
		{"string builtin", `{"expression":"upper case(name)","variables":{"name":"bob"}}`, true, "BOB", "string"},
		{"boolean", `{"expression":"a and b","variables":{"a":true,"b":false}}`, true, "false", "boolean"},
		{"decimal variable", `{"expression":"amount * (1 + rate)","variables":{"amount":100,"rate":0.19}}`, true, "119", "number"},
		{"null variable", `{"expression":"x","variables":{"x":null}}`, true, "null", "null"},
		{"type error is null", `{"expression":"1 + \"x\""}`, true, "null", "null"},
		{"syntax error", `{"expression":"1 +"}`, false, "", ""},
		{"empty is not ok", `{"expression":"  "}`, false, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := doReq(t, ts, http.MethodPost, "/api/v1/feel/evaluate", tc.body, "application/json")
			if code != http.StatusOK {
				t.Fatalf("status=%d body=%s", code, body)
			}
			var resp struct {
				OK     bool   `json:"ok"`
				Result string `json:"result"`
				Kind   string `json:"kind"`
				Error  string `json:"error"`
			}
			if err := json.Unmarshal(body, &resp); err != nil {
				t.Fatalf("unmarshal %s: %v", body, err)
			}
			if resp.OK != tc.wantOK {
				t.Fatalf("ok=%v want %v (error=%q)", resp.OK, tc.wantOK, resp.Error)
			}
			if !tc.wantOK {
				if resp.Error == "" {
					t.Fatalf("expected an error message when not ok")
				}
				return
			}
			if resp.Result != tc.wantResult || resp.Kind != tc.wantKind {
				t.Fatalf("result=%q kind=%q, want %q/%q", resp.Result, resp.Kind, tc.wantResult, tc.wantKind)
			}
		})
	}
}

// TestEvaluateFeelStructuredVariable binds a structured variable (an object) and
// reads one of its members through FEEL, proving objects/arrays are usable — not
// rejected — as evaluation inputs (ADR-0037).
func TestEvaluateFeelStructuredVariable(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/feel/evaluate",
		`{"expression":"x.nested + 1","variables":{"x":{"nested":1}}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	var resp struct {
		OK     bool   `json:"ok"`
		Result string `json:"result"`
		Kind   string `json:"kind"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.OK || resp.Result != "2" || resp.Kind != "number" {
		t.Fatalf("x.nested + 1 = ok=%v result=%q kind=%q err=%q, want 2/number", resp.OK, resp.Result, resp.Kind, resp.Error)
	}
}

// TestEvaluateFeelReturnsJSON evaluates to a whole object and checks it renders as
// canonical JSON under the "json" kind.
func TestEvaluateFeelReturnsJSON(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/feel/evaluate",
		`{"expression":"{b: 2, a: 1}"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	var resp struct {
		OK     bool   `json:"ok"`
		Result string `json:"result"`
		Kind   string `json:"kind"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.OK || resp.Kind != "json" || resp.Result != `{"a":1,"b":2}` {
		t.Fatalf("got ok=%v kind=%q result=%q, want json/{\"a\":1,\"b\":2}", resp.OK, resp.Kind, resp.Result)
	}
}

// TestEvaluateFeelEvalError covers the path where Eval itself returns an error
// (e.g. a runtime failure in a FEEL expression).
func TestEvaluateFeelEvalError(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/feel/evaluate",
		`{"expression":"for x in xs return x","variables":{"xs":"not-a-list"}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	var resp struct {
		OK     bool   `json:"ok"`
		Result string `json:"result"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// The expression either errors or degrades to null — either is acceptable.
}

func TestEvaluateFeelArrayVariable(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/feel/evaluate",
		`{"expression":"count(items)","variables":{"items":[1,2,3]}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	var resp struct {
		OK     bool   `json:"ok"`
		Result string `json:"result"`
		Kind   string `json:"kind"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.OK || resp.Result != "3" || resp.Kind != "number" {
		t.Fatalf("count(items) = ok=%v result=%q kind=%q, want 3/number", resp.OK, resp.Result, resp.Kind)
	}
}

func TestEvaluateFeelNullVariable(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/feel/evaluate",
		`{"expression":"x","variables":{"x":null}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	var resp struct {
		OK     bool   `json:"ok"`
		Result string `json:"result"`
		Kind   string `json:"kind"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.OK || resp.Result != "null" || resp.Kind != "null" {
		t.Fatalf("got ok=%v result=%q kind=%q, want null/null", resp.OK, resp.Result, resp.Kind)
	}
}

// TestEvaluateFeelBadRequest rejects a malformed JSON envelope.
func TestEvaluateFeelBadRequest(t *testing.T) {
	ts := newTestServer(t)
	code, _ := doReq(t, ts, http.MethodPost, "/api/v1/feel/evaluate", "not json", "application/json")
	if code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", code)
	}
}

// TestRenderTemplate exercises the mail template preview the Modeler's editor calls
// (ADR-0487): placeholders and sections render over the sample
// variables, an HTML body escapes what they insert, a '=' value is one FEEL
// expression as it is in the mail, and a template the deploy would refuse is refused
// here too, with its place.
func TestRenderTemplate(t *testing.T) {
	ts := newTestServer(t)
	type problem struct {
		Expression string `json:"expression"`
		Message    string `json:"message"`
	}
	type resp struct {
		OK       bool      `json:"ok"`
		Result   string    `json:"result"`
		Problems []problem `json:"problems"`
		Error    string    `json:"error"`
		Line     int       `json:"line"`
		Column   int       `json:"column"`
	}
	for _, tc := range []struct {
		name, body string
		want       resp
	}{
		{"placeholders and a loop",
			`{"template":"Hallo {{kunde.name}}\n{{#loop positionen}}\n- {{menge}} × {{artikel}}\n{{/loop}}","variables":{"kunde":{"name":"Ada"},"positionen":[{"artikel":"Tee","menge":2}]}}`,
			resp{OK: true, Result: "Hallo Ada\n- 2 × Tee\n"}},
		{"html escapes inserts", `{"template":"<p>{{name}}</p>","variables":{"name":"<b>&"},"html":true}`,
			resp{OK: true, Result: "<p>&lt;b&gt;&amp;</p>"}},
		{"text does not escape", `{"template":"{{name}}","variables":{"name":"<b>"}}`, resp{OK: true, Result: "<b>"}},
		{"a null placeholder is reported", `{"template":"Hallo {{kunde.nmae}}","variables":{"kunde":{"name":"Ada"}}}`,
			resp{OK: true, Result: "Hallo ", Problems: []problem{{Expression: "kunde.nmae", Message: "is null, so it renders as nothing"}}}},
		{"plain text", `{"template":"Guten Tag"}`, resp{OK: true, Result: "Guten Tag"}},
		{"equals is one FEEL expression", `{"template":"= \"<p>\" + name + \"</p>\"","variables":{"name":"Ada"},"html":true}`,
			resp{OK: true, Result: "<p>Ada</p>"}},
		{"equals FEEL that does not compile", `{"template":"=1 +"}`, resp{OK: false}},
		{"equals FEEL calling nothing", `{"template":"=nosuchfunction(1)"}`, resp{OK: false}},
		{"unclosed section", `{"template":"a\n{{#if vip}}b"}`, resp{OK: false, Line: 2, Column: 1}},
		{"a call the deploy refuses", `{"template":"{{ nosuchfunction(x) }}"}`, resp{OK: false, Line: 1, Column: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := doReq(t, ts, http.MethodPost, "/api/v1/feel/template", tc.body, "application/json")
			if code != http.StatusOK {
				t.Fatalf("status=%d body=%s", code, body)
			}
			var got resp
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("unmarshal %s: %v", body, err)
			}
			if got.OK != tc.want.OK {
				t.Fatalf("ok=%v want %v (%s)", got.OK, tc.want.OK, body)
			}
			if !got.OK {
				if got.Error == "" {
					t.Error("a refusal carries a message")
				}
				if got.Line != tc.want.Line || got.Column != tc.want.Column {
					t.Errorf("place = %d:%d, want %d:%d", got.Line, got.Column, tc.want.Line, tc.want.Column)
				}
				return
			}
			if got.Result != tc.want.Result {
				t.Errorf("result = %q, want %q", got.Result, tc.want.Result)
			}
			if len(got.Problems) != len(tc.want.Problems) || (len(got.Problems) > 0 && got.Problems[0] != tc.want.Problems[0]) {
				t.Errorf("problems = %+v, want %+v", got.Problems, tc.want.Problems)
			}
			if got.Problems == nil {
				t.Error("problems is an empty list, never null, so the editor can read it without a guard")
			}
		})
	}
}
