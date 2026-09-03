package csvimport

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

func cols(specs ...Column) Config { return Config{Columns: specs} }

// A fixed-width file finds each field by position — how a mainframe HR extract
// arrives, and MIM's Fixed-Width text file agent.
func TestParseFixedWidth(t *testing.T) {
	cfg := cols(
		Column{Name: "personalnr", Width: 8},
		Column{Name: "name", Width: 12},
		Column{Name: "abteilung", Width: 6},
	)
	cfg.Format = FormatFixedWidth
	no := false
	cfg.HasHeader = &no

	rows, err := Parse(cfg, []byte("00012345Müller      IT    \n00067890Meier\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	// Runes, not bytes: the umlaut must not shift the columns after it. Counted in
	// bytes, "Müller      " would end one position early and "abteilung" would come
	// back as " IT   ".
	if rows[0]["personalnr"] != "00012345" || rows[0]["name"] != "Müller" || rows[0]["abteilung"] != "IT" {
		t.Errorf("row 0 = %v", rows[0])
	}
	// A line ending before the layout does yields empty cells rather than an error:
	// a ragged export is dirty data, which this worker passes through.
	if rows[1]["name"] != "Meier" || rows[1]["abteilung"] != "" {
		t.Errorf("row 1 = %v, want empty cells past the end of a short line", rows[1])
	}
}

// A header row in a fixed-width file carries no information the layout does not
// already have, so it is skipped rather than read.
func TestParseFixedWidthSkipsAHeader(t *testing.T) {
	cfg := cols(Column{Name: "a", Width: 3}, Column{Name: "b", Width: 3})
	cfg.Format = FormatFixedWidth
	rows, err := Parse(cfg, []byte("a  b  \n123456\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(rows) != 1 || rows[0]["a"] != "123" {
		t.Errorf("rows = %v, want the header skipped", rows)
	}
}

func TestParseFixedWidthErrors(t *testing.T) {
	noCols := Config{Format: FormatFixedWidth}
	if _, err := Parse(noCols, []byte("abc")); err == nil {
		t.Error("a fixed-width file with no layout must fail")
	}
	bad := cols(Column{Name: "a"})
	bad.Format = FormatFixedWidth
	if _, err := Parse(bad, []byte("abc")); err == nil {
		t.Error("a fixed-width column with no width must fail")
	}
	empty := cols(Column{Name: "a", Width: 2})
	empty.Format = FormatFixedWidth
	if _, err := Parse(empty, []byte("   \n")); err == nil {
		t.Error("an empty file must fail rather than yield no rows")
	}
}

// An attribute-value file is blank-line-separated blocks of "name: value".
func TestParseAVP(t *testing.T) {
	const data = "# ein Kommentar\nuid: ada\nmail: ada@example.com\n\nuid: bob\nmail: bob@example.com\nmail: zweit@example.com\n"
	rows, err := Parse(Config{Format: FormatAVP}, []byte(data))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0]["uid"] != "ada" || rows[0]["mail"] != "ada@example.com" {
		t.Errorf("row 0 = %v", rows[0])
	}
	// A repeated key keeps the first value, as a duplicate CSV header does — a row
	// object holds one value per field, and taking the last would make the result
	// depend on how far down the file the duplicate appeared.
	if rows[1]["mail"] != "bob@example.com" {
		t.Errorf("row 1 mail = %v, want the first value", rows[1]["mail"])
	}
}

// With a layout the named fields are picked out and the rest of the file ignored.
func TestParseAVPWithALayout(t *testing.T) {
	cfg := cols(Column{Name: "benutzer", Header: "uid"}, Column{Name: "aktiv", Type: "boolean"})
	cfg.Format = FormatAVP
	rows, err := Parse(cfg, []byte("uid: ada\naktiv: true\nignoriert: x\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if rows[0]["benutzer"] != "ada" || rows[0]["aktiv"] != true {
		t.Errorf("row = %v", rows[0])
	}
	if _, present := rows[0]["ignoriert"]; present {
		t.Errorf("row = %v, want only the configured fields", rows[0])
	}
}

func TestParseAVPErrors(t *testing.T) {
	if _, err := Parse(Config{Format: FormatAVP}, []byte("kein separator\n")); err == nil {
		t.Error("a line with no separator must fail")
	}
	if _, err := Parse(Config{Format: FormatAVP}, []byte("\n\n")); err == nil {
		t.Error("a file with no records must fail rather than yield none")
	}
}

func TestParseUnknownFormat(t *testing.T) {
	if _, err := Parse(Config{Format: "yaml"}, []byte("x")); err == nil {
		t.Error("an unknown format must fail")
	}
	if _, err := Render(Config{Format: "yaml"}, []map[string]any{{"a": 1}}); err == nil {
		t.Error("an unknown format must fail on write too")
	}
}

// Write is the inverse of read: what Render produces, Parse reads back.
func TestRenderRoundTrips(t *testing.T) {
	rows := []map[string]any{
		{"uid": "ada", "mail": "ada@example.com"},
		{"uid": "bob", "mail": "bob@example.com"},
	}
	for _, tc := range []struct{ format string }{{FormatCSV}, {FormatAVP}} {
		t.Run(tc.format, func(t *testing.T) {
			cfg := cols(Column{Name: "uid"}, Column{Name: "mail"})
			cfg.Format = tc.format
			out, err := Render(cfg, rows)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			back, err := Parse(cfg, []byte(out))
			if err != nil {
				t.Fatalf("Parse back: %v (%q)", err, out)
			}
			if len(back) != 2 || back[0]["uid"] != "ada" || back[1]["mail"] != "bob@example.com" {
				t.Errorf("round trip = %v, from %q", back, out)
			}
		})
	}
}

func TestRenderFixedWidthRoundTrips(t *testing.T) {
	cfg := cols(Column{Name: "uid", Width: 6}, Column{Name: "mail", Width: 20})
	cfg.Format = FormatFixedWidth
	out, err := Render(cfg, []map[string]any{{"uid": "ada", "mail": "ada@example.com"}})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	back, err := Parse(cfg, []byte(out))
	if err != nil {
		t.Fatalf("Parse back: %v (%q)", err, out)
	}
	if back[0]["uid"] != "ada" || back[0]["mail"] != "ada@example.com" {
		t.Errorf("round trip = %v, from %q", back, out)
	}
}

// A value wider than its column is cut. It is the one place this worker loses
// data on purpose, because the format has no way to express a wider field.
func TestRenderFixedWidthTruncates(t *testing.T) {
	cfg := cols(Column{Name: "uid", Width: 3})
	cfg.Format = FormatFixedWidth
	no := false
	cfg.HasHeader = &no
	out, err := Render(cfg, []map[string]any{{"uid": "abcdefgh"}})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out != "abc\n" {
		t.Errorf("out = %q, want the value cut to the column width", out)
	}
}

// Without a layout the output's field order is every field the rows carry, sorted,
// so a file written twice from the same data is the same file.
func TestRenderDerivesAStableColumnOrder(t *testing.T) {
	rows := []map[string]any{{"z": 1, "a": 2, "m": 3}}
	out, err := Render(Config{}, rows)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.HasPrefix(out, "a,m,z\n") {
		t.Errorf("out = %q, want the derived header sorted", out)
	}
}

func TestRenderNothingToWrite(t *testing.T) {
	if _, err := Render(Config{}, nil); err == nil {
		t.Error("writing with no columns and no rows must fail rather than produce an empty file")
	}
}

// A row value keeps the form a file should hold: an integral number does not acquire
// a float's decimals, and a missing field is empty rather than "<nil>".
func TestCellText(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want string
	}{
		{nil, ""},
		{"x", "x"},
		{true, "true"},
		{false, "false"},
		{float64(42), "42"},
		{1.5, "1.5"},
		{int64(7), "7"},
	} {
		if got := cellText(tc.in); got != tc.want {
			t.Errorf("cellText(%#v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestKnownFormat(t *testing.T) {
	for _, f := range append(Formats(), "") {
		if !KnownFormat(f) {
			t.Errorf("KnownFormat(%q) = false", f)
		}
	}
	if KnownFormat("ldif") {
		t.Error("LDIF is a directory format, not a text table; it belongs to its own worker")
	}
}

// TestCsvFormatsMatchTheConnector is the drift guard between this package's format
// list and the compiler's own copy of it.
//
// The compiler cannot import this package — connector/csvimport imports compiler, so
// the dependency only runs one way — which is why the names exist twice. The check is
// behavioural: every format this package handles must compile, and one it does not
// must be refused. Two lists that disagree fail here rather than in a process that
// deploys and then cannot run.
func TestCsvFormatsMatchTheConnector(t *testing.T) {
	for _, f := range Formats() {
		attrs := `source="datei" format="` + f + `" resultVariable="r"`
		if f == FormatFixedWidth {
			attrs += ` columns="a:5"`
		}
		if _, err := compiler.Parse(1, 1, strings.NewReader(csvBPMN(attrs))); err != nil {
			t.Errorf("the compiler rejects format %q, which this package handles: %v", f, err)
		}
	}
	if _, err := compiler.Parse(1, 1, strings.NewReader(csvBPMN(`source="d" format="ldif" resultVariable="r"`))); err == nil {
		t.Error("the compiler accepted a format this package does not handle")
	}
	// Both directions, likewise.
	for _, op := range []string{OperationRead, OperationWrite} {
		if _, err := compiler.Parse(1, 1, strings.NewReader(csvBPMN(`source="d" operation="`+op+`" resultVariable="r"`))); err != nil {
			t.Errorf("the compiler rejects operation %q: %v", op, err)
		}
	}
}

func csvBPMN(attrs string) string {
	return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:csvConnector ` + attrs + `/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}
