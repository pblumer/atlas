package api

import (
	"strings"
	"testing"
)

// A model whose Atlas extension elements sit in a namespace other than Atlas' own
// deploys and runs — the compiler matches an extension element on its local name
// alone — but the Modeler cannot read it back. The deploy is where somebody is
// looking, so that is where it is said.
func TestForeignAtlasNamespaceIsWarnedAboutAtDeploy(t *testing.T) {
	warnings := foreignAtlasNamespaceWarnings([]byte(modelWithAtlasNamespace("http://atlas.dev/schema/1.0")))
	if len(warnings) != 1 {
		t.Fatalf("got %d warnings, want 1: %q", len(warnings), warnings)
	}
	for _, want := range []string{
		"discordConnector",            // which element
		"http://atlas.dev/schema/1.0", // the namespace the model used
		atlasModdleNamespace(t),       // the one it should have used
		"xmlns:atlas",                 // where to fix it
	} {
		if !strings.Contains(warnings[0], want) {
			t.Errorf("warning does not mention %q:\n%s", want, warnings[0])
		}
	}
}

// The canonical namespace is the whole point of the check: it must not warn about
// the models the Modeler itself writes.
func TestCanonicalAtlasNamespaceIsNotWarnedAbout(t *testing.T) {
	if got := foreignAtlasNamespaceWarnings([]byte(modelWithAtlasNamespace(atlasModdleNamespace(t)))); len(got) != 0 {
		t.Errorf("the canonical namespace warned: %q", got)
	}
}

// Zeebe's extension elements are read from <extensionElements> too, and they are
// not Atlas'. Flagging them would make the check noise on every Modeler model.
func TestZeebeExtensionElementsAreNotAtlasElements(t *testing.T) {
	const model = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="work"/>
        <zeebe:ioMapping/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
  </bpmn:process>
</bpmn:definitions>`
	if got := foreignAtlasNamespaceWarnings([]byte(model)); len(got) != 0 {
		t.Errorf("a Zeebe extension element was reported as a foreign-namespace Atlas one: %q", got)
	}
}

// One warning per namespace, not per element: the prefix is bound once at the
// document root, so every element shares the same mistake and repeating it per
// task would bury the fix.
func TestOneWarningPerNamespaceListingEveryElement(t *testing.T) {
	// The prefix is deliberately not "atlas": what makes an element Atlas' is the
	// namespace it resolves to, never the prefix spelling, and writing it this way
	// keeps this fixture from reading as a model anyone should copy.
	const model = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:x="http://atlas.dev/schema/1.0">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t1">
      <bpmn:extensionElements>
        <x:jiraConnector connector="j" operation="create-issue"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:serviceTask id="t2">
      <bpmn:extensionElements>
        <x:discordConnector connector="d" operation="send-message"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:serviceTask id="t3">
      <bpmn:extensionElements>
        <x:discordConnector connector="d" operation="send-message"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
  </bpmn:process>
</bpmn:definitions>`
	warnings := foreignAtlasNamespaceWarnings([]byte(model))
	if len(warnings) != 1 {
		t.Fatalf("got %d warnings, want 1: %q", len(warnings), warnings)
	}
	// Both distinct names, each once, in a stable order.
	if got, want := warnings[0], "discordConnector, jiraConnector"; !strings.Contains(got, want) {
		t.Errorf("want the element names listed once each as %q:\n%s", want, got)
	}
}

// Two different foreign namespaces in one model are two different mistakes.
func TestEachForeignNamespaceIsReportedOnce(t *testing.T) {
	const model = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:a="http://atlas.dev/schema/1.0"
                  xmlns:b="http://atlas.dev/schema/1.0/bpmn">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t1">
      <bpmn:extensionElements><a:discordConnector connector="d" operation="send-message"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:serviceTask id="t2">
      <bpmn:extensionElements><b:jiraConnector connector="j" operation="create-issue"/></bpmn:extensionElements>
    </bpmn:serviceTask>
  </bpmn:process>
</bpmn:definitions>`
	warnings := foreignAtlasNamespaceWarnings([]byte(model))
	if len(warnings) != 2 {
		t.Fatalf("got %d warnings, want 2: %q", len(warnings), warnings)
	}
	// Sorted by namespace, so a deploy response reads the same way twice.
	if !strings.Contains(warnings[0], "http://atlas.dev/schema/1.0\"") {
		t.Errorf("first warning is not the first namespace in order:\n%s", warnings[0])
	}
	if !strings.Contains(warnings[1], "http://atlas.dev/schema/1.0/bpmn") {
		t.Errorf("second warning is not the second namespace in order:\n%s", warnings[1])
	}
}

// The check is best-effort metadata on a deploy that already succeeded: a document
// the compiler let through but that ends mid-token must not panic or fail it.
func TestMalformedModelYieldsNoWarningRatherThanAFailure(t *testing.T) {
	if got := foreignAtlasNamespaceWarnings([]byte(`<bpmn:definitions><bpmn:process`)); len(got) != 0 {
		t.Errorf("a truncated document produced warnings: %q", got)
	}
	if got := foreignAtlasNamespaceWarnings(nil); len(got) != 0 {
		t.Errorf("an empty document produced warnings: %q", got)
	}
}

// The element names and the namespace both come from the moddle the Modeler
// itself loads, so the check cannot drift from what the Modeler accepts. This
// pins that derivation rather than a copy of it: rename the moddle's uri, or add
// a type to it, and the check follows without anyone editing a list.
func TestTheCheckIsDerivedFromTheModelersOwnModdle(t *testing.T) {
	ns, names, err := atlasModdle()
	if err != nil {
		t.Fatalf("read the moddle: %v", err)
	}
	if ns == "" {
		t.Fatal("the moddle declares no uri")
	}
	// tagAlias lowerCase: DiscordConnector is written <atlas:discordConnector>.
	for _, want := range []string{"discordConnector", "jiraConnector", "startForm", "agentParam"} {
		if !names[want] {
			t.Errorf("the moddle-derived element names lack %q; got %d names", want, len(names))
		}
	}
	// A Zeebe element must not be in the Atlas set, or the check would flag every
	// Modeler-authored model.
	for _, notWant := range []string{"taskDefinition", "ioMapping", "calledElement"} {
		if names[notWant] {
			t.Errorf("%q is Zeebe's, but the moddle-derived set claims it for Atlas", notWant)
		}
	}
}

// atlasModdleNamespace is the canonical namespace, read from the moddle so the
// tests above never hard-code a second copy of it.
func atlasModdleNamespace(t *testing.T) string {
	t.Helper()
	ns, _, err := atlasModdle()
	if err != nil {
		t.Fatalf("read the moddle: %v", err)
	}
	return ns
}

// modelWithAtlasNamespace is one Discord service task in whichever namespace the
// caller wants to bind the atlas prefix to.
func modelWithAtlasNamespace(ns string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="` + ns + `">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t" name="Testnachricht senden">
      <bpmn:extensionElements>
        <atlas:discordConnector connector="d" operation="send-message" channel="=c" content="=x"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
  </bpmn:process>
</bpmn:definitions>`
}
