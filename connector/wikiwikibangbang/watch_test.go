package wikiwikibangbang

import (
	"context"
	"strings"
	"testing"
	"time"
)

// readWiki is a fake whose account reads with a token of scope read, the only kind a
// watch accepts.
func readWiki(t *testing.T) (*fakeWiki, *Client) {
	t.Helper()
	f := newFakeWiki(t)
	f.me.Scope = "read"
	c := f.client(t)
	c.now = func() time.Time {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.clock
	}
	return f, c
}

// tipOf primes a watch on a space and fails the test if it cannot.
func tipOf(t *testing.T, c *Client, space string) string {
	t.Helper()
	cursor, err := c.WatchTip(context.Background(), space)
	if err != nil {
		t.Fatalf("WatchTip: %v", err)
	}
	return cursor
}

func readFinished(t *testing.T, c *Client, req WatchRequest) WatchPage {
	t.Helper()
	page, err := c.FinishedSince(context.Background(), req)
	if err != nil {
		t.Fatalf("FinishedSince: %v", err)
	}
	return page
}

func ids(page WatchPage) []string {
	out := []string{}
	for _, it := range page.Finished {
		out = append(out, it.ID)
	}
	return out
}

// The engine reads the feed itself, so a token that can write never reaches it: a watch
// is refused on a Worker whose token has scope full, before it reads anything.
func TestAWatchRefusesATokenThatCanWrite(t *testing.T) {
	f := newFakeWiki(t)
	c := f.client(t)
	for name, read := range map[string]func() error{
		"tip": func() error { _, err := c.WatchTip(context.Background(), "OPS"); return err },
		"read": func() error {
			_, err := c.FinishedSince(context.Background(), WatchRequest{Space: "OPS", Cursor: "0|2026-10-27T09:00:00Z"})
			return err
		},
	} {
		err := read()
		if err == nil || !strings.Contains(err.Error(), "scope read") || !strings.Contains(err.Error(), `"full"`) {
			t.Errorf("%s with a full token = %v, want a refusal that names the scope", name, err)
		}
	}
	if n := f.count("GET /api/changes") + f.count("PUT /api/spaces/OPS/watch"); n != 0 {
		t.Errorf("a refused watch made %d reads of the feed or the watch", n)
	}
	f.me.Scope, f.me.Admin = "read", true
	c = f.client(t)
	if _, err := c.WatchTip(context.Background(), "OPS"); err == nil || !strings.Contains(err.Error(), "admin") {
		t.Errorf("a watch with an administrator's token = %v, want refused", err)
	}
}

// A watch starts at the newest change: what was finished before it is not published,
// and the space is watched before the tip is read, so nothing between the two is lost.
func TestAWatchStartsAtTheTip(t *testing.T) {
	f, c := readWiki(t)
	done := f.add("OPS", "task", "finished long ago", nil, nil)
	f.setState(done, "done")
	f.tick(time.Hour)
	cursor := tipOf(t, c, "OPS")
	if f.count("PUT /api/spaces/OPS/watch") != 1 {
		t.Fatal("the tip was read without watching the space")
	}
	seq, since, ok := parseWatchCursor(cursor)
	if !ok || seq != len(f.log) {
		t.Fatalf("tip cursor %q: seq %d, want the newest change %d", cursor, seq, len(f.log))
	}
	if want := f.clock.Add(-watchSlack); !since.Equal(want) {
		t.Errorf("since = %v, want the wiki's own clock less the slack, %v", since, want)
	}
	if page := readFinished(t, c, WatchRequest{Space: "OPS", Cursor: cursor}); len(page.Finished) != 0 || page.Cursor != cursor {
		t.Errorf("a read at the tip = %v, cursor %q; want nothing and the same cursor", ids(page), page.Cursor)
	}
}

