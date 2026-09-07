package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/discord"
)

// fakeDiscordClient answers a list with a canned page and records what it was asked.
type fakeDiscordClient struct {
	asked []discord.Request
	pages [][]any
	err   error
}

func (f *fakeDiscordClient) Do(_ context.Context, req discord.Request) (any, error) {
	f.asked = append(f.asked, req)
	if f.err != nil {
		return nil, f.err
	}
	if len(f.pages) == 0 {
		return []any{}, nil
	}
	page := f.pages[0]
	f.pages = f.pages[1:]
	return page, nil
}

// discordMessage builds the shape Discord's list endpoint returns. Ids are strings in
// Discord's JSON — they are 64-bit and would not survive a JavaScript number.
func discordMessage(id, content, authorID string, bot bool) map[string]any {
	var m map[string]any
	_ = json.Unmarshal([]byte(`{
		"id": "`+id+`",
		"content": "`+content+`",
		"timestamp": "2026-09-07T10:00:00.000000+00:00",
		"author": {"id": "`+authorID+`", "username": "melder", "bot": `+boolJSON(bot)+`}
	}`), &m)
	return m
}

func boolJSON(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// discordWatch is the record a channel watch is read from.
func discordWatch(channel, cursor string) inboundSubscription {
	return inboundSubscription{ID: "watch-1", ConnectorID: "conn-1", ChannelID: channel, LastEventID: cursor}
}

// The page is published oldest first, whatever order Discord returned it in. This is
// the test that matters most: the bridge publishes in slice order against one mark per
// watch, so a newest-first page would set the mark from its first element and have
// every older message behind it correctly discarded by the engine.
func TestDiscordSourceSortsThePageAscending(t *testing.T) {
	// Discord answers newest first.
	f := &fakeDiscordClient{pages: [][]any{{
		discordMessage("300", "dritte", "u1", false),
		discordMessage("200", "zweite", "u1", false),
		discordMessage("100", "erste", "u1", false),
	}}}
	events, cursor, err := discordSource{client: f}.Read(context.Background(), discordWatch("42", ""), 25)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3", len(events))
	}
	for i, want := range []uint64{100, 200, 300} {
		if events[i].Seq != want {
			t.Errorf("events[%d].Seq = %d, want %d — the page must be published oldest first", i, events[i].Seq, want)
		}
	}
	if cursor != "300" {
		t.Errorf("cursor = %q, want the newest snowflake", cursor)
	}
}

// The sequence is the snowflake itself and the mark is keyed on the channel, which is
// what routes it through the composition branch carrying the watch's own id.
func TestDiscordSourceSequencesOnTheSnowflakeAndMarksPerChannel(t *testing.T) {
	f := &fakeDiscordClient{pages: [][]any{{discordMessage("1420070400000000000", "hallo", "u1", false)}}}
	events, _, err := discordSource{client: f}.Read(context.Background(), discordWatch("42", ""), 25)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if events[0].Seq != 1420070400000000000 {
		t.Errorf("Seq = %d, want the snowflake itself", events[0].Seq)
	}
	if events[0].MarkKey != "42" {
		t.Errorf("MarkKey = %q, want the channel id", events[0].MarkKey)
	}
}

// Two watches on one Worker must not share a mark. That is the defect this design
// exists to avoid: the scalar branch keys on WatchedSubject, which is clio's field and
// empty for every other kind, so leaving MarkKey empty would collapse them onto one.
func TestDiscordWatchesDoNotShareAMark(t *testing.T) {
	a := inboundSubscription{ID: "watch-a", ConnectorID: "conn-1", ChannelID: "42"}
	b := inboundSubscription{ID: "watch-b", ConnectorID: "conn-1", ChannelID: "42"}
	ida := inboundSourceID(connectorKindDiscord, a, a.ChannelID)
	idb := inboundSourceID(connectorKindDiscord, b, b.ChannelID)
	if ida == idb {
		t.Fatalf("two watches on one Worker share the mark %q; one would suppress the other's messages", ida)
	}
	if !strings.Contains(ida, "watch-a") || !strings.Contains(ida, "42") {
		t.Errorf("sourceID = %q, want it to carry both the watch and the channel", ida)
	}
}

