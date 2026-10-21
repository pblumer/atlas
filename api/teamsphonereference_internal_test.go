package api

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/teamsphone"
)

// The handbook's Teams Phone reference (api/web/handbuch.html, #ref-teamsphone) is where a
// modeller looks up what an operation takes and answers, and which codes an error boundary
// can catch. It is a copy of the worker's tables, so it is held to them: an operation, an
// attribute or a code the worker gains and the card does not name is one nobody models
// for, and a boundary drawn for a code the worker no longer throws never fires.
func TestTeamsPhoneReferenceNamesEveryOperationAttributeAndCode(t *testing.T) {
	body, err := fs.ReadFile(webFS, "web/handbuch.html")
	if err != nil {
		t.Fatalf("read handbuch.html: %v", err)
	}
	src := string(body)
	start := strings.Index(src, `<div class="card" id="ref-teamsphone">`)
	if start < 0 {
		t.Fatal(`handbuch.html has no card id="ref-teamsphone"; the Teams Phone panel links to it`)
	}
	card := src[start:]
	if next := strings.Index(card[1:], `<div class="card"`); next >= 0 {
		card = card[:next+1]
	}
	var missing []string
	names := append(append([]string{}, teamsphone.OpNames()...), teamsphone.Codes...)
	for name := range teamsphone.Attributes {
		names = append(names, name)
	}
	for name := range teamsphone.Settings {
		names = append(names, name)
	}
	for _, name := range names {
		if !strings.Contains(card, "<code>"+name+"</code>") {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("the Teams Phone reference card does not name %s; add each as <code>…</code> where it belongs", strings.Join(missing, ", "))
	}
}
