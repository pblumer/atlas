package ldif

import (
	"errors"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
)

// A content record is a dn plus attributes, blank-line separated, and a repeated
// attribute is how a multi-valued one is written.
func TestParseLDIF(t *testing.T) {
	const data = `version: 1
# ein Kommentar
dn: uid=ada,ou=people,dc=example,dc=com
objectClass: top
objectClass: person
cn: Ada Lovelace
mail: ada@example.com

dn: uid=bob,ou=people,dc=example,dc=com
cn: Bob
`
	got, err := Parse(FormatLDIF, []byte(data))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("entries = %d, want 2", len(got))
	}
	if got[0].DN != "uid=ada,ou=people,dc=example,dc=com" {
		t.Errorf("dn = %q", got[0].DN)
	}
	if len(got[0].Attributes["objectClass"]) != 2 {
		t.Errorf("objectClass = %v, want both values", got[0].Attributes["objectClass"])
	}
	if got[0].Attributes["cn"][0] != "Ada Lovelace" {
		t.Errorf("cn = %v", got[0].Attributes["cn"])
	}
}

// A producer wraps at 76 characters, so a parser reading line-by-line splits long
// DNs and base64 values in half.
func TestParseLDIFUnfoldsContinuations(t *testing.T) {
	const data = "dn: uid=ada,ou=a-very-long-organisational-unit,\n dc=example,dc=com\ncn: Ada\n"
	got, err := Parse(FormatLDIF, []byte(data))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got[0].DN != "uid=ada,ou=a-very-long-organisational-unit,dc=example,dc=com" {
		t.Errorf("dn = %q, want the folded line joined", got[0].DN)
	}
}

// "attr:: value" is base64, which is how a value with a newline, a leading space or
// non-ASCII text is carried. Ignoring the second colon corrupts exactly the values
// that needed encoding.
func TestParseLDIFBase64(t *testing.T) {
	const data = "dn: uid=ada,dc=x\ncn:: TcO8bGxlcg==\n" // "Müller"
	got, err := Parse(FormatLDIF, []byte(data))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got[0].Attributes["cn"][0] != "Müller" {
		t.Errorf("cn = %q, want the decoded value", got[0].Attributes["cn"][0])
	}
}

