package api

import (
	"strings"
	"testing"
)

// The inbox row says when a task was opened, and its filter finds a task by what
// is in it: a task "for Patrick Blumer" holds his id, so the search reads the
// values the list carries (?content=1) and each id among them under the name the
// principals directory gives it.
func TestTheInboxShowsWhenATaskOpenedAndSearchesWhatItIsAbout(t *testing.T) {
	src := readWeb(t, "app.js")

	list := webRegion(t, src, "function renderList()", "\n  }\n")
	if !strings.Contains(list, "openedLabel(t)") || !strings.Contains(list, "${id}${opened}") {
		t.Error("the task row no longer shows when the task was opened")
	}

	search := webRegion(t, src, "const taskHaystack = (t) => {", "const visible = () => {")
	for _, want := range []string{"t.content", "state.names.get(v)", "approvalName(ap)", "openedLabel(t)",
		".every((w) => hay.includes(w))"} {
		if !strings.Contains(search, want) {
			t.Errorf("the task filter no longer reads %s", want)
		}
	}

	// Every listing the inbox loads asks for the content, or the filter has nothing
	// to read on the rows that came from it.
	for _, want := range []string{`api("GET", "/api/v1/tasks?content=1")`,
		`"/api/v1/tasks?content=1&before="`, `"/api/v1/tasks?content=1&folder="`} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js no longer loads %s", want)
		}
	}
	if !strings.Contains(src, "state.names = names;") {
		t.Error("the inbox does not load the principals directory the search resolves ids with")
	}
}
