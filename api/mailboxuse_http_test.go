package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Who may use a mailbox (ADR-draft-mailbox-worker).
//
// ADR-0205 made a mail Worker its owner's to see and change. A mail task that reads the
// mailbox would have walked straight around that — the Console would say "private"
// while any modeler could deploy a task naming the Worker and read the mail. These
// tests are that door: reading needs viewer, changing needs editor, a name nobody
// holds is refused, and sending stays exactly as open as it was.

// mailWorkerPayload is an SMTP mail Worker with an IMAP side. Nothing here dials.
func mailWorkerPayload(name string) string {
	return `{"name":"` + name + `","kind":"mail","provider":"smtp","endpoint":"smtp.example.com",` +
		`"sender":"` + name + `@example.com","mailboxEndpoint":"imap.example.com","enabled":true}`
}

func createMailWorker(t *testing.T, c *http.Client, base, name string) string {
	t.Helper()
	status, body := postAs(t, c, base+"/api/v1/connectors", mailWorkerPayload(name))
	if status != http.StatusOK {
		t.Fatalf("create mail worker = %d: %s", status, body)
	}
	return decodeField(t, body, "id")
}

// mailTaskBPMN is a process with one mail task carrying the given attributes.
func mailTaskBPMN(processID, attrs string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" targetNamespace="http://atlas.test">
  <bpmn:process id="` + processID + `" isExecutable="true">
    <bpmn:startEvent id="Start_1"/>
    <bpmn:serviceTask id="Task_Mail">
      <bpmn:extensionElements><atlas:mailConnector ` + attrs + `/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="End_1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="Start_1" targetRef="Task_Mail"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_Mail" targetRef="End_1"/>
  </bpmn:process>
</bpmn:definitions>`
}

const (
	getAttrs  = `connector="annas-postfach" operation="get" messageId="=mailId" resultVariable="mail"`
	moveAttrs = `connector="annas-postfach" operation="move" messageId="=mailId" destination="Archiv"`
	sendAttrs = `connector="annas-postfach" to="x@example.com" subject="Hallo" body="Text"`
)

func TestAStrangerCannotDeployAProcessThatReadsSomebodyElsesMailbox(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "anna")
	bertID := createUser(t, admin, ts.URL, "bert")
	anna := signInAs(t, ts.URL, "anna", "a-password-that-is-long")
	bert := signInAs(t, ts.URL, "bert", "a-password-that-is-long")
	connID := createMailWorker(t, anna, ts.URL, "annas-postfach")

	status, body := deployAs(t, bert, ts.URL, mailTaskBPMN("bertLiest", getAttrs))
	if status != http.StatusForbidden {
		t.Fatalf("= %d, want 403: a stranger deployed a process that reads somebody else's mailbox\n%s", status, body)
	}
	var refusal map[string]any
	_ = json.Unmarshal([]byte(body), &refusal)
	if refusal["worker"] != "annas-postfach" || refusal["operation"] != "get" || refusal["elementId"] != "Task_Mail" {
		t.Errorf("the refusal must say which task, Worker and operation, so it can be acted on: %s", body)
	}
	if deployed := deployedProcessIDs(t, admin, ts.URL); deployed["bertLiest"] {
		t.Fatal("the refused definition exists anyway")
	}

	t.Run("sending stays as open as it was", func(t *testing.T) {
		if status, body := deployAs(t, bert, ts.URL, mailTaskBPMN("bertSendet", sendAttrs)); status != http.StatusOK {
			t.Errorf("= %d, want 200: a send through a shared sender is not what this check governs\n%s", status, body)
		}
	})
	t.Run("the owner may read her own mailbox", func(t *testing.T) {
		if status, body := deployAs(t, anna, ts.URL, mailTaskBPMN("annaLiest", getAttrs)); status != http.StatusOK {
			t.Errorf("= %d, want 200\n%s", status, body)
		}
	})
	t.Run("a viewer may read and may not change", func(t *testing.T) {
		shareConnector(t, anna, ts.URL, connID, bertID, "user", "viewer", http.StatusOK)
		if status, body := deployAs(t, bert, ts.URL, mailTaskBPMN("bertLiestJetzt", getAttrs)); status != http.StatusOK {
			t.Errorf("read as viewer = %d, want 200\n%s", status, body)
		}
		if status, body := deployAs(t, bert, ts.URL, mailTaskBPMN("bertVerschiebt", moveAttrs)); status != http.StatusForbidden {
			t.Errorf("move as viewer = %d, want 403: changing a mailbox needs editor\n%s", status, body)
		}
	})
	t.Run("an editor may change it", func(t *testing.T) {
		shareConnector(t, anna, ts.URL, connID, bertID, "user", "editor", http.StatusOK)
		if status, body := deployAs(t, bert, ts.URL, mailTaskBPMN("bertVerschiebtJetzt", moveAttrs)); status != http.StatusOK {
			t.Errorf("move as editor = %d, want 200\n%s", status, body)
		}
	})
}

// A model deployed against a name nobody holds would read whichever mailbox somebody
// configures under that name later. So the name has to exist — and be reachable — at
// deploy.
func TestAMailboxOperationOnANameNobodyHoldsIsRefused(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "bert")
	bert := signInAs(t, ts.URL, "bert", "a-password-that-is-long")
	status, body := deployAs(t, bert, ts.URL, mailTaskBPMN("vorgreifend",
		`connector="noch-niemandes" operation="list" resultVariable="mails"`))
	if status != http.StatusForbidden || !strings.Contains(body, "no mail worker of that name exists") {
		t.Fatalf("= %d, want 403 naming the missing worker\n%s", status, body)
	}
	// A send to an unconfigured name deploys as before, with the warning it always had.
	if status, body := deployAs(t, bert, ts.URL, mailTaskBPMN("sendetSpaeter",
		`connector="noch-niemandes" to="x@example.com"`)); status != http.StatusOK {
		t.Errorf("a send to an unconfigured worker = %d, want 200\n%s", status, body)
	}
}

func TestTheProjectDeployChecksTheMailboxToo(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "anna")
	createUser(t, admin, ts.URL, "bert")
	anna := signInAs(t, ts.URL, "anna", "a-password-that-is-long")
	bert := signInAs(t, ts.URL, "bert", "a-password-that-is-long")
	createMailWorker(t, anna, ts.URL, "annas-postfach")

	pid := createProjectAs(t, bert, ts.URL, "Berts Anwendung")
	saveDraftAs(t, bert, ts.URL, pid, mailTaskBPMN("bertImProjekt", getAttrs))
	status, body := postAs(t, bert, ts.URL+"/api/v1/projects/"+pid+"/deploy", "")
	if status != http.StatusForbidden || !strings.Contains(body, "annas-postfach") {
		t.Fatalf("project deploy = %d, want 403 naming the worker\n%s", status, body)
	}
	if deployedProcessIDs(t, admin, ts.URL)["bertImProjekt"] {
		t.Fatal("the refused draft was deployed anyway")
	}
}

// An application import is a deploy. It ran neither this check nor ADR-0205's claim
// before; a check that one door skips is decoration.
func TestTheApplicationImportChecksTheMailboxAndTheClaim(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "anna")
	createUser(t, admin, ts.URL, "bert")
	anna := signInAs(t, ts.URL, "anna", "a-password-that-is-long")
	bert := signInAs(t, ts.URL, "bert", "a-password-that-is-long")
	createMailWorker(t, anna, ts.URL, "annas-postfach")

	bundle := func(processID, xml string) string {
		raw, _ := json.Marshal(map[string]any{
			"application": "Importiert " + processID,
			"release":     map[string]any{"version": 1},
			"artifacts":   []any{map[string]any{"kind": "process", "processId": processID, "xml": xml}},
		})
		return string(raw)
	}
	status, body := postAs(t, bert, ts.URL+"/api/v1/applications/import", bundle("importLiest", mailTaskBPMN("importLiest", getAttrs)))
	if status != http.StatusForbidden || !strings.Contains(body, "annas-postfach") {
		t.Fatalf("import reading a mailbox = %d, want 403\n%s", status, body)
	}
	if deployedProcessIDs(t, admin, ts.URL)["importLiest"] {
		t.Fatal("the refused import was deployed anyway")
	}

	connID := createConnector(t, anna, ts.URL, "annas-ereignisse")
	if status, body := subscribeAs(t, anna, ts.URL, connID, "post-eingegangen"); status != http.StatusOK {
		t.Fatalf("anna's subscription = %d: %s", status, body)
	}
	status, body = postAs(t, bert, ts.URL+"/api/v1/applications/import",
		bundle("importLauscht", messageStartBPMN("importLauscht", "post-eingegangen")))
	if status != http.StatusConflict {
		t.Fatalf("import into a claimed message = %d, want 409\n%s", status, body)
	}
	if strings.Contains(body, "anna") {
		t.Errorf("the refusal discloses whose claim it is: %s", body)
	}
}

func TestAnOpenServerChecksNoMailbox(t *testing.T) {
	ts := newOpenConnectorServer(t)
	if status, body := deployAs(t, &http.Client{}, ts.URL, mailTaskBPMN("offen",
		`connector="irgendwer" operation="get" messageId="x" resultVariable="m"`)); status != http.StatusOK {
		t.Errorf("auth off = %d, want 200: an open server is open by declaration\n%s", status, body)
	}
}