// A task finished after the watch began is published once, with the time it was
// finished at, which is the sequence the engine deduplicates it under.
func TestAFinishedTaskIsPublished(t *testing.T) {
	f, c := readWiki(t)
	cursor := tipOf(t, c, "OPS")
	task := f.add("OPS", "task", "Laptop vorbereiten", map[string]any{"assignee": "ana"}, nil)
	f.add("OPS", "page", "Runbook", nil, nil)
	open := f.add("OPS", "task", "still open", nil, nil)
	f.tick(time.Minute)
	f.setState(task, "done")
	finishedAt := f.clock
	f.edit(open, "still open, renamed")

	page := readFinished(t, c, WatchRequest{Space: "OPS", Cursor: cursor})
	if got := ids(page); len(got) != 1 || got[0] != task.ID {
		t.Fatalf("published %v, want only the finished task %s", got, task.ID)
	}
	got := page.Finished[0]
	if !got.At.Equal(finishedAt) {
		t.Errorf("At = %v, want when it was finished, %v", got.At, finishedAt)
	}
	if got.Item["key"] != task.Key || got.Item["state"] != "done" || got.Item["type"] != "task" {
		t.Errorf("Item = %v", got.Item)
	}
	if _, ok := got.Item["body"]; ok {
		t.Error("the item carries its body; a watch hands over the summary")
	}
	if want := `space = "OPS" AND finished = true AND id IN (`; !strings.HasPrefix(f.lastQuery(), want) {
		t.Errorf("query = %q, want it to start %q", f.lastQuery(), want)
	}
	seq, _, _ := parseWatchCursor(page.Cursor)
	if seq != len(f.log) {
		t.Errorf("cursor seq %d, want the newest change %d", seq, len(f.log))
	}
	if again := readFinished(t, c, WatchRequest{Space: "OPS", Cursor: page.Cursor}); len(again.Finished) != 0 {
		t.Errorf("a second read published %v again", ids(again))
	}
}

// An edit of a finished task is a change in the feed, and the task is still finished:
// it is read again with the same time, which the engine's mark for the item skips. A
// task reopened and finished again is finished at a later time, which it does not.
func TestAnEditIsTheSameFinishAndARefinishIsANewOne(t *testing.T) {
	f, c := readWiki(t)
	cursor := tipOf(t, c, "OPS")
	task := f.add("OPS", "task", "Zugang einrichten", nil, nil)
	f.setState(task, "done")
	first := readFinished(t, c, WatchRequest{Space: "OPS", Cursor: cursor})

	f.tick(time.Minute)
	f.edit(task, "Zugang einrichten (erledigt)")
	edited := readFinished(t, c, WatchRequest{Space: "OPS", Cursor: first.Cursor})
	if len(edited.Finished) != 1 || !edited.Finished[0].At.Equal(first.Finished[0].At) {
		t.Fatalf("after an edit: %+v, want the same task at the same time %v", edited.Finished, first.Finished[0].At)
	}

	f.tick(time.Minute)
	f.setState(task, "open")
	reopened := readFinished(t, c, WatchRequest{Space: "OPS", Cursor: edited.Cursor})
	if len(reopened.Finished) != 0 {
		t.Fatalf("a reopened task was published: %v", ids(reopened))
	}
	f.tick(time.Minute)
	f.setState(task, "done")
	again := readFinished(t, c, WatchRequest{Space: "OPS", Cursor: reopened.Cursor})
	if len(again.Finished) != 1 || !again.Finished[0].At.After(first.Finished[0].At) {
		t.Fatalf("after finishing again: %+v, want a later time than %v", again.Finished, first.Finished[0].At)
	}
}

// A task finished before the watch began and edited after it is in the feed and is
// finished — and is not published: what a watch publishes is what was finished while
// it watched.
func TestATaskFinishedBeforeTheWatchIsNotPublished(t *testing.T) {
	f, c := readWiki(t)
	old := f.add("OPS", "task", "finished before", nil, nil)
	f.setState(old, "done")
	f.tick(2 * watchSlack)
	cursor := tipOf(t, c, "OPS")
	f.tick(time.Minute)
	f.edit(old, "finished before, edited after")
	if page := readFinished(t, c, WatchRequest{Space: "OPS", Cursor: cursor}); len(page.Finished) != 0 {
		t.Errorf("published %v, finished before the watch began", ids(page))
	}
}

// The feed lists every space the account watches; a watch publishes its own space's
// tasks only, and a change with no item — a space renamed — is no task.
func TestOnlyTheWatchedSpaceIsPublished(t *testing.T) {
	f, c := readWiki(t)
	f.watch("DOC", true)
	cursor := tipOf(t, c, "OPS")
	other := f.add("DOC", "task", "elsewhere", nil, nil)
	f.setState(other, "done")
	f.spaceEvent("OPS")
	mine := f.add("OPS", "task", "here", nil, nil)
	f.setState(mine, "done")
	page := readFinished(t, c, WatchRequest{Space: "OPS", Cursor: cursor})
	if got := ids(page); len(got) != 1 || got[0] != mine.ID {
		t.Errorf("published %v, want only %s", got, mine.ID)
	}
	if strings.Contains(f.lastQuery(), `""`) {
		t.Errorf("query %q asks for an empty id", f.lastQuery())
	}
}

