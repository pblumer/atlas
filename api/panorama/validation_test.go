package panorama

import (
	"os"
	"strings"
	"testing"
)

func minimalModel(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/minimal.archimate.xml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}

func TestValidateAcceptsOpenExchangeModel(t *testing.T) {
	result := Validate(minimalModel(t))
	if !result.Valid {
		t.Fatalf("Validate: problems = %#v", result.Problems)
	}
	if result.Notation != NotationArchiMate32 {
		t.Errorf("notation = %q, want %q", result.Notation, NotationArchiMate32)
	}
	if result.Namespace != ExchangeNamespace {
		t.Errorf("namespace = %q, want %q", result.Namespace, ExchangeNamespace)
	}
	if result.ModelIdentifier != "model-panorama-minimal" || result.Name != "Minimal Panorama Model" {
		t.Errorf("identity = (%q, %q), want fixture identity", result.ModelIdentifier, result.Name)
	}
	if result.Elements != 2 || result.Relationships != 1 || result.Views != 1 {
		t.Errorf("counts = elements:%d relationships:%d views:%d, want 2/1/1",
			result.Elements, result.Relationships, result.Views)
	}
}

func TestValidateRejectsUnsafeOrInvalidExchangeContent(t *testing.T) {
	valid := string(minimalModel(t))
	tests := []struct {
		name string
		xml  string
		want string
	}{
		{
			name: "document type",
			xml:  `<!DOCTYPE model [<!ENTITY xxe SYSTEM "file:///etc/passwd">]><model xmlns="` + ExchangeNamespace + `" identifier="m"><name>&xxe;</name></model>`,
			// The refusal names what it is refusing and why, because "not allowed"
			// left whoever is holding the file with nothing to do about it.
			want: "a DOCTYPE can name content outside the document",
		},
		{
			name: "wrong namespace",
			xml:  strings.Replace(valid, ExchangeNamespace, "https://example.invalid/archimate", 1),
			want: "This is not an ArchiMate Open Exchange document",
		},
		{
			name: "unknown element type",
			xml:  strings.Replace(valid, `xsi:type="ApplicationComponent"`, `xsi:type="AtlasApplication"`, 1),
			want: `unknown ArchiMate element type "AtlasApplication"`,
		},
		{
			name: "duplicate identifier",
			xml:  strings.Replace(valid, `identifier="service-workflow"`, `identifier="application-atlas"`, 1),
			want: `duplicate identifier "application-atlas"`,
		},
		{
			name: "missing relationship target",
			xml:  strings.Replace(valid, `target="service-workflow"`, `target="missing"`, 1),
			want: `target "missing" does not exist`,
		},
		{
			name: "relationship endpoint is a diagram object",
			xml:  strings.Replace(valid, `source="application-atlas"`, `source="node-atlas"`, 1),
			want: `source "node-atlas" is not an ArchiMate concept`,
		},
		{
			name: "missing view element",
			xml:  strings.Replace(valid, `elementRef="service-workflow"`, `elementRef="missing"`, 1),
			want: `elementRef "missing" does not exist`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := Validate([]byte(tc.xml))
			if result.Valid {
				t.Fatal("Validate reported valid, want rejection")
			}
			if !problemsContain(result.Problems, tc.want) {
				t.Fatalf("problems = %#v, want one containing %q", result.Problems, tc.want)
			}
		})
	}
}

func TestValidateRejectsMissingModelIdentityAndExcessiveDepth(t *testing.T) {
	missing := `<model xmlns="` + ExchangeNamespace + `"><name>Unnamed identity</name></model>`
	result := Validate([]byte(missing))
	if result.Valid || !problemsContain(result.Problems, "model identifier is required") {
		t.Fatalf("missing identifier problems = %#v", result.Problems)
	}

	deep := `<model xmlns="` + ExchangeNamespace + `" identifier="m"><name>Deep</name>` +
		strings.Repeat("<x>", maxXMLDepth+1) + strings.Repeat("</x>", maxXMLDepth+1) + `</model>`
	result = Validate([]byte(deep))
	if result.Valid || !problemsContain(result.Problems, "maximum XML depth") {
		t.Fatalf("deep model problems = %#v", result.Problems)
	}
}

func problemsContain(problems []Problem, want string) bool {
	for _, p := range problems {
		if strings.Contains(p.Message, want) {
			return true
		}
	}
	return false
}

