package compiler

import (
	"errors"
	"strings"
	"testing"
)

// A user task answered through a link a mail carries
// (ADR-draft-user-task-answered-by-link): the task opts in with
// atlas:answeredBy="link", and a mail task names it with answerTask and shows the link
// it is handed as answerLink.

// answerModel is a process that mails a link to the user task "approve" and waits on it.
// mailAttrs and taskAttrs are spliced into the mail connector and the user task; inner
// is extra content inside the user task's extensionElements.
func answerModel(mailAttrs, taskAttrs, taskExt string) string {
	return wizardModel(`
	    <startEvent id="s"/>
	    <serviceTask id="send">
	      <extensionElements>
	        <atlas:mailConnector connector="mail" to="=email" ` + mailAttrs + `/>
	      </extensionElements>
	    </serviceTask>
	    <userTask id="approve" ` + taskAttrs + `>
	      <extensionElements>` + taskExt + `</extensionElements>
	    </userTask>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="send"/>
	    <sequenceFlow id="f2" sourceRef="send" targetRef="approve"/>
	    <sequenceFlow id="f3" sourceRef="approve" targetRef="e"/>`)
}

const (
	answerForm  = `<zeebe:formDefinition formId="approve-form"/>`
	answerMail  = `answerTask="approve" subject="Approval" body="=&quot;Answer here: &quot; + answerLink"`
	answerOptIn = `atlas:answeredBy="link"`
)

func TestATaskAnsweredByLinkAndTheMailThatCarriesItCompile(t *testing.T) {
	xml := answerModel(answerMail, answerOptIn, answerForm)
	cp := mustParse(t, xml)
	if !cp.UserTask(cp.Node(node(t, cp, "approve")).Detail).AnsweredByLink {
		t.Error("the user task does not say it is answered by link")
	}
	if id, ok := cp.AnswerTask(node(t, cp, "send")); !ok || id != "approve" {
		t.Errorf("the mail task's answer task = %q (%v), want approve", id, ok)
	}
	if ps := problemsOf(t, xml); len(ps) != 0 {
		t.Errorf("a well-formed model has problems: %+v", ps)
	}
}

func TestAMailWithoutAnAnswerTaskNamesNone(t *testing.T) {
	cp := mustParse(t, answerModel(`subject="Hello" body="Hi"`, ``, answerForm))
	if id, ok := cp.AnswerTask(node(t, cp, "send")); ok {
		t.Errorf("answer task = %q, want none", id)
	}
	if cp.UserTask(cp.Node(node(t, cp, "approve")).Detail).AnsweredByLink {
		t.Error("a user task that did not opt in is answered by link")
	}
}

// TestALinkInATemplateIsShown: a templated body reads answerLink through a placeholder.
func TestALinkInATemplateIsShown(t *testing.T) {
	xml := answerModel(`answerTask="approve" templating="true" subject="Approval" body="Answer here: {{answerLink}}"`, answerOptIn, answerForm)
	if p := findProblem(problemsOf(t, xml), RuleAnswerUnshown, "send"); p != nil {
		t.Errorf("a template that shows the link is reported unshown: %s", p.Message)
	}
	// And in the HTML body or the subject alike.
	xml = answerModel(`answerTask="approve" subject="=&quot;Open &quot; + answerLink" body="Hi"`, answerOptIn, answerForm)
	if p := findProblem(problemsOf(t, xml), RuleAnswerUnshown, "send"); p != nil {
		t.Errorf("a subject that shows the link is reported unshown: %s", p.Message)
	}
}

