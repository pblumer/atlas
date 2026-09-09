package api

import (
	"strings"
	"testing"
)

// A searchable declaration is accepted by the deploy whatever it names, and a name
// nothing writes then answers an empty search forever with nothing saying why. These
// cover the two readings the deploy can honestly give: one settled by the model's own
// declaration, one gated on the model having stated its inputs at all.
func TestSearchableDeclarationWarnings(t *testing.T) {
	const head = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	                xmlns:atlas="http://atlas/schema/1.0"
	                xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">`
	for _, tc := range []struct {
		name  string
		xml   string
		wants []string // substrings, one per expected warning, in order
	}{
		{
			name: "a name the model declares as json can never be indexed",
			xml: head + `<process id="identitaet" atlas:searchable="payload">
			  <extensionElements><atlas:startForm>
			    <atlas:startVariable name="payload" type="json"/>
			  </atlas:startForm></extensionElements>
			</process></definitions>`,
			wants: []string{`declares "payload" searchable, but declares it as json`},
		},
		{
			name: "a name nothing writes, where the model has stated its inputs",
			xml: head + `<process id="identitaet" atlas:searchable="identityId,itme">
			  <extensionElements><atlas:startForm>
			    <atlas:startVariable name="identityId" type="string"/>
			  </atlas:startForm></extensionElements>
			</process></definitions>`,
			wants: []string{`declares "itme" searchable, but nothing in the model produces that name`},
		},
		{
			name: "an output mapping counts as producing the name",
			xml: head + `<process id="identitaet" atlas:searchable="identityId,row">
			  <extensionElements><atlas:startForm>
			    <atlas:startVariable name="identityId" type="string"/>
			  </atlas:startForm></extensionElements>
			  <serviceTask id="t"><extensionElements><zeebe:ioMapping>
			    <zeebe:output source="=x" target="row"/>
			  </zeebe:ioMapping></extensionElements></serviceTask>
			</process></definitions>`,
		},
		{
			name: "so does a result variable, whatever writes it",
			xml: head + `<process id="identitaet" atlas:searchable="identityId,konto">
			  <extensionElements><atlas:startForm>
			    <atlas:startVariable name="identityId" type="string"/>
			  </atlas:startForm></extensionElements>
			  <serviceTask id="t"><extensionElements>
			    <atlas:entraConnector operation="read-user" resultVariable="konto"/>
			  </extensionElements></serviceTask>
			</process></definitions>`,
		},
		{
			name: "a model that states no inputs is not second-guessed",
			xml: head + `<process id="identitaet" atlas:searchable="identityId">
			  <startEvent id="s"/>
			</process></definitions>`,
		},
		{
			name: "nor is one whose inputs come from a form this cannot read",
			xml: head + `<process id="identitaet" atlas:searchable="identityId,kundennummer">
			  <extensionElements><atlas:startForm>
			    <atlas:startVariable name="identityId" type="string"/>
			  </atlas:startForm></extensionElements>
			  <startEvent id="s"><extensionElements>
			    <zeebe:formDefinition formId="antrag"/>
			  </extensionElements></startEvent>
			</process></definitions>`,
		},
		{
			name: "a process that declares nothing searchable is never looked at",
			xml: head + `<process id="identitaet">
			  <extensionElements><atlas:startForm>
			    <atlas:startVariable name="payload" type="json"/>
			  </atlas:startForm></extensionElements>
			</process></definitions>`,
		},
		{
			name: "the declaration is per process, and so is the reading",
			xml: head + `<collaboration id="c"/>
			<process id="vertrieb" atlas:searchable="payload">
			  <extensionElements><atlas:startForm>
			    <atlas:startVariable name="payload" type="json"/>
			  </atlas:startForm></extensionElements>
			</process>
			<process id="bank" atlas:searchable="identityId">
			  <extensionElements><atlas:startForm>
			    <atlas:startVariable name="identityId" type="string"/>
			  </atlas:startForm></extensionElements>
			</process></definitions>`,
			wants: []string{`process "vertrieb" declares "payload" searchable`},
		},
		{
			name: "a repeated name is the compiler's refusal, not explained twice here",
			xml: head + `<process id="identitaet" atlas:searchable="itme,itme">
			  <extensionElements><atlas:startForm>
			    <atlas:startVariable name="identityId" type="string"/>
			  </atlas:startForm></extensionElements>
			</process></definitions>`,
			wants: []string{`declares "itme" searchable, but nothing in the model produces that name`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := searchableDeclarationWarnings([]byte(tc.xml))
			if len(got) != len(tc.wants) {
				t.Fatalf("got %d warning(s), want %d:\n%s", len(got), len(tc.wants), strings.Join(got, "\n"))
			}
			for i, want := range tc.wants {
				if !strings.Contains(got[i], want) {
					t.Errorf("warning %d = %q, want it to contain %q", i, got[i], want)
				}
			}
		})
	}
}

// A document that ends mid-process still says what it said: the walk is best-effort
// metadata about a deploy that already succeeded, so it must not lose a finding to a
// token error — and must not panic on one either.
func TestSearchableDeclarationWarningsTruncated(t *testing.T) {
	const truncated = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	                     xmlns:atlas="http://atlas/schema/1.0">
	  <process id="identitaet" atlas:searchable="payload">
	    <extensionElements><atlas:startForm>
	      <atlas:startVariable name="payload" type="json"/>`
	got := searchableDeclarationWarnings([]byte(truncated))
	if len(got) != 1 || !strings.Contains(got[0], `declares it as json`) {
		t.Errorf("warnings = %v, want the finding the document had already made", got)
	}
	if len(searchableDeclarationWarnings(nil)) != 0 {
		t.Error("an empty document warned about something")
	}
}