// A watch's own query narrows which finished tasks count; it is grouped, so it cannot
// widen what the watch asks.
func TestAQueryNarrowsTheWatch(t *testing.T) {
	f, c := readWiki(t)
	cursor := tipOf(t, c, "OPS")
	tagged := f.add("OPS", "task", "tagged", nil, nil)
	tagged.Labels = []string{"atlas"}
	f.setState(tagged, "done")
	plain := f.add("OPS", "task", "plain", nil, nil)
	f.setState(plain, "done")
	page := readFinished(t, c, WatchRequest{Space: "OPS", Query: `label = "atlas"`, Cursor: cursor})
	if got := ids(page); len(got) != 1 || got[0] != tagged.ID {
		t.Errorf("published %v, want only the labelled task", got)
	}
	if want := `(label = "atlas") AND space = "OPS" AND finished = true AND id IN (`; !strings.HasPrefix(f.lastQuery(), want) {
		t.Errorf("query = %q, want it to start %q", f.lastQuery(), want)
	}
	if _, err := c.FinishedSince(context.Background(), WatchRequest{Space: "OPS", Query: `x) OR (y`, Cursor: cursor}); err == nil {
		t.Error("a query that closes the watch's group was sent")
	}
}

// The answer is checked against the question: a task the watch did not ask about, or
// of another space, is not published because the wiki answered it.
func TestTheAnswerIsCheckedAgainstTheQuestion(t *testing.T) {
	f, c := readWiki(t)
	f.watch("DOC", true)
	cursor := tipOf(t, c, "OPS")
	unasked := f.add("OPS", "task", "finished before the page", nil, nil)
	f.setState(unasked, "done")
	page := readFinished(t, c, WatchRequest{Space: "OPS", Cursor: cursor})
	other := f.add("DOC", "task", "another space", nil, nil)
	f.setState(other, "done")
	mine := f.add("OPS", "task", "asked for", nil, nil)
	f.setState(mine, "done")
	f.mu.Lock()
	f.widen = true
	f.mu.Unlock()
	got := ids(readFinished(t, c, WatchRequest{Space: "OPS", Cursor: page.Cursor}))
	if len(got) != 1 || got[0] != mine.ID {
		t.Errorf("published %v, want only %s", got, mine.ID)
	}
}

// A page of the feed is bounded; the cursor stops at the last change read, and the
// next read continues there.
func TestAPageOfChangesIsBounded(t *testing.T) {
	f, c := readWiki(t)
	cursor := tipOf(t, c, "OPS")
	var tasks []*fakeItem
	for _, title := range []string{"a", "b", "c"} {
		task := f.add("OPS", "task", title, nil, nil)
		f.setState(task, "done")
		tasks = append(tasks, task)
	}
	// Two changes per task: made, then finished.
	first := readFinished(t, c, WatchRequest{Space: "OPS", Cursor: cursor, Limit: 3})
	if got := ids(first); len(got) != 2 || got[0] != tasks[0].ID || got[1] != tasks[1].ID {
		t.Fatalf("first page %v, want a and b", got)
	}
	second := readFinished(t, c, WatchRequest{Space: "OPS", Cursor: first.Cursor, Limit: 3})
	// b's finish is the first change of this page; publishing it again is the engine's
	// to skip.
	if got := ids(second); len(got) != 2 || got[0] != tasks[1].ID || got[1] != tasks[2].ID {
		t.Fatalf("second page %v, want b and c", got)
	}
}

// A cursor that is lost or unreadable starts the watch at the tip again rather than at
// the beginning of the feed.
func TestALostCursorStartsAtTheTip(t *testing.T) {
	f, c := readWiki(t)
	tipOf(t, c, "OPS")
	task := f.add("OPS", "task", "done meanwhile", nil, nil)
	f.setState(task, "done")
	for _, cursor := range []string{"", "garbage", "12", "x|2026-10-27T09:00:00Z", "-1|2026-10-27T09:00:00Z", "3|yesterday"} {
		page := readFinished(t, c, WatchRequest{Space: "OPS", Cursor: cursor})
		seq, _, ok := parseWatchCursor(page.Cursor)
		if len(page.Finished) != 0 || !ok || seq != len(f.log) {
			t.Errorf("cursor %q: published %v, cursor %q; want nothing and the tip", cursor, ids(page), page.Cursor)
		}
	}
}

