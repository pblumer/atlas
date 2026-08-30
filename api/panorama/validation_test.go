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
			want: "directives are not allowed",
		},
		{
			name: "wrong namespace",
			xml:  strings.Replace(valid, ExchangeNamespace, "https://example.invalid/archimate", 1),
			want: "unsupported root element",
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
