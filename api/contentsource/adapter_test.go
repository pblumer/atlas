package contentsource

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The wikiwikibangbang adapter against a source that misbehaves in ways the fake
// does not: answers that are not JSON, too long, without a feed position, redirects
// that never end, and attachments that are missing, failing or too large. Each must
// be a named error or "unreadable" — never a silent success Atlas would trust.

// scripted is a TLS source whose every route is a test's handler.
func scripted(t *testing.T, routes map[string]http.HandlerFunc) *WikiWikiBangBang {
	t.Helper()
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h, ok := routes[r.URL.Path]; ok {
			h(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)
	ad, err := NewWikiWikiBangBang(ts.URL, "tok", ts.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	return ad
}

func answer(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

func status(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
}

func TestAnAnswerThatIsNotWhatItShouldBeIsAnError(t *testing.T) {
	ad := scripted(t, map[string]http.HandlerFunc{
		"/api/me": answer(`{"scope":`),
		"/api/changes": func(w http.ResponseWriter, _ *http.Request) {
			// Longer than any answer the adapter will read.
			_, _ = w.Write([]byte(strings.Repeat(" ", maxResponse+1)))
		},
	})
	if _, err := ad.Standing(ctx(t)); err == nil || !strings.Contains(err.Error(), "not what it should be") {
		t.Fatalf("broken JSON: %v", err)
	}
	if _, err := ad.Head(ctx(t)); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized answer: %v", err)
	}
}

func TestAFeedWithoutAPositionIsAnError(t *testing.T) {
	ad := scripted(t, map[string]http.HandlerFunc{"/api/changes": answer(`{"changes":[]}`)})
	if _, err := ad.Head(ctx(t)); err == nil || !strings.Contains(err.Error(), "no position") {
		t.Fatalf("head: %v", err)
	}
	if _, err := ad.Changes(ctx(t), 3, time.Second); err == nil || !strings.Contains(err.Error(), "no position") {
		t.Fatalf("changes: %v", err)
	}
	failing := scripted(t, map[string]http.HandlerFunc{"/api/changes": status(http.StatusInternalServerError)})
	if _, err := failing.Head(ctx(t)); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("head 500: %v", err)
	}
}

func TestARedirectWithinTheSourceIsFollowedOnlyAFewTimes(t *testing.T) {
	ad := scripted(t, map[string]http.HandlerFunc{
		"/api/me": func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/api/me", http.StatusFound) },
	})
	if _, err := ad.Standing(ctx(t)); err == nil || !strings.Contains(err.Error(), "too often") {
		t.Fatalf("redirect loop: %v", err)
	}
}

func TestAnAttachmentIsTakenOnlyWhenItIsThereAndSmallEnough(t *testing.T) {
	list := answer(`{"attachments":[{"id":"att_1","name":"logo.png","type":"image/png"},` +
		`{"id":"att_2","name":"gone.png","type":"image/png"},{"id":"att_3","name":"broken.png","type":"image/png"},` +
		`{"id":"bad id","name":"odd.png","type":"image/png"}]}`)
	ad := scripted(t, map[string]http.HandlerFunc{
		"/api/items/it_a/attachments": list,
		"/api/items/it_a/attachments/att_1": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte("PNGDATA"))
		},
		"/api/items/it_a/attachments/att_3": status(http.StatusInternalServerError),
		"/api/items/it_b/attachments":       status(http.StatusInternalServerError),
	})
	data, typ, err := ad.Attachment(ctx(t), "it_a", "logo.png", 100)
	if err != nil || string(data) != "PNGDATA" || typ != "image/png" {
		t.Fatalf("taken = %q %q %v (an octet-stream answer keeps the listing's type)", data, typ, err)
	}
	if _, _, err := ad.Attachment(ctx(t), "it_a", "logo.png", 3); !errors.Is(err, errTooLarge) {
		t.Fatalf("too large: %v", err)
	}
	for name, want := range map[string]error{"missing.png": ErrUnreadable, "gone.png": ErrUnreadable, "odd.png": ErrUnreadable} {
		if _, _, err := ad.Attachment(ctx(t), "it_a", name, 100); !errors.Is(err, want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, _, err := ad.Attachment(ctx(t), "it_a", "broken.png", 100); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("a failing download: %v", err)
	}
	if _, _, err := ad.Attachment(ctx(t), "it_b", "logo.png", 100); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("a failing listing: %v", err)
	}
	if _, _, err := ad.Attachment(ctx(t), "../etc", "logo.png", 100); !errors.Is(err, ErrUnreadable) {
		t.Errorf("a page id that is not an id: %v", err)
	}
}