// A watch with no cursor reads from the beginning, not from the tip. An absent `after`
// makes Discord answer with the newest page, so a backfill watch would publish the most
// recent hundred messages and never see the history it was pointed at.
func TestDiscordSourceReadsFromTheBeginningWithoutACursor(t *testing.T) {
	f := &fakeDiscordClient{pages: [][]any{{}}}
	if _, _, err := (discordSource{client: f}).Read(context.Background(), discordWatch("42", ""), 25); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := f.asked[0].After; got != discord.BeginningCursor {
		t.Errorf("after = %q, want the beginning sentinel %q", got, discord.BeginningCursor)
	}
	if f.asked[0].Channel != "42" {
		t.Errorf("channel = %q, want the watch's channel", f.asked[0].Channel)
	}
	if f.asked[0].Operation != "list-messages" {
		t.Errorf("operation = %q, want the Worker's own list operation", f.asked[0].Operation)
	}
}

// A watch that has read before resumes after its own cursor.
func TestDiscordSourceResumesFromItsCursor(t *testing.T) {
	f := &fakeDiscordClient{pages: [][]any{{}}}
	if _, _, err := (discordSource{client: f}).Read(context.Background(), discordWatch("42", " 555 "), 25); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := f.asked[0].After; got != "555" {
		t.Errorf("after = %q, want the stored cursor, trimmed", got)
	}
}

// The page cap is Discord's, not the bridge's: defaultInboundBatch is 256 and is an
// operator setting, and a larger limit is answered with a 400 naming a field nobody set.
func TestDiscordSourceClampsToDiscordsPageCap(t *testing.T) {
	for _, tc := range []struct {
		name  string
		limit int
		want  int32
	}{
		{"the bridge's default", defaultInboundBatch, discord.MaxListPageSize},
		{"zero", 0, discord.MaxListPageSize},
		{"under the cap", 25, 25},
		// Past int32: converting the limit rather than comparing it as an int would
		// wrap this to something small, or negative, and quietly ask for a page nobody
		// chose.
		{"past int32", 1 << 40, discord.MaxListPageSize},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeDiscordClient{pages: [][]any{{}}}
			if _, _, err := (discordSource{client: f}).Read(context.Background(), discordWatch("42", ""), tc.limit); err != nil {
				t.Fatalf("Read: %v", err)
			}
			if got := f.asked[0].MaxResults; got != tc.want {
				t.Errorf("maxResults = %d, want %d", got, tc.want)
			}
		})
	}
}

// A message exposes a curated envelope plus the raw object. authorBot is the one a
// watch on a channel its own Worker posts into cannot do without.
func TestDiscordSourceFieldEnvelope(t *testing.T) {
	f := &fakeDiscordClient{pages: [][]any{{discordMessage("100", "Antrag 17", "u1", true)}}}
	events, _, err := discordSource{client: f}.Read(context.Background(), discordWatch("42", ""), 25)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	fields := events[0].Fields
	for name, want := range map[string]any{
		"eventType": "discord.message.created",
		"messageId": "100",
		"channelId": "42",
		"content":   "Antrag 17",
		"authorId":  "u1",
		"authorBot": true,
	} {
		if fields[name] != want {
			t.Errorf("fields[%q] = %#v, want %#v", name, fields[name], want)
		}
	}
	if _, ok := fields["message"].(map[string]any); !ok {
		t.Errorf("fields[\"message\"] = %#v, want the whole message for anything not named", fields["message"])
	}
}

// A message with no author object still answers the bot question, because a watch that
// filters on it would otherwise read "unknown" as "not a bot" — a webhook post is the
// case that produces one.
func TestDiscordSourceAuthorlessMessageIsNotABot(t *testing.T) {
	var m map[string]any
	_ = json.Unmarshal([]byte(`{"id":"100","content":"x"}`), &m)
	f := &fakeDiscordClient{pages: [][]any{{m}}}
	events, _, err := discordSource{client: f}.Read(context.Background(), discordWatch("42", ""), 25)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if events[0].Fields["authorBot"] != false {
		t.Errorf("authorBot = %#v, want false rather than absent", events[0].Fields["authorBot"])
	}
}

