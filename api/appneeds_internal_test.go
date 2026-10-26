package api

import (
	"strings"
	"testing"
)

// needsBPMN reads LANGUAGE from the installation, with a fallback when fallback is
// set, and names a mail worker; a worker given as a FEEL expression is only known
// when the task runs, and is no need.
func needsBPMN(id string, fallback bool, worker string) needSource {
	value := ""
	if fallback {
		value = ` value="de-CH"`
	}
	return needSource{ID: id, XML: []byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0">
  <process id="` + id + `" isExecutable="true">
    <extensionElements><atlas:constants>
      <atlas:constant name="LANGUAGE" source="server"` + value + `/>
      <atlas:constant name="environment" source="server"/>
      <atlas:constant name="LOCAL" value="x"/>
    </atlas:constants></extensionElements>
    <startEvent id="s"/>
    <serviceTask id="notify">
      <extensionElements><atlas:mailConnector connector="` + worker + `" to="a@b.ch" subject="hi" body="hi"/></extensionElements>
    </serviceTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="notify"/>
    <sequenceFlow id="f2" sourceRef="notify" targetRef="e"/>
  </process>
</definitions>`)}
}

func TestNeedsOfMergesWhatSeveralProcessesNeed(t *testing.T) {
	needs, unread := needsOf([]needSource{
		needsBPMN("b", true, "Postausgang"),
		needsBPMN("a", false, "Postausgang"),
		needsBPMN("c", true, "=mandant"),
		{ID: "kaputt", XML: []byte("<definitions")},
	})
	if strings.Join(unread, ",") != "kaputt" {
		t.Errorf("unread = %v, want the draft that does not compile", unread)
	}
	// A process constant and a fact Atlas fills itself are no need; neither is a worker
	// written as an expression.
	if len(needs) != 2 {
		t.Fatalf("needs = %+v, want LANGUAGE and Postausgang", needs)
	}
	lang, mail := needs[0], needs[1]
	if lang.Kind != needServerConstant || lang.Name != "LANGUAGE" || strings.Join(lang.Processes, ",") != "a,b,c" {
		t.Errorf("LANGUAGE = %+v", lang)
	}
	// One process without a fallback is enough for a missing value to refuse a start.
	if lang.Fallback {
		t.Errorf("LANGUAGE has a fallback in every process: %+v", lang)
	}
	if mail.Kind != needWorker || mail.Name != "Postausgang" || mail.Type != "mail" || strings.Join(mail.Processes, ",") != "a,b" {
		t.Errorf("Postausgang = %+v", mail)
	}
	if needs, _ := needsOf([]needSource{needsBPMN("b", true, "x"), needsBPMN("c", true, "x")}); !needs[0].Fallback {
		t.Errorf("LANGUAGE with a fallback everywhere = %+v", needs[0])
	}
}