// TestValidateNamesWhatTheDocumentActuallyIs.
//
// Every one of these refusals was already correct — none of them is an ArchiMate
// Open Exchange document — and every one of them was useless. The message was
// written in Clark notation for whoever wrote the parser: two namespace URIs, no
// name for the thing that had actually been handed over, and no remedy. Three of
// them were worse than useless, because they rendered as "model; expected model"
// and read like a fault in Atlas rather than a fact about the file.
//
// So the assertions are about the sentence, which is the deliverable here.
func TestValidateNamesWhatTheDocumentActuallyIs(t *testing.T) {
	tests := []struct {
		name string
		xml  string
		want []string
	}{
		{
			name: "a modelling tool's own spreadsheet export",
			xml:  `<?xml version="1.0"?><XmlFile xmlns="http://www.mid.de/spec/Excel"><Sheet/></XmlFile>`,
			want: []string{"MID", "Model Exchange File Format"},
		},
		{
			// Two nearly identical URIs, and the reader was left to spot which.
			name: "the previous version of the exchange format",
			xml:  `<?xml version="1.0"?><model xmlns="http://www.opengroup.org/xsd/archimate" identifier="m"/>`,
			want: []string{"ArchiMate 2.x", "3.x"},
		},
		{
			// The likeliest mistake an ArchiMate user makes, and the file picker
			// even offers .archimate — so the message has to carry the export path.
			name: "Archi's own save format",
			xml:  `<?xml version="1.0"?><archimate:model xmlns:archimate="http://www.archimatetool.com/archimate" name="X"/>`,
			want: []string{"Archi's own model file", "Export"},
		},
		{
			// This one rendered as "{}model; expected {…}model".
			name: "an exchange file that lost its namespace",
			xml:  `<?xml version="1.0"?><model identifier="m"><name>X</name></model>`,
			want: []string{"declares no XML namespace", ExchangeNamespace},
		},
		{
			name: "a process model",
			xml:  `<?xml version="1.0"?><definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"/>`,
			want: []string{"BPMN", "Modeler"},
		},
		{
			name: "a raw XMI export",
			xml:  `<?xml version="1.0"?><xmi:XMI xmlns:xmi="http://www.omg.org/spec/XMI/20131001"/>`,
			want: []string{"XMI", "Model Exchange File Format"},
		},
		{
			name: "a saved web page",
			xml:  `<!doctype html><html><body>Sign in</body></html>`,
			want: []string{"HTML page"},
		},
		{
			// Not in the list, so the message says what Atlas does read rather than
			// guessing at what this is.
			name: "something nobody anticipated",
			xml:  `<?xml version="1.0"?><catalogue xmlns="urn:acme:stuff"/>`,
			want: []string{"Model Exchange File Format", `"catalogue"`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := Validate([]byte(tc.xml))
			if result.Valid {
				t.Fatal("Valid = true; this document is not an Open Exchange model")
			}
			// One problem, not a cascade. Everything after the root check is about a
			// model, and this document is not one: "model identifier is required"
			// against a spreadsheet is noise stacked on the fact worth reading.
			if len(result.Problems) != 1 {
				t.Fatalf("problems = %d, want exactly one: %#v", len(result.Problems), result.Problems)
			}
			message := result.Problems[0].Message
			for _, want := range tc.want {
				if !strings.Contains(message, want) {
					t.Errorf("message %q does not mention %q", message, want)
				}
			}
		})
	}
}

// TestValidateNamesAnEncodingItCannotRead. The decoder used to fail with
// "encoding \"UTF-16\" declared but Decoder.CharsetReader is nil", which tells an
// operator about a field on a Go struct.
//
// The refusal itself stays: Panorama stores and re-exports a document byte-for-byte
// (ADR-0189 §2) and its writers splice into those bytes, so a UTF-16 document
// accepted at the gate would be corrupted at the first shape somebody moved.
func TestValidateNamesAnEncodingItCannotRead(t *testing.T) {
	result := Validate([]byte(`<?xml version="1.0" encoding="UTF-16"?><model xmlns="` +
		ExchangeNamespace + `" identifier="m"><name>X</name></model>`))

	if result.Valid {
		t.Fatal("Valid = true")
	}
	if len(result.Problems) != 1 {
		t.Fatalf("problems = %d, want exactly one: %#v", len(result.Problems), result.Problems)
	}
	message := result.Problems[0].Message
	for _, want := range []string{"UTF-8", `"UTF-16"`, "Re-save"} {
		if !strings.Contains(message, want) {
			t.Errorf("message %q does not mention %q", message, want)
		}
	}
	if strings.Contains(message, "CharsetReader") {
		t.Errorf("message %q leaks the standard library's internals", message)
	}
}