// An id that is not a snowflake carries no sequence, and an event with no sequence has
// no place in a mechanism whose correctness rests on one.
func TestDiscordSourceSkipsAMessageWithNoSnowflake(t *testing.T) {
	f := &fakeDiscordClient{pages: [][]any{{
		discordMessage("nicht-numerisch", "kaputt", "u1", false),
		discordMessage("200", "gut", "u1", false),
	}}}
	events, cursor, err := discordSource{client: f}.Read(context.Background(), discordWatch("42", ""), 25)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(events) != 1 || events[0].Seq != 200 {
		t.Fatalf("events = %+v, want only the message that carries a snowflake", events)
	}
	if cursor != "200" {
		t.Errorf("cursor = %q, want the usable message's id", cursor)
	}
}

// A page in which nothing carries a snowflake leaves the cursor where it was, rather
// than stepping it somewhere arbitrary.
func TestDiscordSourceLeavesTheCursorOnAnUnusablePage(t *testing.T) {
	f := &fakeDiscordClient{pages: [][]any{{discordMessage("x", "kaputt", "u1", false)}}}
	events, cursor, err := discordSource{client: f}.Read(context.Background(), discordWatch("42", "555"), 25)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(events) != 0 || cursor != "" {
		t.Errorf("= %+v, %q; want nothing published and the cursor untouched", events, cursor)
	}
}

// A read failure is returned so the watch retries next tick rather than advancing past
// what it could not read.
func TestDiscordSourceReadError(t *testing.T) {
	f := &fakeDiscordClient{err: errors.New("Discord said no")}
	if _, _, err := (discordSource{client: f}).Read(context.Background(), discordWatch("42", ""), 25); err == nil {
		t.Fatal("Read swallowed the client's error")
	}
}

// Priming reaches the tip in one read of the newest message and publishes nothing —
// which is what keeps a watch pointed at a busy channel from starting a process per
// historical message.
func TestDiscordSourcePrimeTakesTheNewestMessage(t *testing.T) {
	f := &fakeDiscordClient{pages: [][]any{{discordMessage("900", "letzte", "u1", false)}}}
	cursor, done, err := discordSource{client: f}.Prime(context.Background(), discordWatch("42", ""))
	if err != nil {
		t.Fatalf("Prime: %v", err)
	}
	if cursor != "900" || !done {
		t.Errorf("= %q, %v; want the newest id and done on the first call", cursor, done)
	}
	// No cursor: asking without `after` is what returns the newest page, and priming is
	// the one case where that is wanted.
	if f.asked[0].After != "" {
		t.Errorf("after = %q, want none while priming", f.asked[0].After)
	}
	if f.asked[0].MaxResults != 1 {
		t.Errorf("maxResults = %d, want a single message", f.asked[0].MaxResults)
	}
}

// An empty channel primes to nothing: there is no snowflake to resume from.
func TestDiscordSourcePrimeOnAnEmptyChannel(t *testing.T) {
	f := &fakeDiscordClient{pages: [][]any{{}}}
	cursor, done, err := discordSource{client: f}.Prime(context.Background(), discordWatch("42", ""))
	if err != nil || cursor != "" || !done {
		t.Fatalf("= %q, %v, %v; want an empty cursor and done", cursor, done, err)
	}
}

// A failed prime is reported so the watch stays unprimed and tries again, rather than
// being marked primed at a cursor it never read.
func TestDiscordSourcePrimeError(t *testing.T) {
	f := &fakeDiscordClient{err: errors.New("Discord said no")}
	if _, _, err := (discordSource{client: f}).Prime(context.Background(), discordWatch("42", "")); err == nil {
		t.Fatal("Prime swallowed the client's error")
	}
}

// --- the watch's shape ---

