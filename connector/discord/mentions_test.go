package discord_test

import (
	"testing"

	"github.com/pblumer/atlas/connector/discord"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
)

// A message built from what a stranger typed into a public form must not be able to
// ping a channel: a first name of "@everyone" is text, not a mention. The
// registration recipe (ADR-draft-system-processes-announce-their-facts-as-signals)
// silences mentions with the FEEL context literal {parse: []}, written inline rather
// than held in a variable. This holds that the literal reaches Discord as the object
// allowed_mentions needs, with an empty parse list, and not as a string Discord would
// reject or ignore.
func TestAContextLiteralSilencesMentions(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:discordConnector connector="team" operation="send-message" channel="42"
			content="=&quot;Neuer Aufnahme-Antrag von &quot; + vorname">
			<atlas:discordField name="allowed_mentions" value="={parse: []}"/>
		 </atlas:discordConnector>`,
		model.VariableValue{Name: "vorname", Kind: model.VarString, Text: "@everyone"},
	)
	c := &recordingClient{}
	reg := discord.NewRegistry()
	reg.Register("team", c)
	if _, err := discord.Handler(rd, lookup, reg)(job.Job{Key: 5, ElementInstanceKey: 42}); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(c.reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(c.reqs))
	}
	if got := c.reqs[0].Content; got != "Neuer Aufnahme-Antrag von @everyone" {
		t.Errorf("content = %q, want the typed name kept as text", got)
	}
	am, ok := c.reqs[0].Fields["allowed_mentions"].(map[string]any)
	if !ok {
		t.Fatalf("allowed_mentions = %#v, want an object", c.reqs[0].Fields["allowed_mentions"])
	}
	parse, ok := am["parse"].([]any)
	if !ok || len(parse) != 0 {
		t.Errorf("allowed_mentions.parse = %#v, want an empty list", am["parse"])
	}
}
