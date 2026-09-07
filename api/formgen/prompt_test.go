package formgen

import (
	"encoding/json"
	"strings"
	"testing"
)

// The system prompt is the contract: what to answer with, and out of what vocabulary.
// Every type it names has to be one the gate in schema.go will actually let through —
// the two drifting apart is how a feature starts refusing its own best answers.
func TestTheContractOffersOnlyWhatTheGateAccepts(t *testing.T) {
	sys := systemPrompt()
	for kind := range renderable {
		if !strings.Contains(sys, kind) {
			t.Errorf("the gate accepts %q but the prompt never offers it", kind)
		}
	}
	for _, refused := range []string{"iframe", "filepicker", "documentPreview", "expression"} {
		if strings.Contains(sys, refused) {
			t.Errorf("the prompt offers %q, which the gate refuses", refused)
		}
	}
	if !strings.Contains(sys, "nothing else") {
		t.Errorf("the prompt does not ask for the document alone:\n%s", sys)
	}
}

// Prose is the author's brief and it leads, because it is the only part of the request
// that says what this particular form is *for*.
func TestTheBriefLeads(t *testing.T) {
	got := goalPrompt(Request{Description: "Ein Antrag auf Sonderurlaub mit Grund und Zeitraum."}, Process{}, "")
	if !strings.HasPrefix(strings.TrimSpace(got), "What the form is for") {
		t.Errorf("goal does not open with the brief:\n%s", got)
	}
	if !strings.Contains(got, "Sonderurlaub") {
		t.Errorf("goal lost the author's words:\n%s", got)
	}
}

// The process is the second source, and naming a step turns the request from "a form"
// into "the form for this step".
func TestTheProcessReachesTheModel(t *testing.T) {
	p := ReadProcess([]byte(sampleBPMN))
	got := goalPrompt(Request{Description: "Bitte prüfen.", ElementID: "Task_Pruefen"}, p, "")

	if !strings.Contains(got, "Antrag prüfen") || !strings.Contains(got, "entscheidung") {
		t.Errorf("the process outline did not reach the prompt:\n%s", got)
	}
}

// Generating over a form that exists is a refinement, not a fresh start: the model has
// to see what it is changing, or "add a date field" replaces the other twelve.
func TestARefinementShowsTheFormAsItStands(t *testing.T) {
	current, err := json.Marshal(map[string]any{"type": "default", "components": []any{
		map[string]any{"type": "textfield", "key": "grund", "label": "Grund"},
	}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := goalPrompt(Request{Description: "Füge ein Datum hinzu."}, Process{}, string(current))
	if !strings.Contains(got, "The form as it stands") || !strings.Contains(got, `"grund"`) {
		t.Errorf("the current form did not reach the prompt:\n%s", got)
	}
	if !strings.Contains(got, "whole document") {
		t.Errorf("nothing told the model to return the whole form rather than the change:\n%s", got)
	}
}

// A request with no prose at all is legitimate — "a start form for this process" is a
// complete brief on its own — and must not produce a prompt with an empty heading in it.
func TestAProcessAloneIsABrief(t *testing.T) {
	got := goalPrompt(Request{ProcessID: "urlaubsantrag"}, ReadProcess([]byte(sampleBPMN)), "")
	brief, _, _ := strings.Cut(got, "The process")
	if strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(brief), "What the form is for")) == "--------------------" {
		t.Errorf("the brief is an empty heading:\n%s", got)
	}
	if !strings.Contains(got, "starts this process") {
		t.Errorf("nothing says what the form is for:\n%s", got)
	}
	if !strings.Contains(got, "Urlaubsantrag") {
		t.Errorf("the process is missing, and it is the whole brief here:\n%s", got)
	}
}
