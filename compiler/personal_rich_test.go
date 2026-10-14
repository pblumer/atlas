package compiler

import (
	"sort"
	"strings"
	"testing"
)

// The completeness of ADR-0314's refusal, checked on a process that actually has one of
// everything.
//
// personal_visitor_test.go closes two halves of the guard: that every compiled expression
// is accounted for by one of the two lists, and that every engine-evaluated path carries a
// label. What neither can show is the third: the visitor walks *slices*, so a path can be
// listed, labelled, and still never reached — because no fixture the refusal was ever run
// against contained a boundary event, a multi-instance activity, an event subprocess or an
// ad-hoc scope for its loop to iterate. A personal variable read in one of those would have
// deployed.
//
// So this fixture carries one of each, and the assertion is that the visitor returns a site
// for every kind it names. It fails with the list of what it could not reach, which is what
// makes it usable when the next expression-carrying element is added.
const richPersonalModel = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
             xmlns:atlas="http://atlas/schema/1.0" id="defs">
  <message id="Msg_a" name="anmeldung">
    <extensionElements><zeebe:subscription correlationKey="= kennung"/></extensionElements>
  </message>
  <message id="Msg_b" name="abbruch">
    <extensionElements><zeebe:subscription correlationKey="= kennung"/></extensionElements>
  </message>
  <message id="Msg_c" name="antwort">
    <extensionElements><zeebe:subscription correlationKey="= kennung"/></extensionElements>
  </message>
  <message id="Msg_d" name="start">
    <extensionElements><zeebe:subscription correlationKey="= kennung"/></extensionElements>
  </message>
  <process id="rich" isExecutable="true" atlas:personal="geheim" atlas:dataSubject="kennung">
    <dataObject id="DataObject_akte" name="akte"/>

    <startEvent id="s"/>

    <startEvent id="s_msg">
      <messageEventDefinition messageRef="Msg_d"/>
    </startEvent>

    <startEvent id="s_timer">
      <timerEventDefinition><timeCycle>= "R/PT5M"</timeCycle></timerEventDefinition>
    </startEvent>

    <exclusiveGateway id="gw"/>

    <scriptTask id="skript">
      <extensionElements><zeebe:script expression="= zaehler + 1" resultVariable="naechster"/></extensionElements>
    </scriptTask>

    <serviceTask id="attrappe">
      <extensionElements>
        <atlas:mockupConnector minDuration="PT1S" resultExpression="= zaehler * 2" resultVariable="doppelt"/>
      </extensionElements>
    </serviceTask>

    <serviceTask id="abbildung">
      <extensionElements>
        <zeebe:taskDefinition type="w"/>
        <zeebe:ioMapping>
          <zeebe:input source="= zaehler" target="lokal"/>
          <zeebe:output source="= lokal" target="ergebnis"/>
        </zeebe:ioMapping>
      </extensionElements>
    </serviceTask>

    <task id="lesen">
      <dataInputAssociation>
        <sourceRef>DataObject_akte</sourceRef>
        <targetRef>aktennummer</targetRef>
        <assignment><from>= akte.nummer</from></assignment>
      </dataInputAssociation>
      <dataOutputAssociation>
        <targetRef>DataObject_akte</targetRef>
        <assignment><from>= zaehler</from><to>akte.nummer</to></assignment>
      </dataOutputAssociation>
    </task>

    <serviceTask id="mehrfach">
      <extensionElements><zeebe:taskDefinition type="w"/></extensionElements>
      <multiInstanceLoopCharacteristics>
        <loopCardinality>= zaehler</loopCardinality>
        <completionCondition>= fertig</completionCondition>
        <extensionElements>
          <zeebe:loopCharacteristics outputCollection="ergebnisse" outputElement="= zaehler"/>
        </extensionElements>
      </multiInstanceLoopCharacteristics>
    </serviceTask>

    <serviceTask id="jeweils">
      <extensionElements><zeebe:taskDefinition type="w"/></extensionElements>
      <multiInstanceLoopCharacteristics>
        <extensionElements>
          <zeebe:loopCharacteristics inputCollection="= posten" inputElement="post"/>
        </extensionElements>
      </multiInstanceLoopCharacteristics>
    </serviceTask>

    <serviceTask id="schleife">
      <extensionElements><zeebe:taskDefinition type="w"/></extensionElements>
      <standardLoopCharacteristics><loopCondition>= nochmal</loopCondition></standardLoopCharacteristics>
    </serviceTask>

    <userTask id="freigeben">
      <extensionElements>
        <zeebe:assignmentDefinition assignee="= pruefer" candidateGroups="= gruppen"/>
      </extensionElements>
    </userTask>

    <intermediateCatchEvent id="bedingt">
      <conditionalEventDefinition><condition>= bereit</condition></conditionalEventDefinition>
    </intermediateCatchEvent>

    <intermediateCatchEvent id="warten_msg">
      <messageEventDefinition messageRef="Msg_a"/>
    </intermediateCatchEvent>

    <intermediateCatchEvent id="warten_timer">
      <timerEventDefinition><timeDuration>= dauer</timeDuration></timerEventDefinition>
    </intermediateCatchEvent>

    <intermediateThrowEvent id="senden">
      <messageEventDefinition messageRef="Msg_c"/>
    </intermediateThrowEvent>

    <receiveTask id="empfangen" messageRef="Msg_c">
    </receiveTask>

    <businessRuleTask id="regel">
      <extensionElements>
        <zeebe:calledDecision decisionId="d" resultVariable="entscheidung"/>
        <zeebe:ioMapping><zeebe:input source="= zaehler" target="Season"/></zeebe:ioMapping>
      </extensionElements>
    </businessRuleTask>

    <adHocSubProcess id="adhoc">
      <completionCondition>= fertig</completionCondition>
      <extensionElements>
        <atlas:agentConnector connector="agent_pb" resultCollection="werkzeugergebnisse" resultElement="= zaehler"/>
      </extensionElements>
      <serviceTask id="adhoc_t">
        <documentation>Ein Werkzeug, das der Agent aufrufen kann.</documentation>
        <extensionElements><zeebe:taskDefinition type="w"/></extensionElements>
      </serviceTask>
    </adHocSubProcess>

    <subProcess id="huelle">
      <startEvent id="h_s"/>
      <task id="h_t"/>
      <endEvent id="h_e"/>
      <sequenceFlow id="h_f1" sourceRef="h_s" targetRef="h_t"/>
      <sequenceFlow id="h_f2" sourceRef="h_t" targetRef="h_e"/>

      <subProcess id="ereignis_bedingt" triggeredByEvent="true">
        <startEvent id="eb_s">
          <conditionalEventDefinition><condition>= bereit</condition></conditionalEventDefinition>
        </startEvent>
        <endEvent id="eb_e"/>
        <sequenceFlow id="eb_f" sourceRef="eb_s" targetRef="eb_e"/>
      </subProcess>

      <subProcess id="ereignis_msg" triggeredByEvent="true">
        <startEvent id="em_s">
          <messageEventDefinition messageRef="Msg_b"/>
        </startEvent>
        <endEvent id="em_e"/>
        <sequenceFlow id="em_f" sourceRef="em_s" targetRef="em_e"/>
      </subProcess>

      <subProcess id="ereignis_timer" triggeredByEvent="true">
        <startEvent id="et_s">
          <timerEventDefinition><timeDuration>= "PT30M"</timeDuration></timerEventDefinition>
        </startEvent>
        <endEvent id="et_e"/>
        <sequenceFlow id="et_f" sourceRef="et_s" targetRef="et_e"/>
      </subProcess>
    </subProcess>

    <boundaryEvent id="rand_bedingt" attachedToRef="huelle" cancelActivity="false">
      <conditionalEventDefinition><condition>= bereit</condition></conditionalEventDefinition>
    </boundaryEvent>
    <boundaryEvent id="rand_msg" attachedToRef="huelle">
      <messageEventDefinition messageRef="Msg_b"/>
    </boundaryEvent>
    <boundaryEvent id="rand_timer" attachedToRef="freigeben">
      <timerEventDefinition><timeDuration>= dauer</timeDuration></timerEventDefinition>
    </boundaryEvent>

    <endEvent id="e"/>

    <sequenceFlow id="f1" sourceRef="s" targetRef="gw"/>
    <sequenceFlow id="f2" sourceRef="gw" targetRef="skript">
      <conditionExpression>= zaehler > 0</conditionExpression>
    </sequenceFlow>
    <sequenceFlow id="f3" sourceRef="gw" targetRef="attrappe"/>
    <sequenceFlow id="f4" sourceRef="skript" targetRef="abbildung"/>
    <sequenceFlow id="f5" sourceRef="attrappe" targetRef="lesen"/>
    <sequenceFlow id="f6" sourceRef="abbildung" targetRef="mehrfach"/>
    <sequenceFlow id="f7" sourceRef="lesen" targetRef="jeweils"/>
    <sequenceFlow id="f7b" sourceRef="jeweils" targetRef="schleife"/>
    <sequenceFlow id="f8" sourceRef="mehrfach" targetRef="freigeben"/>
    <sequenceFlow id="f9" sourceRef="schleife" targetRef="bedingt"/>
    <sequenceFlow id="f10" sourceRef="freigeben" targetRef="warten_msg"/>
    <sequenceFlow id="f11" sourceRef="bedingt" targetRef="warten_timer"/>
    <sequenceFlow id="f12" sourceRef="warten_msg" targetRef="senden"/>
    <sequenceFlow id="f13" sourceRef="warten_timer" targetRef="empfangen"/>
    <sequenceFlow id="f14" sourceRef="senden" targetRef="regel"/>
    <sequenceFlow id="f15" sourceRef="empfangen" targetRef="adhoc"/>
    <sequenceFlow id="f16" sourceRef="regel" targetRef="huelle"/>
    <sequenceFlow id="f17" sourceRef="adhoc" targetRef="e"/>
    <sequenceFlow id="f18" sourceRef="huelle" targetRef="e"/>
    <sequenceFlow id="f19" sourceRef="s_msg" targetRef="gw"/>
    <sequenceFlow id="f20" sourceRef="s_timer" targetRef="gw"/>
    <sequenceFlow id="f21" sourceRef="rand_bedingt" targetRef="e"/>
    <sequenceFlow id="f22" sourceRef="rand_msg" targetRef="e"/>
    <sequenceFlow id="f23" sourceRef="rand_timer" targetRef="e"/>
  </process>
</definitions>`

// TestTheRefusalReachesEveryKindInAProcessThatHasOne is the assertion; it also proves the
// rule produces no false refusal on a process this dense, where every expression reads
// something other than the declared name.
func TestTheRefusalReachesEveryKindInAProcessThatHasOne(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(richPersonalModel))
	if err != nil {
		t.Fatalf("the rich fixture does not compile: %v", err)
	}
	if !cp.IsPersonal("geheim") {
		t.Fatal("the fixture's declaration did not survive the build")
	}

	reached := map[string]bool{}
	for _, site := range cp.expressionSites() {
		reached[site.path] = true
	}
	var missing []string
	for path := range expressionKindByPath {
		if !reached[path] {
			missing = append(missing, path)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("the visitor reached no expression for %d kind(s):\n  %s\n\n"+
			"Each is a path the refusal walks but never sees, because this fixture has no element "+
			"carrying it — so a personal variable read there would deploy. Add the element (or, if the "+
			"kind no longer exists, remove it from expressionKindByPath).",
			len(missing), strings.Join(missing, "\n  "))
	}
}