func TestAWatchNamesOnlyAnIdAndAnEndedWatchIsDone(t *testing.T) {
	ad := scripted(t, map[string]http.HandlerFunc{
		"/api/items/it_a/watch": status(http.StatusOK),
		"/api/items/it_b/watch": status(http.StatusInternalServerError),
	})
	for _, bad := range []string{"../x", "a b", ""} {
		if err := ad.Watch(ctx(t), bad); !errors.Is(err, ErrUnreadable) {
			t.Errorf("watch %q: %v", bad, err)
		}
		if err := ad.Unwatch(ctx(t), bad); !errors.Is(err, ErrUnreadable) {
			t.Errorf("unwatch %q: %v", bad, err)
		}
	}
	if err := ad.Watch(ctx(t), "it_a"); err != nil {
		t.Fatalf("watch: %v", err)
	}
	// No such watch any more: the state asked for holds.
	if err := ad.Unwatch(ctx(t), "it_gone"); err != nil {
		t.Fatalf("unwatch of a watch that is gone: %v", err)
	}
	if err := ad.Unwatch(ctx(t), "it_b"); err == nil {
		t.Fatal("a failing unwatch reported success")
	}
}

func TestTheStandingTakesSystemRolesFromTheFlagsAndFailsOnABrokenRolesRoute(t *testing.T) {
	ad := scripted(t, map[string]http.HandlerFunc{
		"/api/me":       answer(`{"scope":"read","admin":false,"erasure":true}`),
		"/api/me/roles": answer(`{"system":[],"spaces":[{"space":"SVC","role":"viewer"}]}`),
	})
	st, err := ad.Standing(ctx(t))
	if err != nil || !st.SystemRolesKnown || len(st.SystemRoles) != 1 || st.SystemRoles[0] != "erasure" {
		t.Fatalf("an erasure flag the roles route does not list = %+v %v", st, err)
	}
	broken := scripted(t, map[string]http.HandlerFunc{
		"/api/me":       answer(`{"scope":"read","admin":false,"erasure":false}`),
		"/api/me/roles": status(http.StatusInternalServerError),
	})
	if _, err := broken.Standing(ctx(t)); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("a failing roles route = %v", err)
	}
}

func TestOnlyParagraphsHeadingsAndListsAreTextBlocks(t *testing.T) {
	for kind, want := range map[string]BlockKind{"paragraph": BlockParagraph, "heading": BlockHeading,
		"bullet": BlockBullet, "task": BlockBullet, "ordered": BlockOrdered, "quote": BlockOther, "invented": BlockOther} {
		if got := blockKind(kind); got != want {
			t.Errorf("%s = %v, want %v", kind, got, want)
		}
	}
}

func TestAPageWithoutAnIdOrASpaceIsNotTrusted(t *testing.T) {
	ad := scripted(t, map[string]http.HandlerFunc{
		"/api/items/it_x": answer(`{"item":{"key":"X-1","title":"no id"}}`),
		"/api/search":     status(http.StatusInternalServerError),
	})
	if _, err := ad.Page(ctx(t), "it_x"); err == nil || !strings.Contains(err.Error(), "without an id") {
		t.Fatalf("page without id = %v", err)
	}
	if _, err := ad.Resolve(ctx(t), "it_x"); err == nil || !strings.Contains(err.Error(), "without an id") {
		t.Fatalf("resolve without id = %v", err)
	}
	if _, err := ad.Page(ctx(t), "../x"); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("a ref that is not an id = %v", err)
	}
	if _, err := ad.Children(ctx(t), Page{ID: "it_x", Space: "a/b"}); err == nil || !strings.Contains(err.Error(), "cannot be listed") {
		t.Fatalf("a space that is not a name = %v", err)
	}
	if _, err := ad.Search(ctx(t), "x"); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("a failing search = %v", err)
	}
	for _, in := range []string{"https://[::1", "x y"} {
		if _, err := ad.Resolve(ctx(t), in); err == nil {
			t.Errorf("resolve %q succeeded", in)
		}
	}
}

func TestATreeThatNeverEndsIsRefused(t *testing.T) {
	n := 0
	ad := scripted(t, map[string]http.HandlerFunc{
		"/api/spaces/SVC/tree": func(w http.ResponseWriter, _ *http.Request) {
			n++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"items":[{"id":"it_` + strings.Repeat("a", n) + `"}],"next":"more"}`))
		},
	})
	if _, err := ad.Children(ctx(t), Page{ID: "it_p", Space: "SVC"}); err == nil {
		t.Fatal("a tree whose cursor never ends was accepted")
	}
	failing := scripted(t, map[string]http.HandlerFunc{"/api/spaces/SVC/tree": status(http.StatusBadGateway)})
	if _, err := failing.Children(ctx(t), Page{ID: "it_p", Space: "SVC"}); err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("a failing tree = %v", err)
	}
}
