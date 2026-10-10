package compiler

import (
	"strings"
	"testing"
)

// wizardModel wraps process content in a model with the atlas and zeebe namespaces.
func wizardModel(content string) string {
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:atlas="http://atlas/schema/1.0"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">` + content + `</process>
	</definitions>`
}

// wizardAround puts inner flow content in a subprocess "w" carrying attrs, between a
// start and an end, with boundary as whatever is attached to the subprocess.
func wizardAround(attrs, inner, boundary string) string {
	return wizardModel(`
	    <startEvent id="s"/>
	    <subProcess id="w" ` + attrs + `>
	      <startEvent id="ws"/>
	      ` + inner + `
	    </subProcess>
	    ` + boundary + `
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="w"/>
	    <sequenceFlow id="f2" sourceRef="w" targetRef="e"/>`)
}

// timeout is the interrupting timer boundary that states when a sitting is abandoned.
const timeout = `<boundaryEvent id="abandoned" attachedToRef="w"><timerEventDefinition><timeDuration>PT30M</timeDuration></timerEventDefinition></boundaryEvent>
	    <endEvent id="ea"/>
	    <sequenceFlow id="fa" sourceRef="abandoned" targetRef="ea"/>`

// twoScreens is a wizard's inner flow: two user tasks one after the other.
const twoScreens = `<userTask id="one"/>
	      <userTask id="two"/>
	      <endEvent id="we"/>
	      <sequenceFlow id="w1" sourceRef="ws" targetRef="one"/>
	      <sequenceFlow id="w2" sourceRef="one" targetRef="two"/>
	      <sequenceFlow id="w3" sourceRef="two" targetRef="we"/>`

func mustParse(t *testing.T, xml string) *CompiledProcess {
	t.Helper()
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return cp
}

func node(t *testing.T, cp *CompiledProcess, bpmnID string) int32 {
	t.Helper()
	id, ok := cp.ElementIndexOf(bpmnID)
	if !ok {
		t.Fatalf("no element %q", bpmnID)
	}
	return id
}

// problemsOf runs the deploy's dry run, which reports errors and warnings alike.
func problemsOf(t *testing.T, xml string) []Problem {
	t.Helper()
	ps, err := ValidateModel(strings.NewReader(xml))
	if err != nil {
		t.Fatalf("ValidateModel: %v", err)
	}
	return ps
}

func findProblem(ps []Problem, rule, element string) *Problem {
	for i := range ps {
		if ps[i].Rule == rule && ps[i].Element == element {
			return &ps[i]
		}
	}
	return nil
}

// TestAWizardIsMarkedOnTheSubprocess: atlas:wizard compiles onto the subprocess node,
// and every element inside reaches it through the scope chain, through an ordinary
// subprocess in between too. Nothing outside does.
func TestAWizardIsMarkedOnTheSubprocess(t *testing.T) {
	for _, kind := range []WizardKind{WizardInternal, WizardPublic} {
		t.Run(kind.String(), func(t *testing.T) {
			cp := mustParse(t, wizardAround(`atlas:wizard="`+kind.String()+`"`, `
	      <subProcess id="inner">
	        <startEvent id="is"/>
	        <userTask id="deep"/>
	        <endEvent id="ie"/>
	        <sequenceFlow id="i1" sourceRef="is" targetRef="deep"/>
	        <sequenceFlow id="i2" sourceRef="deep" targetRef="ie"/>
	      </subProcess>
	      <userTask id="one"/>
	      <endEvent id="we"/>
	      <sequenceFlow id="w1" sourceRef="ws" targetRef="one"/>
	      <sequenceFlow id="w2" sourceRef="one" targetRef="inner"/>
	      <sequenceFlow id="w3" sourceRef="inner" targetRef="we"/>`, timeout))
			w := node(t, cp, "w")
			if got := cp.Wizard(w); got != kind {
				t.Fatalf("Wizard(w) = %v, want %v", got, kind)
			}
			for _, inside := range []string{"one", "inner", "deep", "we"} {
				if got := cp.EnclosingWizard(node(t, cp, inside)); got != w {
					t.Errorf("EnclosingWizard(%s) = %d, want the wizard %d", inside, got, w)
				}
			}
			for _, outside := range []string{"s", "w", "e", "abandoned"} {
				if got := cp.EnclosingWizard(node(t, cp, outside)); got != -1 {
					t.Errorf("EnclosingWizard(%s) = %d, want -1", outside, got)
				}
			}
			if got := cp.Wizard(node(t, cp, "inner")); got != WizardNone {
				t.Errorf("an ordinary subprocess inside the wizard is a wizard of its own: %v", got)
			}
		})
	}
}

// TestAWellFormedWizardIsQuiet: a wizard of user tasks with a timeout raises nothing,
// and a step of an internal wizard may be addressed like any task.
func TestAWellFormedWizardIsQuiet(t *testing.T) {
	for name, xml := range map[string]string{
		"public":                  wizardAround(`atlas:wizard="public"`, twoScreens, timeout),
		"internal, assigned step": wizardAround(`atlas:wizard="internal"`, strings.Replace(twoScreens, `<userTask id="two"/>`, `<userTask id="two"><extensionElements><zeebe:assignmentDefinition candidateGroups="hr"/></extensionElements></userTask>`, 1), timeout),
		"no mark at all":          wizardAround(``, twoScreens, ``),
	} {
		t.Run(name, func(t *testing.T) {
			for _, p := range problemsOf(t, xml) {
				if strings.HasPrefix(p.Rule, "wizard.") {
					t.Errorf("unexpected %v", p)
				}
			}
		})
	}
}

// TestWizardRulesRefuseWhatHasNoMeaning: the four errors refuse the deploy, each on the
// element the author has to change.
func TestWizardRulesRefuseWhatHasNoMeaning(t *testing.T) {
	cases := []struct {
		name, xml, rule, element string
	}{
		{"a value naming no kind",
			wizardAround(`atlas:wizard="yes"`, twoScreens, timeout), RuleWizardValue, "w"},
		{"a wizard in a wizard",
			wizardAround(`atlas:wizard="internal"`, `
	      <subProcess id="nested" atlas:wizard="internal">
	        <startEvent id="ns"/>
	        <userTask id="one"/>
	        <endEvent id="ne"/>
	        <sequenceFlow id="n1" sourceRef="ns" targetRef="one"/>
	        <sequenceFlow id="n2" sourceRef="one" targetRef="ne"/>
	      </subProcess>
	      <endEvent id="we"/>
	      <sequenceFlow id="w1" sourceRef="ws" targetRef="nested"/>
	      <sequenceFlow id="w2" sourceRef="nested" targetRef="we"/>`, timeout), RuleWizardNested, "nested"},
		{"an event subprocess",
			wizardModel(`
	    <startEvent id="s"/>
	    <userTask id="work"/>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="work"/>
	    <sequenceFlow id="f2" sourceRef="work" targetRef="e"/>
	    <subProcess id="late" triggeredByEvent="true" atlas:wizard="internal">
	      <startEvent id="ls"><timerEventDefinition><timeDuration>PT1H</timeDuration></timerEventDefinition></startEvent>
	      <userTask id="explain"/>
	      <endEvent id="le"/>
	      <sequenceFlow id="l1" sourceRef="ls" targetRef="explain"/>
	      <sequenceFlow id="l2" sourceRef="explain" targetRef="le"/>
	    </subProcess>`), RuleWizardEventSubProcess, "late"},
		{"an assignee in a public wizard",
			wizardAround(`atlas:wizard="public"`, strings.Replace(twoScreens, `<userTask id="two"/>`, `<userTask id="two"><extensionElements><zeebe:assignmentDefinition assignee="clerk"/></extensionElements></userTask>`, 1), timeout),
			RuleWizardPublicAssignment, "two"},
		{"candidate groups by expression in a public wizard",
			wizardAround(`atlas:wizard="public"`, strings.Replace(twoScreens, `<userTask id="one"/>`, `<userTask id="one"><extensionElements><zeebe:assignmentDefinition candidateGroups="=team"/></extensionElements></userTask>`, 1), timeout),
			RuleWizardPublicAssignment, "one"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := findProblem(problemsOf(t, c.xml), c.rule, c.element)
			if p == nil {
				t.Fatalf("no %s on %q; got %v", c.rule, c.element, problemsOf(t, c.xml))
			}
			if p.Severity != SeverityError {
				t.Errorf("severity = %q, want error", p.Severity)
			}
			if _, err := Parse(1, 1, strings.NewReader(c.xml)); err == nil {
				t.Error("the deploy went through")
			}
		})
	}
}

// TestWizardRulesWarnAboutWaiting: what leaves the filler waiting, and a wizard nobody
// bounds, are warnings. The model still deploys.
func TestWizardRulesWarnAboutWaiting(t *testing.T) {
	waiting := map[string]string{
		"a receive task": `<receiveTask id="wait" messageRef="m"/>`,
		"a timer catch":  `<intermediateCatchEvent id="wait"><timerEventDefinition><timeDuration>PT1H</timeDuration></timerEventDefinition></intermediateCatchEvent>`,
		"a call":         `<callActivity id="wait"><extensionElements><zeebe:calledElement processId="other"/></extensionElements></callActivity>`,
	}
	for name, el := range waiting {
		t.Run(name, func(t *testing.T) {
			xml := wizardAround(`atlas:wizard="internal"`, el+`
	      <userTask id="one"/>
	      <endEvent id="we"/>
	      <sequenceFlow id="w1" sourceRef="ws" targetRef="one"/>
	      <sequenceFlow id="w2" sourceRef="one" targetRef="wait"/>
	      <sequenceFlow id="w3" sourceRef="wait" targetRef="we"/>`, timeout)
			xml = strings.Replace(xml, `<process id="p"`, `<message id="m" name="m"><extensionElements><zeebe:subscription correlationKey="=k"/></extensionElements></message>
	  <process id="p"`, 1)
			p := findProblem(problemsOf(t, xml), RuleWizardWait, "wait")
			if p == nil {
				t.Fatalf("no %s; got %v", RuleWizardWait, problemsOf(t, xml))
			}
			if p.Severity != SeverityWarning {
				t.Errorf("severity = %q, want warning", p.Severity)
			}
			mustParse(t, xml)
		})
	}

	nonInterrupting := strings.Replace(timeout, `<boundaryEvent id="abandoned" attachedToRef="w">`, `<boundaryEvent id="abandoned" attachedToRef="w" cancelActivity="false">`, 1)
	for name, boundary := range map[string]string{"no boundary": ``, "a non-interrupting timer": nonInterrupting} {
		t.Run(name, func(t *testing.T) {
			xml := wizardAround(`atlas:wizard="internal"`, twoScreens, boundary)
			p := findProblem(problemsOf(t, xml), RuleWizardNoTimeout, "w")
			if p == nil || p.Severity != SeverityWarning {
				t.Fatalf("want a %s warning on w; got %v", RuleWizardNoTimeout, problemsOf(t, xml))
			}
			mustParse(t, xml)
		})
	}
}

// TestUntimedPublicWizard names the public wizard a start link must not be published
// for, and nothing once an interrupting timer bounds it. An internal wizard without one
// is the author's warning, not the link's refusal.
func TestUntimedPublicWizard(t *testing.T) {
	if got := mustParse(t, wizardAround(`atlas:wizard="public"`, twoScreens, ``)).UntimedPublicWizard(); got != "w" {
		t.Errorf("untimed public wizard = %q, want w", got)
	}
	if got := mustParse(t, wizardAround(`atlas:wizard="public"`, twoScreens, timeout)).UntimedPublicWizard(); got != "" {
		t.Errorf("a timed public wizard was reported: %q", got)
	}
	if got := mustParse(t, wizardAround(`atlas:wizard="internal"`, twoScreens, ``)).UntimedPublicWizard(); got != "" {
		t.Errorf("an internal wizard was reported: %q", got)
	}
}

// TestAnUnknownWizardValueStillReloads: the refusal is the deploy's. A definition
// already stored comes back as the ordinary subprocess it compiled to, with the
// finding reported (ADR-0177).
func TestAnUnknownWizardValueStillReloads(t *testing.T) {
	cp, problems, err := ReloadNamed(1, 1, strings.NewReader(wizardAround(`atlas:wizard="yes"`, twoScreens, timeout)), "p")
	if err != nil {
		t.Fatalf("ReloadNamed: %v", err)
	}
	if cp.Wizard(node(t, cp, "w")) != WizardNone {
		t.Error("an unknown value compiled to a wizard")
	}
	if findProblem(problems, RuleWizardValue, "w") == nil {
		t.Errorf("the reload did not report %s: %v", RuleWizardValue, problems)
	}
}

// TestACollapsedWizardCompilesLikeAnExpandedOne: a collapsed subprocess is the same
// semantic <subProcess> with its inside drawn on a plane of its own. The compiler never
// reads diagram interchange, so the overview's one shape and the drill-down's screens
// are one wizard (ADR-0449).
func TestACollapsedWizardCompilesLikeAnExpandedOne(t *testing.T) {
	expanded := wizardAround(`atlas:wizard="public"`, twoScreens, timeout)
	collapsed := strings.Replace(expanded, `</definitions>`, `
	  <bpmndi:BPMNDiagram id="d1" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" xmlns:dc="http://www.omg.org/spec/DD/20100524/DC">
	    <bpmndi:BPMNPlane id="plane" bpmnElement="p">
	      <bpmndi:BPMNShape id="w_di" bpmnElement="w" isExpanded="false"><dc:Bounds x="200" y="80" width="100" height="80"/></bpmndi:BPMNShape>
	    </bpmndi:BPMNPlane>
	  </bpmndi:BPMNDiagram>
	  <bpmndi:BPMNDiagram id="d2" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" xmlns:dc="http://www.omg.org/spec/DD/20100524/DC">
	    <bpmndi:BPMNPlane id="w_plane" bpmnElement="w">
	      <bpmndi:BPMNShape id="one_di" bpmnElement="one"><dc:Bounds x="100" y="80" width="100" height="80"/></bpmndi:BPMNShape>
	      <bpmndi:BPMNShape id="two_di" bpmnElement="two"><dc:Bounds x="250" y="80" width="100" height="80"/></bpmndi:BPMNShape>
	    </bpmndi:BPMNPlane>
	  </bpmndi:BPMNDiagram>
	</definitions>`, 1)
	a, b := mustParse(t, expanded), mustParse(t, collapsed)
	if a.NodeCount() != b.NodeCount() {
		t.Fatalf("node count %d collapsed, %d expanded", b.NodeCount(), a.NodeCount())
	}
	w := node(t, b, "w")
	if b.Wizard(w) != WizardPublic {
		t.Errorf("the collapsed subprocess lost its mark")
	}
	for _, step := range []string{"one", "two"} {
		if b.EnclosingWizard(node(t, b, step)) != w {
			t.Errorf("%s is not a step of the collapsed wizard", step)
		}
	}
	for _, p := range problemsOf(t, collapsed) {
		if strings.HasPrefix(p.Rule, "wizard.") {
			t.Errorf("unexpected %v", p)
		}
	}
}

// TestAnEventSubprocessWizardIsOnlyRefused: the refusal is the one thing to say about an
// event subprocess marked as a wizard. A timeout warning beside it would ask for a timer
// that cannot attach to an event subprocess at all.
func TestAnEventSubprocessWizardIsOnlyRefused(t *testing.T) {
	ps := problemsOf(t, wizardModel(`
	    <startEvent id="s"/>
	    <userTask id="work"/>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="work"/>
	    <sequenceFlow id="f2" sourceRef="work" targetRef="e"/>
	    <subProcess id="late" triggeredByEvent="true" atlas:wizard="public">
	      <startEvent id="ls"><timerEventDefinition><timeDuration>PT1H</timeDuration></timerEventDefinition></startEvent>
	      <endEvent id="le"/>
	      <sequenceFlow id="l1" sourceRef="ls" targetRef="le"/>
	    </subProcess>`))
	var rules []string
	for _, p := range ps {
		if p.Element == "late" {
			rules = append(rules, p.Rule)
		}
	}
	if len(rules) != 1 || rules[0] != RuleWizardEventSubProcess {
		t.Errorf("findings on the event subprocess = %v, want only %s", rules, RuleWizardEventSubProcess)
	}
}

// TestEnclosingWizardSurvivesAMalformedChain: Validate runs on processes a Builder put
// together by hand, whose scope chain may point outside the graph or back at itself.
// The walk answers "none" for both rather than panicking or looping.
func TestEnclosingWizardSurvivesAMalformedChain(t *testing.T) {
	b := NewBuilder(1, "p", 1)
	s := b.AddStartEvent()
	e := b.AddEndEvent()
	b.Connect(s, e)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	cp.nodes[e].FlowScope = 42
	if got := cp.EnclosingWizard(e); got != -1 {
		t.Errorf("out-of-range scope: EnclosingWizard = %d, want -1", got)
	}
	cp.nodes[s].FlowScope = s
	if got := cp.EnclosingWizard(s); got != -1 {
		t.Errorf("self-scoped node: EnclosingWizard = %d, want -1", got)
	}
	Validate(cp) // must return, whatever it reports
}
