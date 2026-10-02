package eventcatalog_test

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/pblumer/atlas/eventcatalog"
	"github.com/pblumer/atlas/logging"
)

var update = flag.Bool("update", false, "rewrite testdata/stable.golden from the catalogue")

// TestEveryEntryIsComplete holds each entry to ADR-0435 §1 and §2: a well-formed name
// that no other entry uses, the fields every entry states, and a moment that says
// where the event comes from.
func TestEveryEntryIsComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range eventcatalog.Entries {
		if seen[e.Type] {
			t.Errorf("%s: catalogued twice", e.Type)
		}
		seen[e.Type] = true
		if !e.Shape && !eventcatalog.WellNamed(e.Type) {
			t.Errorf("%s: not of the form atlas.<subject>.<fact>", e.Type)
		}
		if e.Kind != eventcatalog.Domain && e.Kind != eventcatalog.Platform {
			t.Errorf("%s: kind %q", e.Type, e.Kind)
		}
		if strings.TrimSpace(e.Meaning) == "" {
			t.Errorf("%s: no meaning", e.Type)
		}
		if len(e.Channels) == 0 {
			t.Errorf("%s: no channel", e.Type)
		}
		for _, ch := range e.Channels {
			switch ch {
			case eventcatalog.Signal, eventcatalog.Message, eventcatalog.Feed, eventcatalog.Log:
			default:
				t.Errorf("%s: unknown channel %q", e.Type, ch)
			}
		}
		if e.Stability != eventcatalog.Stable && e.Stability != eventcatalog.Experimental {
			t.Errorf("%s: stability %q", e.Type, e.Stability)
		}
		if strings.TrimSpace(e.Since) == "" {
			t.Errorf("%s: no release it arrived in", e.Type)
		}
		if strings.TrimSpace(e.SecretGuard) == "" {
			t.Errorf("%s: no test guards that its payload carries no secret", e.Type)
		}
		if len(e.Moments) == 0 {
			t.Errorf("%s: no moment", e.Type)
		}
		for _, m := range e.Moments {
			thrownByModel := e.Has(eventcatalog.Signal) || e.Has(eventcatalog.Message)
			if thrownByModel && (m.Process == "" || m.Element == "") {
				t.Errorf("%s: a signal or message names the system process and element of its moment", e.Type)
			}
			if !e.Has(eventcatalog.Signal) && m.Source == "" {
				t.Errorf("%s: a message or a feed fact names the source it is produced from", e.Type)
			}
		}
		// Platform facts are not thrown as signals (ADR-0435 §3): a listener whose own
		// failure raised the fact that started it would start again.
		if e.Kind == eventcatalog.Platform && e.Has(eventcatalog.Signal) {
			t.Errorf("%s: a platform fact is not thrown as a signal", e.Type)
		}
	}
}

// TestEveryPayloadFieldIsMarked is the drift test ADR-0435 §5 asks for: a field states
// whether it is personal data, with no default, because the access rule reads it. A
// field nobody looked at would otherwise pass as "not personal" and open its listeners
// to every modeler.
func TestEveryPayloadFieldIsMarked(t *testing.T) {
	types := map[string]bool{"string": true, "number": true, "boolean": true, "timestamp": true, "json": true}
	for _, e := range eventcatalog.Entries {
		names := map[string]bool{}
		for _, f := range e.Payload {
			if names[f.Name] {
				t.Errorf("%s.%s: declared twice", e.Type, f.Name)
			}
			names[f.Name] = true
			switch f.Personal {
			case eventcatalog.PersonalData, eventcatalog.NotPersonal:
			default:
				t.Errorf("%s.%s: does not say whether it is personal data", e.Type, f.Name)
			}
			if f.Presence != eventcatalog.Always && f.Presence != eventcatalog.Optional {
				t.Errorf("%s.%s: presence %q", e.Type, f.Name, f.Presence)
			}
			if !types[f.Type] {
				t.Errorf("%s.%s: type %q", e.Type, f.Name, f.Type)
			}
			if strings.TrimSpace(f.Meaning) == "" {
				t.Errorf("%s.%s: no meaning", e.Type, f.Name)
			}
		}
	}
}