func TestTheAnswerRules(t *testing.T) {
	for _, c := range []struct {
		name     string
		xml      string
		rule     string
		element  string
		severity Severity
	}{
		{"an unknown answeredBy value",
			answerModel(answerMail, `atlas:answeredBy="mail"`, answerForm), RuleAnswerValue, "approve", SeverityError},
		{"a task answered by link with no form",
			answerModel(answerMail, answerOptIn, ``), RuleAnswerForm, "approve", SeverityError},
		{"a multi-instance task answered by link",
			strings.Replace(answerModel(answerMail, answerOptIn, answerForm), `</extensionElements>
	    </userTask>`, `</extensionElements>
	      <multiInstanceLoopCharacteristics><extensionElements><zeebe:loopCharacteristics inputCollection="=[1,2]" inputElement="n"/></extensionElements></multiInstanceLoopCharacteristics>
	    </userTask>`, 1), RuleAnswerMultiInstance, "approve", SeverityError},
		{"an answer task that does not exist",
			answerModel(strings.Replace(answerMail, `answerTask="approve"`, `answerTask="nowhere"`, 1), answerOptIn, answerForm), RuleAnswerTask, "send", SeverityError},
		{"an answer task that did not opt in",
			answerModel(answerMail, ``, answerForm), RuleAnswerTask, "send", SeverityError},
		{"an answer task that is not a user task",
			answerModel(strings.Replace(answerMail, `answerTask="approve"`, `answerTask="send"`, 1), answerOptIn, answerForm), RuleAnswerTask, "send", SeverityError},
		{"an answer task outside the scope the mail is sent from",
			wizardModel(`
	    <startEvent id="s"/>
	    <subProcess id="sub">
	      <startEvent id="ss"/>
	      <serviceTask id="send"><extensionElements>
	        <atlas:mailConnector connector="mail" to="=email" ` + answerMail + `/>
	      </extensionElements></serviceTask>
	      <endEvent id="se"/>
	      <sequenceFlow id="s1" sourceRef="ss" targetRef="send"/>
	      <sequenceFlow id="s2" sourceRef="send" targetRef="se"/>
	    </subProcess>
	    <userTask id="approve" ` + answerOptIn + `><extensionElements>` + answerForm + `</extensionElements></userTask>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="sub"/>
	    <sequenceFlow id="f2" sourceRef="sub" targetRef="approve"/>
	    <sequenceFlow id="f3" sourceRef="approve" targetRef="e"/>`), RuleAnswerTask, "send", SeverityError},
		{"an answer task on a mailbox operation",
			strings.Replace(answerModel(`answerTask="approve" operation="list" resultVariable="mails"`, answerOptIn, answerForm), `to="=email" `, ``, 1),
			RuleAnswerOperation, "send", SeverityError},
		{"a mail that never shows the link",
			answerModel(`answerTask="approve" subject="Approval" body="Please answer"`, answerOptIn, answerForm), RuleAnswerUnshown, "send", SeverityError},
		{"a task answered by link whose link no mail sends",
			answerModel(`subject="Hello" body="Hi"`, answerOptIn, answerForm), RuleAnswerUnsent, "approve", SeverityWarning},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := findProblem(problemsOf(t, c.xml), c.rule, c.element)
			if p == nil {
				t.Fatalf("no %s problem on %s; got %+v", c.rule, c.element, problemsOf(t, c.xml))
			}
			if p.Severity != c.severity {
				t.Errorf("severity %s, want %s", p.Severity, c.severity)
			}
		})
	}
}

// TestAnAnswerErrorRefusesTheDeployButNotTheReload: the rules are stage 5, so a definition
// deployed before them comes back on restart and is reported, never refused at boot.
func TestAnAnswerErrorRefusesTheDeployButNotTheReload(t *testing.T) {
	xml := answerModel(answerMail, answerOptIn, ``) // no form: answer.form
	_, err := Parse(1, 1, strings.NewReader(xml))
	var ve *ValidationError
	if !errors.As(err, &ve) || findProblem(ve.Problems, RuleAnswerForm, "approve") == nil {
		t.Fatalf("deploy: err = %v, want a validation error naming answer.form", err)
	}
	cp, ps, err := ReloadNamed(1, 1, strings.NewReader(xml), "p")
	if err != nil || cp == nil {
		t.Fatalf("reload: %v", err)
	}
	if findProblem(ps, RuleAnswerForm, "approve") == nil {
		t.Errorf("the reload does not report what today's gate says: %+v", ps)
	}
}

// TestATaskBelowTheMailsScopeIsReachable: the link is bound to the scope the mail is sent
// from and opens a task in it or within it — a mail at the root may ask for a task inside
// a subprocess.
func TestATaskBelowTheMailsScopeIsReachable(t *testing.T) {
	xml := wizardModel(`
	    <startEvent id="s"/>
	    <serviceTask id="send"><extensionElements>
	      <atlas:mailConnector connector="mail" to="=email" ` + answerMail + `/>
	    </extensionElements></serviceTask>
	    <subProcess id="sub">
	      <startEvent id="ss"/>
	      <userTask id="approve" ` + answerOptIn + `><extensionElements>` + answerForm + `</extensionElements></userTask>
	      <endEvent id="se"/>
	      <sequenceFlow id="s1" sourceRef="ss" targetRef="approve"/>
	      <sequenceFlow id="s2" sourceRef="approve" targetRef="se"/>
	    </subProcess>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="send"/>
	    <sequenceFlow id="f2" sourceRef="send" targetRef="sub"/>
	    <sequenceFlow id="f3" sourceRef="sub" targetRef="e"/>`)
	if ps := problemsOf(t, xml); len(ps) != 0 {
		t.Errorf("problems: %+v", ps)
	}
}