// A wiki restored from a backup has a shorter feed than the cursor remembers; the
// watch continues from the wiki's newest change and says what it may have missed.
func TestARestoredWikiMovesTheCursorBack(t *testing.T) {
	f, c := readWiki(t)
	cursor := tipOf(t, c, "OPS")
	task := f.add("OPS", "task", "lost in the restore", nil, nil)
	f.setState(task, "done")
	page := readFinished(t, c, WatchRequest{Space: "OPS", Cursor: cursor})
	f.restore(1)
	back := readFinished(t, c, WatchRequest{Space: "OPS", Cursor: page.Cursor})
	if seq, _, _ := parseWatchCursor(back.Cursor); seq != 1 || back.Gap == "" {
		t.Errorf("after a restore: cursor %q, gap %q; want seq 1 and a gap", back.Cursor, back.Gap)
	}
	_, since, _ := parseWatchCursor(page.Cursor)
	if _, kept, _ := parseWatchCursor(back.Cursor); !kept.Equal(since) {
		t.Errorf("since moved from %v to %v; it is when the watch began", since, kept)
	}
	// The restore took the account's watch with it; the next read makes it again.
	readFinished(t, c, WatchRequest{Space: "OPS", Cursor: back.Cursor})
	if n := f.count("PUT /api/spaces/OPS/watch"); n != 2 {
		t.Errorf("watched %d times, want again after the restore", n)
	}
}

// The space is watched once, and again after a while, in case somebody stopped the
// account's watch in the wiki: a feed that has gone quiet that way looks like a quiet
// space.
func TestTheSpaceIsWatchedOnceAndAgainLater(t *testing.T) {
	f, c := readWiki(t)
	cursor := tipOf(t, c, "OPS")
	readFinished(t, c, WatchRequest{Space: "OPS", Cursor: cursor})
	readFinished(t, c, WatchRequest{Space: "OPS", Cursor: cursor})
	if n := f.count("PUT /api/spaces/OPS/watch"); n != 1 {
		t.Fatalf("watched %d times, want once", n)
	}
	f.watch("OPS", false)
	f.tick(standingFor + time.Second)
	readFinished(t, c, WatchRequest{Space: "OPS", Cursor: cursor})
	if n := f.count("PUT /api/spaces/OPS/watch"); n != 2 {
		t.Errorf("watched %d times after %v, want again", n, standingFor)
	}
	if _, err := c.WatchTip(context.Background(), "NOPE"); err == nil || !strings.Contains(err.Error(), "NOPE") || !strings.Contains(err.Error(), "viewer") {
		t.Errorf("a space the account cannot read = %v, want a refusal that names it", err)
	}
}

func TestCheckSpace(t *testing.T) {
	for _, ok := range []string{"OPS", "IT2", "A"} {
		if err := CheckSpace(ok); err != nil {
			t.Errorf("CheckSpace(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "ops", "2IT", "OPS-1", `OPS"`, "OPS AND", strings.Repeat("A", 65)} {
		if err := CheckSpace(bad); err == nil {
			t.Errorf("CheckSpace(%q) accepted it", bad)
		}
	}
}

func TestCheckWatchQuery(t *testing.T) {
	for _, ok := range []string{"", `label = "atlas"`, `(type = task OR type = bug) AND label = x`, `title ~ "a) OR (b"`, `title ~ 'order by'`, `title = "say \"hi\")"`} {
		if err := CheckWatchQuery(ok); err != nil {
			t.Errorf("CheckWatchQuery(%q) = %v", ok, err)
		}
	}
	for q, why := range map[string]string{
		`x) OR (y`:                "closes the watch's group",
		`(a`:                      "leaves a group open",
		`title = "open`:           "leaves a text open",
		`label = x ORDER BY key`:  "orders what the watch orders",
		`label = x order  by key`: "orders, in lower case",
	} {
		if err := CheckWatchQuery(q); err == nil {
			t.Errorf("CheckWatchQuery(%q) accepted a query that %s", q, why)
		}
	}
}