// TestEveryDomainPayloadPointsBack holds ADR-0435 §3: a domain fact a model throws
// carries the instance that threw it, so a receiver can point back at the request.
func TestEveryDomainPayloadPointsBack(t *testing.T) {
	for _, e := range eventcatalog.Entries {
		if e.Kind != eventcatalog.Domain || !e.Has(eventcatalog.Signal) {
			continue
		}
		var found bool
		for _, f := range e.Payload {
			if f.Name == "atlasInstance" && f.Presence == eventcatalog.Always {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: a signal a system process throws carries atlasInstance", e.Type)
		}
	}
}

// TestLogChannelsNameALogEvent ties an entry with the log channel to the event in
// logging's catalogue that carries it.
func TestLogChannelsNameALogEvent(t *testing.T) {
	logged := map[string]bool{}
	for _, name := range logging.Events() {
		logged[name] = true
	}
	for _, e := range eventcatalog.Entries {
		switch {
		case e.Has(eventcatalog.Log) && !logged[e.LogEvent]:
			t.Errorf("%s: log event %q is not in logging's catalogue", e.Type, e.LogEvent)
		case !e.Has(eventcatalog.Log) && e.LogEvent != "":
			t.Errorf("%s: names log event %q without the log channel", e.Type, e.LogEvent)
		}
	}
}

// TestTheRuleFollowsTheData pins ADR-0435 §6: a listener needs admin when, and only
// when, the event's payload carries personal data.
func TestTheRuleFollowsTheData(t *testing.T) {
	open := eventcatalog.Entry{Channels: []eventcatalog.Channel{eventcatalog.Signal},
		Payload: []eventcatalog.Field{{Name: "orderId", Personal: eventcatalog.NotPersonal}}}
	if got := open.ListenerRole(); got != eventcatalog.RoleModeler {
		t.Errorf("an event without personal data needs %q to listen to, want modeler", got)
	}
	closed := open
	closed.Payload = append(closed.Payload, eventcatalog.Field{Name: "email", Personal: eventcatalog.PersonalData})
	if got := closed.ListenerRole(); got != eventcatalog.RoleAdmin {
		t.Errorf("an event with personal data needs %q to listen to, want admin", got)
	}
	if got := closed.PersonalFields(); len(got) != 1 || got[0] != "email" {
		t.Errorf("personal fields = %v, want [email]", got)
	}
	if got := closed.Access(); got[eventcatalog.Signal] != eventcatalog.RoleAdmin || len(got) != 1 {
		t.Errorf("access = %v, want only the signal channel, at admin", got)
	}

	user, ok := eventcatalog.Lookup("atlas.user.requested")
	if !ok || user.ListenerRole() != eventcatalog.RoleAdmin {
		t.Errorf("atlas.user.requested carries a requester's data; its listeners need admin")
	}
	if _, ok := eventcatalog.Lookup("<message>.<outcome>"); ok {
		t.Error("a shape is not a name a model can listen to")
	}
}

// TestStableEntriesNeverLoseAField is the golden file of ADR-0435 §5. A stable entry
// may gain fields; it never loses one, never changes a field's type and never makes an
// always-present field optional. Removing one is a failing test rather than a review
// comment. A field added on purpose is recorded with -update.
func TestStableEntriesNeverLoseAField(t *testing.T) {
	path := filepath.Join("testdata", "stable.golden")
	current := stableLines()
	if *update {
		if err := os.WriteFile(path, []byte(strings.Join(current, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	have := map[string]string{} // entry/field → "type presence"
	for _, l := range current {
		k, v := splitGolden(l)
		have[k] = v
	}
	recorded := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		k, want := splitGolden(line)
		recorded[k] = true
		got, ok := have[k]
		switch {
		case !ok:
			t.Errorf("%s: a stable entry never loses a field or the entry itself; mark it experimental in a release first, and call it out in the changelog", k)
		case got != want && !(strings.HasSuffix(want, " optional") && strings.TrimSuffix(got, " always") == strings.TrimSuffix(want, " optional")):
			t.Errorf("%s: was %q, is %q; a stable field keeps its type and stays present", k, want, got)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	for k := range have {
		if !recorded[k] {
			t.Errorf("%s: new in a stable entry; record it with go test ./eventcatalog -update", k)
		}
	}
}

func stableLines() []string {
	var out []string
	for _, e := range eventcatalog.Entries {
		if e.Stability != eventcatalog.Stable {
			continue
		}
		out = append(out, fmt.Sprintf("%s\t-\t-", e.Type))
		for _, f := range e.Payload {
			out = append(out, fmt.Sprintf("%s.%s\t%s\t%s", e.Type, f.Name, f.Type, f.Presence))
		}
	}
	sort.Strings(out)
	return out
}

func splitGolden(line string) (key, value string) {
	parts := strings.SplitN(line, "\t", 2)
	if len(parts) < 2 {
		return parts[0], ""
	}
	return parts[0], strings.ReplaceAll(parts[1], "\t", " ")
}