// A discord watch needs its channel and nothing else; the fields the other kinds use
// are refused rather than stored where nothing would ever read them.
func TestValidateDiscordWatch(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  inboundSubscription
		want string
	}{
		{"no channel", inboundSubscription{}, "channelId is required"},
		{"a clio subject", inboundSubscription{ChannelID: "42", WatchedSubject: "/x"}, "belong to a clio"},
		{"a jira query", inboundSubscription{ChannelID: "42", JQL: "project = OPS"}, "belong to a clio"},
		{"a spreadsheet", inboundSubscription{ChannelID: "42", SpreadsheetID: "1B"}, "belong to a clio"},
		{"a drive folder", inboundSubscription{ChannelID: "42", FolderID: "f"}, "belong to a clio"},
		{"a cursor field", inboundSubscription{ChannelID: "42", CursorField: "updated"}, "never moves"},
		{"a lag", inboundSubscription{ChannelID: "42", LagSeconds: 30}, "not by a timestamp"},
		{"a negative cadence", inboundSubscription{ChannelID: "42", PollSeconds: -1}, "cannot be negative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := tc.rec
			msg := validateInboundWatch(connectorKindDiscord, &rec)
			if !strings.Contains(msg, tc.want) {
				t.Errorf("message = %q, want it to mention %q", msg, tc.want)
			}
		})
	}
	rec := inboundSubscription{ChannelID: "42", PollSeconds: 30}
	if msg := validateInboundWatch(connectorKindDiscord, &rec); msg != "" {
		t.Errorf("a channel watch with its own cadence was refused: %s", msg)
	}
}

// The message-sources view says what a watch reads in that kind's own words, which is
// what an author sees beside a message-start event.
func TestDescribeDiscordWatch(t *testing.T) {
	got := describeInboundWatch(connectorKindDiscord, inboundSubscription{ChannelID: "42"})
	if got != "new messages in channel 42" {
		t.Errorf("describeInboundWatch = %q", got)
	}
}

// --- the bridge's wiring ---

// A channel watch is read on its own cadence: Discord rate-limits per bot token, so
// this is a budget question — but a chat channel is watched because somebody is
// waiting, which is why the default sits far below Google's minute.
func TestDiscordCadence(t *testing.T) {
	if got := inboundCadence(connectorKindDiscord, inboundSubscription{}); got != discordDefaultPoll {
		t.Errorf("cadence = %v, want the kind default %v", got, discordDefaultPoll)
	}
	if got := inboundCadence(connectorKindDiscord, inboundSubscription{PollSeconds: 5}); got != 5*time.Second {
		t.Errorf("cadence = %v, want the watch's own 5s", got)
	}
}

// A watch on a Discord Worker with a live client resolves to a discordSource. Without
// the registry entry it resolves to nothing rather than to a source that would fail
// every poll — the same rule every other kind follows.
func TestResolveInboundSubsBuildsADiscordSource(t *testing.T) {
	srv, _ := newValidateServer(t, WithInboundPollInterval(0))
	srv.do(func() {
		_ = srv.connectors.Save(connector{ID: "d1", Name: "team", Kind: connectorKindDiscord, CredentialsRef: "r", Enabled: true, CreatedAt: 1})
		_ = srv.inboundSubs.Save(inboundSubscription{ID: "s-d", ConnectorID: "d1", ChannelID: "42", MessageName: "m", Enabled: true, CreatedAt: 1})
	})
	var got []pendingSub
	srv.do(func() { got = srv.resolveInboundSubs() })
	if len(got) != 0 {
		t.Fatalf("resolved %d subs with no live client; want none", len(got))
	}

	srv.do(func() { srv.discordRegistry.Replace(map[string]discord.Client{"team": &fakeDiscordClient{}}) })
	srv.do(func() { got = srv.resolveInboundSubs() })
	if len(got) != 1 {
		t.Fatalf("resolveInboundSubs = %d subs, want the discord watch", len(got))
	}
	if _, ok := got[0].source.(discordSource); !ok {
		t.Errorf("source = %T, want a discordSource", got[0].source)
	}
	if got[0].kind != connectorKindDiscord {
		t.Errorf("kind = %q, want %q", got[0].kind, connectorKindDiscord)
	}
}