func TestParseLDIFErrors(t *testing.T) {
	for _, tc := range []struct{ name, data, want string }{
		{"attribute before a dn", "cn: Ada\n", "before any dn"},
		{"no separator", "dn: uid=a,dc=x\nkaputt\n", "no attribute separator"},
		{"empty name", "dn: uid=a,dc=x\n: wert\n", "names no attribute"},
		{"bad base64", "dn: uid=a,dc=x\ncn:: !!!\n", "does not decode"},
		{"url value", "dn: uid=a,dc=x\njpegPhoto:< file:///x\n", "does not fetch"},
		{"no entries", "version: 1\n\n", "holds no entries"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(FormatLDIF, []byte(tc.data))
			if err == nil {
				t.Fatalf("want an error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// What Render writes, Parse reads back — including the values that must be encoded
// to survive the format at all.
func TestLDIFRoundTrip(t *testing.T) {
	in := []Entry{{
		DN: "uid=müller,ou=people,dc=example,dc=com",
		Attributes: map[string][]string{
			"objectClass": {"top", "person"},
			"cn":          {"Müller"},
			"description": {" führendes Leerzeichen", "zwei\nZeilen", "schlicht"},
		},
	}}
	out, err := Render(FormatLDIF, in)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	back, err := Parse(FormatLDIF, []byte(out))
	if err != nil {
		t.Fatalf("Parse back: %v\n%s", err, out)
	}
	if len(back) != 1 || back[0].DN != in[0].DN {
		t.Fatalf("round trip dn = %+v, from:\n%s", back, out)
	}
	for name, want := range in[0].Attributes {
		got := back[0].Attributes[name]
		if len(got) != len(want) {
			t.Fatalf("%s = %q, want %q\n%s", name, got, want, out)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s[%d] = %q, want %q", name, i, got[i], want[i])
			}
		}
	}
}

// A value that would not survive the plain form is encoded. Getting this wrong is the
// quiet kind of bug: the file still parses, it just no longer says what it said.
func TestNeedsBase64(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"schlicht", false},
		{"", false},
		{" führend", true},
		{"nachlaufend ", true},
		{":doppelpunkt", true},
		{"<kleiner", true},
		{"zwei\nZeilen", true},
		{"Müller", true},
	} {
		if got := needsBase64(tc.in); got != tc.want {
			t.Errorf("needsBase64(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// The output is stable: attributes in a fixed order, so a file written twice from the
// same entries is the same file.
func TestRenderLDIFIsStable(t *testing.T) {
	e := []Entry{{DN: "uid=a,dc=x", Attributes: map[string][]string{"z": {"1"}, "a": {"2"}, "m": {"3"}}}}
	first, err := Render(FormatLDIF, e)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := Render(FormatLDIF, e)
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		if again != first {
			t.Fatalf("two renders of the same entries differ:\n%s\n---\n%s", first, again)
		}
	}
	if strings.Index(first, "\na: ") > strings.Index(first, "\nm: ") {
		t.Errorf("attributes are not sorted:\n%s", first)
	}
}

func TestRenderErrors(t *testing.T) {
	if _, err := Render(FormatLDIF, nil); err == nil {
		t.Error("writing no entries must fail rather than produce an empty file")
	}
	if _, err := Render(FormatLDIF, []Entry{{Attributes: map[string][]string{"cn": {"x"}}}}); err == nil {
		t.Error("an entry with no dn must fail: a directory entry is addressed by its name")
	}
	if _, err := Render("json", []Entry{{DN: "uid=a"}}); err == nil {
		t.Error("an unknown format must fail")
	}
	if _, err := Parse("json", []byte("{}")); err == nil {
		t.Error("an unknown format must fail on read too")
	}
}

func TestKnownFormat(t *testing.T) {
	for _, f := range Formats() {
		if !KnownFormat(f) {
			t.Errorf("KnownFormat(%q) = false", f)
		}
	}
	// There is deliberately no default: guessing the format from the bytes is how a
	// malformed file becomes a plausible-looking empty result.
	if KnownFormat("") {
		t.Error("the empty format must not be accepted")
	}
	if KnownFormat("csv") {
		t.Error("a table format belongs to the text-file worker, not this one")
	}
}

// The JSON shape is the one the directory workers produce, so a process handles a
// file and a live directory the same way.
func TestEntriesToJSON(t *testing.T) {
	got, ok := EntriesToJSON([]Entry{{DN: "uid=a,dc=x", Attributes: map[string][]string{"cn": {"A"}}}}).([]any)
	if !ok || len(got) != 1 {
		t.Fatalf("EntriesToJSON = %#v", got)
	}
	e := got[0].(map[string]any)
	if e["dn"] != "uid=a,dc=x" {
		t.Errorf("dn = %v", e["dn"])
	}
	attrs, ok := e["attributes"].(map[string]any)
	if !ok || attrs["cn"].([]string)[0] != "A" {
		t.Errorf("attributes = %#v", e["attributes"])
	}
}

// --- the resolved job, shared by the engine and a worker ---

type fakeVarStore struct {
	vars    map[uint64][]model.VariableValue
	ei      map[uint64]model.ElementInstanceValue
	varsErr error
}

func (f fakeVarStore) VariablesOfScope(scope uint64, fn func(*model.VariableValue) error) error {
	if f.varsErr != nil {
		return f.varsErr
	}
	for i := range f.vars[scope] {
		v := f.vars[scope][i]
		if err := fn(&v); err != nil {
			return err
		}
	}
	return nil
}

func (f fakeVarStore) GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error) {
	ei, ok := f.ei[key]
	if !ok {
		return nil, false, nil
	}
	return &ei, true, nil
}

func buildTask(t *testing.T, cfg compiler.LdifConfig) (*compiler.CompiledProcess, *compiler.ConnectorTaskDetail) {
	t.Helper()
	b := compiler.NewBuilder(1, "p", 1)
	el := b.AddLdifConnectorTask(cfg)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ConnectorTask(cp.Node(el).Detail)
}

func TestResolveAndRunRead(t *testing.T) {
	cp, d := buildTask(t, compiler.LdifConfig{
		Format: FormatLDIF, Operation: OperationRead, Source: "datei", Result: "eintraege",
	})
	store := fakeVarStore{vars: map[uint64][]model.VariableValue{
		1: {{Name: "datei", Kind: model.VarString, Text: "dn: uid=ada,dc=x\ncn: Ada\n"}},
	}}
	j, err := Resolve(store, cp, d, 1)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	res, err := Run(j)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	vars := res.Variables()
	if vars["entryCount"] != 1 {
		t.Errorf("entryCount = %v", vars["entryCount"])
	}
	entries, ok := vars["eintraege"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("entries = %#v", vars["eintraege"])
	}
}

// The write direction takes the {dn, attributes} array a directory read produces, so
// a live directory's result can be written straight to a file.
func TestResolveAndRunWrite(t *testing.T) {
	cp, d := buildTask(t, compiler.LdifConfig{
		Format: FormatLDIF, Operation: OperationWrite, Source: "eintraege", Result: "datei",
	})
	store := fakeVarStore{vars: map[uint64][]model.VariableValue{
		1: {{Name: "eintraege", Kind: model.VarJSON,
			Text: `[{"dn":"uid=ada,dc=x","attributes":{"cn":["Ada"]}}]`}},
	}}
	j, err := Resolve(store, cp, d, 1)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	res, err := Run(j)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	text, ok := res.Variables()["datei"].(string)
	if !ok || !strings.Contains(text, "dn: uid=ada,dc=x") {
		t.Errorf("written file = %#v", res.Variables()["datei"])
	}
}

func TestResolveErrors(t *testing.T) {
	cp, d := buildTask(t, compiler.LdifConfig{Format: FormatLDIF, Source: "datei", Result: "r"})
	if _, err := Resolve(fakeVarStore{}, cp, nil, 1); err == nil {
		t.Error("a nil detail must fail")
	}
	if _, err := Resolve(fakeVarStore{}, cp, d, 1); err == nil {
		t.Error("a missing source variable must fail")
	}
	// A read wants text.
	wrong := fakeVarStore{vars: map[uint64][]model.VariableValue{1: {{Name: "datei", Kind: model.VarJSON, Text: "[]"}}}}
	if _, err := Resolve(wrong, cp, d, 1); err == nil {
		t.Error("a read whose source is not text must fail")
	}
	// A write wants the entries.
	cpW, dW := buildTask(t, compiler.LdifConfig{Format: FormatLDIF, Operation: OperationWrite, Source: "datei", Result: "r"})
	txt := fakeVarStore{vars: map[uint64][]model.VariableValue{1: {{Name: "datei", Kind: model.VarString, Text: "x"}}}}
	if _, err := Resolve(txt, cpW, dW, 1); err == nil {
		t.Error("a write whose source is not the entries must fail")
	}
	if _, err := Resolve(fakeVarStore{varsErr: errTest}, cp, d, 1); err == nil {
		t.Error("a store error must fail the resolve")
	}
}

var errTest = errors.New("store unavailable")

// A source on an outer scope is visible at the task.
func TestResolveWalksTheScopeChain(t *testing.T) {
	cp, d := buildTask(t, compiler.LdifConfig{Format: FormatLDIF, Source: "datei", Result: "r"})
	store := fakeVarStore{
		vars: map[uint64][]model.VariableValue{9: {{Name: "datei", Kind: model.VarString, Text: "dn: uid=a,dc=x\ncn: A\n"}}},
		ei:   map[uint64]model.ElementInstanceValue{1: {FlowScopeKey: 9}},
	}
	if _, err := Resolve(store, cp, d, 1); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
}

func TestRunErrors(t *testing.T) {
	if _, err := Run(Job{Format: "csv", Result: "r"}); err == nil {
		t.Error("an unknown format must fail")
	}
	if _, err := Run(Job{Format: FormatLDIF}); err == nil {
		t.Error("a job with no result variable must fail")
	}
	if _, err := Run(Job{Format: FormatLDIF, Result: "r", Source: "  "}); err == nil {
		t.Error("an empty source must fail")
	}
	if _, err := Run(Job{Format: FormatLDIF, Result: "r", Source: "nonsense"}); err == nil {
		t.Error("an unparseable file must fail")
	}
	if _, err := Run(Job{Format: FormatLDIF, Operation: OperationWrite, Result: "r", Source: "{}"}); err == nil {
		t.Error("entries that are not the {dn, attributes} array must fail")
	}
	if _, err := Run(Job{Format: FormatLDIF, Operation: OperationWrite, Result: "r", Source: "[]"}); err == nil {
		t.Error("writing no entries must fail")
	}
}

// fakeJobStore is the slice of the store the handler reads, plus the element
// instance that points at the task.
type fakeJobStore struct {
	fakeVarStore
	missing bool
	eiErr   error
}

func (f fakeJobStore) GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error) {
	if f.eiErr != nil {
		return nil, false, f.eiErr
	}
	if f.missing {
		return nil, false, nil
	}
	ei, ok := f.ei[key]
	if !ok {
		return nil, false, nil
	}
	return &ei, true, nil
}

// The in-process handler runs the same Resolve/Run pair a worker does and turns the
// result into process variables.
func TestHandler(t *testing.T) {
	b := compiler.NewBuilder(7, "p", 1)
	start := b.AddStartEvent()
	el := b.AddLdifConnectorTask(compiler.LdifConfig{
		Format: FormatLDIF, Source: "datei", Result: "eintraege",
	})
	b.Connect(start, el)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	store := fakeJobStore{fakeVarStore: fakeVarStore{
		vars: map[uint64][]model.VariableValue{
			5: {{Name: "datei", Kind: model.VarString, Text: "dn: uid=ada,dc=x\ncn: Ada\n"}},
		},
		ei: map[uint64]model.ElementInstanceValue{5: {ProcessDefKey: 7, ElementId: el}},
	}}
	h := Handler(store, func(uint64) *compiler.CompiledProcess { return cp })

	vars, err := h(job.Job{ElementInstanceKey: 5})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(vars) != 2 {
		t.Fatalf("variables = %+v, want the entries and the count", vars)
	}
	if vars[0].Name != "eintraege" || vars[0].Kind != model.VarJSON {
		t.Errorf("result variable = %+v", vars[0])
	}
	if !strings.Contains(vars[0].Text, `"uid=ada,dc=x"`) {
		t.Errorf("entries = %s", vars[0].Text)
	}
	if vars[1].Name != "entryCount" || vars[1].Text != "1" {
		t.Errorf("count variable = %+v", vars[1])
	}
}

// A write completes with the rendered file as text.
func TestHandlerWrite(t *testing.T) {
	b := compiler.NewBuilder(7, "p", 1)
	start := b.AddStartEvent()
	el := b.AddLdifConnectorTask(compiler.LdifConfig{
		Format: FormatDSML, Operation: OperationWrite, Source: "eintraege", Result: "datei",
	})
	b.Connect(start, el)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	store := fakeJobStore{fakeVarStore: fakeVarStore{
		vars: map[uint64][]model.VariableValue{
			5: {{Name: "eintraege", Kind: model.VarJSON, Text: `[{"dn":"uid=a,dc=x","attributes":{"cn":["A"]}}]`}},
		},
		ei: map[uint64]model.ElementInstanceValue{5: {ProcessDefKey: 7, ElementId: el}},
	}}
	vars, err := Handler(store, func(uint64) *compiler.CompiledProcess { return cp })(job.Job{ElementInstanceKey: 5})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if vars[0].Kind != model.VarString || !strings.Contains(vars[0].Text, "<dsml:entry") {
		t.Errorf("written file = %+v", vars[0])
	}
}

func TestHandlerErrors(t *testing.T) {
	b := compiler.NewBuilder(7, "p", 1)
	start := b.AddStartEvent()
	el := b.AddLdifConnectorTask(compiler.LdifConfig{Format: FormatLDIF, Source: "datei", Result: "r"})
	b.Connect(start, el)
	cp, _ := b.Build()
	live := fakeJobStore{fakeVarStore: fakeVarStore{
		ei: map[uint64]model.ElementInstanceValue{5: {ProcessDefKey: 7, ElementId: el}},
	}}

	// An element instance that is gone (already completed) is nothing to do.
	gone := fakeJobStore{missing: true}
	if vars, err := Handler(gone, func(uint64) *compiler.CompiledProcess { return cp })(job.Job{ElementInstanceKey: 5}); err != nil || vars != nil {
		t.Errorf("a gone element instance = %v, %v; want nil, nil", vars, err)
	}
	if _, err := Handler(fakeJobStore{eiErr: errTest}, func(uint64) *compiler.CompiledProcess { return cp })(job.Job{ElementInstanceKey: 5}); err == nil {
		t.Error("a store error must surface")
	}
	if _, err := Handler(live, func(uint64) *compiler.CompiledProcess { return nil })(job.Job{ElementInstanceKey: 5}); err == nil {
		t.Error("a missing compiled process must fail")
	}
	// The source variable is not set, so Resolve fails.
	if _, err := Handler(live, func(uint64) *compiler.CompiledProcess { return cp })(job.Job{ElementInstanceKey: 5}); err == nil {
		t.Error("a missing source variable must fail the job")
	}
}

// An element that is not a task at all is reported rather than run.
func TestHandlerNonConnectorTask(t *testing.T) {
	b := compiler.NewBuilder(7, "p", 1)
	start := b.AddStartEvent()
	b.Connect(start, b.AddEndEvent())
	cp, _ := b.Build()
	store := fakeJobStore{fakeVarStore: fakeVarStore{
		ei: map[uint64]model.ElementInstanceValue{5: {ProcessDefKey: 7, ElementId: start}},
	}}
	if _, err := Handler(store, func(uint64) *compiler.CompiledProcess { return cp })(job.Job{ElementInstanceKey: 5}); err == nil {
		t.Error("a non-connector element must fail rather than run")
	}
}

// Invalid UTF-8 is encoded rather than emitted raw, so the file stays readable.
func TestIsASCIIRejectsInvalidUTF8(t *testing.T) {
	if !needsBase64(string([]byte{0xff, 0xfe})) {
		t.Error("invalid UTF-8 must be encoded")
	}
}
