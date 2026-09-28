package api

import (
	"strings"
	"testing"
)

// A task form that shows who a provisioning step is for used to show the id. The
// names came only with an approval, so every other task that carries an order's
// recipient and orderer — handing over a device, setting up its operating system —
// read "usr_914b…" where a person expected a name. These hold the Tasks app to
// resolving both ids for any task, from the principals directory, without the
// process ever holding a name (ADR-0314).
func TestTaskFormsNameThePeopleOfAnyTask(t *testing.T) {
	src := readWeb(t, "app.js")

	mount := webRegion(t, src, "async function mountForm(", "\n  }\n")
	if !strings.Contains(mount, "peopleNames()") {
		t.Error("mountForm does not load the principals directory, so a task that is not " +
			"an approval shows its recipient as an id")
	}
	if !strings.Contains(mount, "withPeopleNamed(data || {}, state.approvals.get(t.key), names)") {
		t.Error("mountForm does not hand the directory to withPeopleNamed")
	}

	named := webRegion(t, src, "function withPeopleNamed(", "\n  }\n")
	for _, want := range []string{"out.recipientName = names.get(out.recipient)", "out.ordererName = names.get(out.orderer)"} {
		if !strings.Contains(named, want) {
			t.Errorf("withPeopleNamed no longer resolves %q from the directory", want)
		}
	}
	// An approval's own names still come first: they are resolved server-side for
	// exactly this task.
	if strings.Index(named, "approval.recipientName") > strings.Index(named, "names.get(out.recipient)") {
		t.Error("withPeopleNamed prefers the directory over the approval's own names")
	}
}
