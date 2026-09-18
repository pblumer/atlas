package api

import (
	"strings"
	"testing"
)

// Checking a mail worker asked for the recipient through a window.prompt.
//
// It is not the catalogue's defect — there is no list here, and nothing to click.
// It is the other half of what a prompt cannot do: the check has **two modes**, and
// the prompt encoded the choice between them as an emptiness. "Leave empty to only
// check the connection and credential" asks somebody to express "stop at the door"
// by typing nothing into a box, next to the one input that also means "send a real
// message to a real person". A stray character sends mail nobody asked for; an
// intended address typed into a dialog the browser suppressed sends nothing and
// says nothing.
//
// That last one is the reason this is a defect rather than a preference: a prompt
// is refusable. A sandboxed frame, or the "prevent this page from creating
// additional dialogs" box a person ticks once, makes window.prompt return null
// without opening — and null was indistinguishable from Cancel, so the check
// silently did nothing on a page where the operator had just asked for it.
//
// So the two modes are a control that names them, in a dialog the page draws itself.

// workerTestBranch returns app.js's "test" action, so this reads the check and not
// whatever else the console says about workers.
func workerTestBranch(t *testing.T) string {
	t.Helper()
	src := readWeb(t, "app.js")
	start := strings.Index(src, `} else if (act === "test") {`)
	if start < 0 {
		t.Fatal("app.js has no worker test action; if it moved, this test now passes " +
			"vacuously and says so instead")
	}
	end := strings.Index(src[start+1:], `} else if (act === "toggle")`)
	if end < 0 {
		t.Fatal("the worker test action has no recognisable end; the extraction above is stale")
	}
	return src[start : start+1+end]
}

// TestCheckingAMailWorkerNamesItsTwoModes is the defect: the choice between
// probing the connection and sending a real message must be a control, not the
// emptiness of a text box in a dialog the browser may refuse to open.
func TestCheckingAMailWorkerNamesItsTwoModes(t *testing.T) {
	branch := workerTestBranch(t)
	if strings.Contains(branch, "window.prompt(") {
		t.Error("checking a mail worker still asks a window.prompt, so the choice " +
			"between probing and sending is an empty box — and a suppressed prompt " +
			"cancels the check with nothing said")
	}
	if !strings.Contains(branch, "testWorkerFlow(") {
		t.Error("the worker check is not the shared flow, so it cannot be exercised " +
			"by a test: app.js boots the whole console on import")
	}

	app := readWeb(t, "app.js")
	if !strings.Contains(app, "testWorkerFlow") || !strings.Contains(app, `from "./workerdialog.js"`) {
		t.Error("app.js does not take the worker check from workerdialog.js, where the " +
			"worker dialogs that a test can reach live")
	}

	dlg := readWeb(t, "workerdialog.js")
	if !strings.Contains(dlg, "export async function testWorkerFlow(") {
		t.Fatal("workerdialog.js exports no testWorkerFlow(); the check has nowhere " +
			"to live that a test can open")
	}
	// The two modes, named. A select and not an empty string is the whole point.
	for _, want := range []string{`value="probe"`, `value="send"`} {
		if !strings.Contains(dlg, want) {
			t.Errorf("the check dialog has no %s option, so the two modes are not "+
				"something a person chooses between", want)
		}
	}
}

// TestAProbeIsStillAnEmptyRecipient: the dialog changed, the contract did not. The
// server decides what a check does by whether `to` is empty — empty probes the
// connection, an address sends a real message (api/connectors.go). A dialog that
// stopped sending the field, or sent a placeholder for "no recipient", would turn
// every probe into a delivery failure or worse, a delivery.
func TestAProbeIsStillAnEmptyRecipient(t *testing.T) {
	dlg := readWeb(t, "workerdialog.js")
	at := strings.Index(dlg, "export async function testWorkerFlow(")
	if at < 0 {
		t.Fatal("workerdialog.js exports no testWorkerFlow(); the check has nowhere " +
			"to live that a test can open")
	}
	flow := dlg[at:]
	if !strings.Contains(flow, "/api/v1/connectors/test") {
		t.Fatal("testWorkerFlow does not post the check; the extraction above is stale")
	}
	if !strings.Contains(flow, "to,") && !strings.Contains(flow, "to:") {
		t.Error("the check is posted without a recipient field, so the server cannot " +
			"tell a probe from a send")
	}
	// Only mail has a second half; every other kind's check dials and stops, so it
	// must not be made to ask a question it has no answer for.
	if !strings.Contains(flow, `"mail"`) {
		t.Error("the check asks for a recipient regardless of Worker Type, but only a " +
			"mail worker can be checked by sending something")
	}
}
