package compiler

import (
	"errors"
	"strings"
	"testing"
)

// eventGatewayWith is eventGatewayBPMN's gateway carrying attrs — the two BPMN
// attributes that turn the exclusive deferred choice atlas runs into a construct it
// does not run (#804).
func eventGatewayWith(attrs string) string {
	return strings.Replace(eventGatewayBPMN, `<bpmn:eventBasedGateway id="wait"/>`,
		`<bpmn:eventBasedGateway id="wait" `+attrs+`/>`, 1)
}

// TestParseRefusesAnEventGatewayAtlasDoesNotRun is #804's first stage. An event-based
// gateway marked instantiate="true" (it starts the instance) or
// eventGatewayType="Parallel" (it waits for every event) used to deploy without a word
// and run as the exclusive choice — something other than what the diagram says. A
// deploy now refuses it, anchored to the gateway.
func TestParseRefusesAnEventGatewayAtlasDoesNotRun(t *testing.T) {
	for _, attrs := range []string{
		`instantiate="true"`,
		`instantiate="TRUE"`,
		`eventGatewayType="Parallel"`,
		`instantiate="true" eventGatewayType="Parallel"`,
		`instantiate="true" eventGatewayType="Exclusive"`,
	} {
		t.Run(attrs, func(t *testing.T) {
			_, err := Parse(1, 1, strings.NewReader(eventGatewayWith(attrs)))
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("Parse err = %v, want a ValidationError", err)
			}
			var found bool
			for _, p := range ve.Problems {
				if p.Rule != RuleEventGatewayKind {
					continue
				}
				found = true
				if p.Severity != SeverityError || p.Element != "wait" {
					t.Errorf("problem = %+v, want an error anchored to %q", p, "wait")
				}
				if !strings.Contains(p.Message, "exclusive") {
					t.Errorf("message = %q, want it to say what atlas runs instead", p.Message)
				}
			}
			if !found {
				t.Fatalf("problems = %+v, want %s", ve.Problems, RuleEventGatewayKind)
			}
		})
	}
}

// TestParseAcceptsTheEventGatewayAtlasRuns: the defaults a tool may write out loud are
// the exclusive deferred choice, and deploy as they always have.
func TestParseAcceptsTheEventGatewayAtlasRuns(t *testing.T) {
	for _, attrs := range []string{
		`instantiate="false"`,
		`eventGatewayType="Exclusive"`,
		`instantiate="false" eventGatewayType="Exclusive"`,
	} {
		t.Run(attrs, func(t *testing.T) {
			if _, err := Parse(1, 1, strings.NewReader(eventGatewayWith(attrs))); err != nil {
				t.Fatalf("Parse: %v", err)
			}
		})
	}
}

// TestValidateModelReportsAnEventGatewayAtlasDoesNotRun keeps the Modeler's Problems
// panel telling the same truth as a deploy.
func TestValidateModelReportsAnEventGatewayAtlasDoesNotRun(t *testing.T) {
	ps, err := ValidateModel(strings.NewReader(eventGatewayWith(`eventGatewayType="Parallel"`)))
	if err != nil {
		t.Fatalf("ValidateModel: %v", err)
	}
	for _, p := range ps {
		if p.Rule == RuleEventGatewayKind && p.Element == "wait" && p.Severity == SeverityError {
			return
		}
	}
	t.Fatalf("problems = %+v, want %s on %q", ps, RuleEventGatewayKind, "wait")
}

// TestReloadKeepsAnEventGatewayAtlasDoesNotRun draws the ADR-0177 line: a definition
// deployed before the rule existed comes back on reload as the exclusive choice it has
// always run as, with the finding reported beside it — never a server that will not
// start (ADR-0393).
func TestReloadKeepsAnEventGatewayAtlasDoesNotRun(t *testing.T) {
	cp, problems, err := ReloadNamed(1, 1, strings.NewReader(eventGatewayWith(`instantiate="true"`)), "req")
	if err != nil {
		t.Fatalf("ReloadNamed: %v", err)
	}
	if cp == nil {
		t.Fatal("ReloadNamed returned no compiled process")
	}
	if n := nodeByBpmnId(t, cp, "wait"); n.Type != TypeEventBasedGateway {
		t.Fatalf("gateway type = %v, want the EventBasedGateway it has always compiled to", n.Type)
	}
	var found bool
	for _, p := range problems {
		found = found || p.Rule == RuleEventGatewayKind
	}
	if !found {
		t.Fatalf("problems = %+v, want %s reported", problems, RuleEventGatewayKind)
	}
}
