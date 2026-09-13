package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A deadline on an approval may remind and may move it, and may never decide it.
//
// That is the one rule in the whole escalation mechanism that cannot be recovered
// from if it is broken: a clock that records a refusal puts into a record kept
// forever a decision nobody made. The Go half holds it by construction — Reject
// takes the principal who decided, so a clock has nothing to pass — and this is
// the model half, where it would be broken by drawing one arrow.

var (
	approvalModel = regexp.MustCompile(`^genehmigung-.*\.bpmn$`)
	boundaryOn    = regexp.MustCompile(`<bpmn:boundaryEvent[^>]*attachedToRef="Genehmigen"[^>]*>`)
	cancelFalse   = regexp.MustCompile(`cancelActivity="false"`)
	// Matched where the path is *assigned*, not where it is mentioned: every one
	// of these models explains in prose which endpoint records a refusal, and a
	// check that could not tell the two apart would forbid the explanation.
	decisionPost = regexp.MustCompile(`/decision&#34;" target="path"`)
	// deadlineFlow names the flows a deadline's token travels along. Anything
	// reachable only through these is on a deadline branch.
	deadlineFlow = regexp.MustCompile(`id="(F_Erinnerung_\w+|F_Frist_\w+)"`)
)

func approvalModels(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(systemProcessesDir)
	if err != nil {
		t.Fatalf("read %s: %v", systemProcessesDir, err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !approvalModel.MatchString(e.Name()) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(systemProcessesDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		out[e.Name()] = string(b)
	}
	if len(out) == 0 {
		t.Fatalf("no approval models under %s — this test would pass on an empty set", systemProcessesDir)
	}
	return out
}

// TestNoDeadlineCanCloseTheApprovalItChases.
//
// An interrupting boundary timer cancels the activity it is attached to. On an
// approval that means the task disappears and the process carries on down the
// timer's path — so whatever that path does becomes the answer, and the answer
// was given by a clock. Non-interrupting is therefore not a preference here: it is
// what keeps "silence is not a refusal" true in the model rather than only in the
// prose above it.
func TestNoDeadlineCanCloseTheApprovalItChases(t *testing.T) {
	for name, src := range approvalModels(t) {
		t.Run(name, func(t *testing.T) {
			events := boundaryOn.FindAllString(src, -1)
			if len(events) == 0 {
				t.Fatalf("%s puts no deadline on its approval at all: an approval nobody "+
					"answers waits forever and nothing notices", name)
			}
			for _, e := range events {
				if !cancelFalse.MatchString(e) {
					t.Errorf("%s has an interrupting deadline on the approval:\n  %s\n"+
						"It would close the task whose completion it is chasing, and the path "+
						"it then takes would be a decision a clock made.", name, e)
				}
			}
		})
	}
}

// TestEveryApprovalRemindsBeforeItGivesUp: two deadlines, not one. A single one
// that jumps straight to moving the approval would take it away from somebody who
// was about to act; a reminder is the cheap thing to try first.
func TestEveryApprovalRemindsBeforeItGivesUp(t *testing.T) {
	for name, src := range approvalModels(t) {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(src, `id="Erinnerung"`) {
				t.Errorf("%s never reminds; its first deadline takes the approval away", name)
			}
			if !strings.Contains(src, `id="Frist"`) {
				t.Errorf("%s has no escalation deadline", name)
			}
			if !strings.Contains(src, "/escalate") {
				t.Errorf("%s reminds and then does nothing: a second reminder is not an escalation", name)
			}
		})
	}
}

// TestNoDeadlineBranchDecidesAnything is the rule stated as a shape: the only
// place an approval model reports a decision is the task the gateway feeds, and
// the gateway is fed by the user task — which only a person completes.
func TestNoDeadlineBranchDecidesAnything(t *testing.T) {
	for name, src := range approvalModels(t) {
		t.Run(name, func(t *testing.T) {
			if n := len(decisionPost.FindAllString(src, -1)); n != 1 {
				t.Fatalf("%s reports a decision in %d places; there is one legitimate one, "+
					"and it is reached only by completing the task", name, n)
			}
			// And that one is fed by the gateway, not by a deadline.
			deciding := src[strings.Index(src, `id="Ablehnen"`):]
			deciding = deciding[:strings.Index(deciding, "</bpmn:serviceTask>")]
			if !strings.Contains(deciding, "<bpmn:incoming>F_Gw_Ablehnen</bpmn:incoming>") {
				t.Errorf("%s: the task that records a refusal is not the one the gateway feeds", name)
			}
			for _, f := range deadlineFlow.FindAllStringSubmatch(deciding, -1) {
				t.Errorf("%s: a deadline's flow (%s) reaches the task that records a refusal. "+
					"A clock recording a refusal writes a decision nobody made into a record "+
					"kept forever.", name, f[1])
			}
		})
	}
}

// TestTheInterruptingCheckWouldBite: a check on a shape nobody ever writes is a
// check that passes for the wrong reason. This asserts the pattern against the
// thing it is meant to catch, so the guard above cannot quietly stop matching.
func TestTheInterruptingCheckWouldBite(t *testing.T) {
	interrupting := `<bpmn:boundaryEvent id="Frist" attachedToRef="Genehmigen" cancelActivity="true">`
	e := boundaryOn.FindString(interrupting)
	if e == "" {
		t.Fatal("the boundary pattern no longer finds a deadline on the approval at all")
	}
	if cancelFalse.MatchString(e) {
		t.Error("an interrupting deadline reads as non-interrupting")
	}
	// And the default, which BPMN says is interrupting when the attribute is absent.
	bare := `<bpmn:boundaryEvent id="Frist" attachedToRef="Genehmigen">`
	if cancelFalse.MatchString(boundaryOn.FindString(bare)) {
		t.Error("a deadline with no cancelActivity reads as non-interrupting; BPMN's default is the opposite")
	}
}
