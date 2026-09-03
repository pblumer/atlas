package ldif

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// DSML in the wild carries whichever namespace prefix its producer chose, so the
// reader matches on local names.
func TestParseDSML(t *testing.T) {
	for _, prefix := range []string{"dsml:", "ds:", ""} {
		t.Run("prefix "+prefix, func(t *testing.T) {
			doc := `<?xml version="1.0"?>
<` + prefix + `dsml xmlns:dsml="http://www.dsml.org/DSML" xmlns:ds="http://www.dsml.org/DSML">
  <` + prefix + `directory-entries>
    <` + prefix + `entry dn="uid=ada,dc=example,dc=com">
      <` + prefix + `objectclass>
        <` + prefix + `oc-value>top</` + prefix + `oc-value>
        <` + prefix + `oc-value>person</` + prefix + `oc-value>
      </` + prefix + `objectclass>
      <` + prefix + `attr name="cn"><` + prefix + `value>Ada Lovelace</` + prefix + `value></` + prefix + `attr>
      <` + prefix + `attr name="mail">
        <` + prefix + `value>ada@example.com</` + prefix + `value>
        <` + prefix + `value>a.l@example.com</` + prefix + `value>
      </` + prefix + `attr>
    </` + prefix + `entry>
  </` + prefix + `directory-entries>
</` + prefix + `dsml>`
			got, err := Parse(FormatDSML, []byte(doc))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if len(got) != 1 || got[0].DN != "uid=ada,dc=example,dc=com" {
				t.Fatalf("entries = %+v", got)
			}
			// DSML's dedicated objectclass element folds back into a normal attribute,
			// which is what makes the result the shape a directory read produces.
			if len(got[0].Attributes["objectClass"]) != 2 {
				t.Errorf("objectClass = %v, want both values", got[0].Attributes["objectClass"])
			}
			if len(got[0].Attributes["mail"]) != 2 {
				t.Errorf("mail = %v, want both values", got[0].Attributes["mail"])
			}
		})
	}
}

// A binary value is base64, marked on the value rather than on the attribute as LDIF
// marks it.
func TestParseDSMLBase64Value(t *testing.T) {
	const doc = `<dsml xmlns="http://www.dsml.org/DSML"><directory-entries>
  <entry dn="uid=a,dc=x"><attr name="cn"><value encoding="base64">TcO8bGxlcg==</value></attr></entry>
</directory-entries></dsml>`
	got, err := Parse(FormatDSML, []byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got[0].Attributes["cn"][0] != "Müller" {
		t.Errorf("cn = %q, want the decoded value", got[0].Attributes["cn"][0])
	}
}

func TestParseDSMLErrors(t *testing.T) {
	for _, tc := range []struct{ name, doc, want string }{
		{"not XML", "{nope}", "dsml:"},
		{"no entries", `<dsml><directory-entries></directory-entries></dsml>`, "no directory entries"},
		{"entry without a dn", `<dsml><directory-entries><entry><attr name="cn"><value>A</value></attr></entry></directory-entries></dsml>`, "no dn"},
		{"attribute without a name", `<dsml><directory-entries><entry dn="uid=a"><attr><value>A</value></attr></entry></directory-entries></dsml>`, "no name"},
		{"bad base64", `<dsml><directory-entries><entry dn="uid=a"><attr name="cn"><value encoding="base64">!!!</value></attr></entry></directory-entries></dsml>`, "does not decode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(FormatDSML, []byte(tc.doc))
			if err == nil {
				t.Fatalf("want an error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// What Render writes, Parse reads back — including the markup characters and the
// values that must be encoded.
func TestDSMLRoundTrip(t *testing.T) {
	in := []Entry{{
		DN: `uid=ada,ou=R&D,dc=example,dc=com`,
		Attributes: map[string][]string{
			"objectClass": {"top", "person"},
			"cn":          {"Ada & <Lovelace>"},
			"description": {"Müller", "schlicht"},
		},
	}}
	out, err := Render(FormatDSML, in)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	back, err := Parse(FormatDSML, []byte(out))
	if err != nil {
		t.Fatalf("Parse back: %v\n%s", err, out)
	}
	if back[0].DN != in[0].DN {
		t.Errorf("dn = %q, want %q\n%s", back[0].DN, in[0].DN, out)
	}
	if back[0].Attributes["cn"][0] != "Ada & <Lovelace>" {
		t.Errorf("cn = %q, want the markup characters intact", back[0].Attributes["cn"][0])
	}
	if len(back[0].Attributes["objectClass"]) != 2 {
		t.Errorf("objectClass = %v\n%s", back[0].Attributes["objectClass"], out)
	}
	if back[0].Attributes["description"][0] != "Müller" {
		t.Errorf("description = %v, want the encoded value back", back[0].Attributes["description"])
	}
}

// objectClass is written as DSML's own element rather than twice.
func TestRenderDSMLObjectClassOnce(t *testing.T) {
	out, err := Render(FormatDSML, []Entry{{DN: "uid=a,dc=x", Attributes: map[string][]string{"objectClass": {"top"}}}})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "<dsml:oc-value>top</dsml:oc-value>") {
		t.Errorf("out = %s, want the objectclass element", out)
	}
	if strings.Contains(out, `name="objectClass"`) {
		t.Errorf("objectClass was also written as a generic attribute:\n%s", out)
	}
}

// A file read as LDIF writes back as DSML and vice versa: both carry the same entry
// shape, which is the whole reason they are one worker.
func TestFormatsInterchange(t *testing.T) {
	const ldifIn = "dn: uid=ada,dc=x\nobjectClass: person\ncn: Ada\n"
	entries, err := Parse(FormatLDIF, []byte(ldifIn))
	if err != nil {
		t.Fatalf("Parse LDIF: %v", err)
	}
	asDSML, err := Render(FormatDSML, entries)
	if err != nil {
		t.Fatalf("Render DSML: %v", err)
	}
	back, err := Parse(FormatDSML, []byte(asDSML))
	if err != nil {
		t.Fatalf("Parse DSML: %v\n%s", err, asDSML)
	}
	if back[0].DN != "uid=ada,dc=x" || back[0].Attributes["cn"][0] != "Ada" ||
		back[0].Attributes["objectClass"][0] != "person" {
		t.Errorf("interchange lost data: %+v", back[0])
	}
}

// TestLdifFormatsMatchTheConnector is the drift guard between this package's format
// list and the compiler's copy of it. The compiler cannot import this package — the
// dependency runs the other way — so the check is behavioural: every format this
// package handles must compile, and one it does not must be refused.
func TestLdifFormatsMatchTheConnector(t *testing.T) {
	bpmn := func(attrs string) string {
		return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:ldifConnector ` + attrs + `/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	}
	for _, f := range Formats() {
		if _, err := compiler.Parse(1, 1, strings.NewReader(bpmn(`format="`+f+`" source="d" resultVariable="r"`))); err != nil {
			t.Errorf("the compiler rejects format %q, which this package handles: %v", f, err)
		}
	}
	if _, err := compiler.Parse(1, 1, strings.NewReader(bpmn(`format="csv" source="d" resultVariable="r"`))); err == nil {
		t.Error("the compiler accepted a format this package does not handle")
	}
	for _, op := range []string{OperationRead, OperationWrite} {
		if _, err := compiler.Parse(1, 1, strings.NewReader(bpmn(`format="ldif" operation="`+op+`" source="d" resultVariable="r"`))); err != nil {
			t.Errorf("the compiler rejects operation %q: %v", op, err)
		}
	}
}
