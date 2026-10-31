package catalog

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/contentsource/wikifake"
)

// The wiki's services as building blocks (tshopper phase A, TSH3).
//
// A product owner describes a service in the wiki; a product manager composes
// offerings from those descriptions. The building-block list is the wiki searched
// from the catalogue's side: every page a search finds, and which products already
// use it — named where the reader maintains them, counted where they do not.

type blockResp struct {
	Source string `json:"source"`
	Blocks []struct {
		ID            string   `json:"id"`
		Key           string   `json:"key"`
		Title         string   `json:"title"`
		UsedBy        []string `json:"usedBy"`
		UsedElsewhere int      `json:"usedElsewhere"`
	} `json:"blocks"`
}

func TestBuildingBlocksAreTheWikiWithWhatUsesIt(t *testing.T) {
	b := newBoundFixture(t)
	search := func(p string) blockResp {
		t.Helper()
		who := b.owner
		if p == "stranger" {
			who = user("usr_stranger")
		}
		rec := call(t, b.s.HandleBuildingBlocks, who, "GET", "/x?source=wiki&q=VPN", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("blocks = %d %s", rec.Code, rec.Body)
		}
		var out blockResp
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	byKey := func(r blockResp, key string) (used []string, elsewhere int, found bool) {
		for _, bl := range r.Blocks {
			if bl.Key == key {
				return bl.UsedBy, bl.UsedElsewhere, true
			}
		}
		return nil, 0, false
	}

	rec := call(t, b.s.HandleBuildingBlocks, b.owner, "GET", "/x", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"sources":[{"id":"wiki","name":"Wiki"`) ||
		strings.Contains(rec.Body.String(), "wiki-token") {
		t.Fatalf("the sources to choose from = %d %s", rec.Code, rec.Body)
	}

	before := search("owner")
	if before.Source != "wiki" {
		t.Errorf("source = %q", before.Source)
	}
	if used, _, ok := byKey(before, "SVC-12"); !ok || len(used) != 0 {
		t.Fatalf("before linking, SVC-12 = %v (found %v), want found and unused", used, ok)
	}
	b.linkVPN(t)
	after := search("owner")
	if used, _, _ := byKey(after, "SVC-12"); len(used) != 1 || used[0] != "vpn" {
		t.Errorf("after linking, SVC-12 is used by %v, want vpn", used)
	}
	// Somebody who maintains none of the products using it learns that it is used,
	// not by what.
	other := search("stranger")
	if used, elsewhere, _ := byKey(other, "SVC-12"); len(used) != 0 || elsewhere != 1 {
		t.Errorf("a stranger sees usedBy %v, elsewhere %d; want none and 1", used, elsewhere)
	}

	for target, want := range map[string]int{"/x?q=VPN": 400, "/x?source=wiki": 400, "/x?source=nope&q=a": 400} {
		if rec := call(t, b.s.HandleBuildingBlocks, b.owner, "GET", target, ""); rec.Code != want {
			t.Errorf("%s = %d, want %d", target, rec.Code, want)
		}
	}
}

func TestBuildingBlocksWithNoSource(t *testing.T) {
	s := serviceWithAdmin(t)
	if rec := call(t, s.HandleBuildingBlocks, user("usr_a"), "GET", "/x?source=wiki&q=a", ""); rec.Code != http.StatusNotFound {
		t.Errorf("no source configured = %d, want 404", rec.Code)
	}
}

func TestBuildingBlocksSayWhenTheSourceCannotBeSearched(t *testing.T) {
	b := newBoundFixture(t)
	b.wiki.Set(func(s *wikifake.Server) { s.Down = true })
	if rec := call(t, b.s.HandleBuildingBlocks, b.owner, "GET", "/x?source=wiki&q=VPN", ""); rec.Code != http.StatusBadGateway {
		t.Errorf("a source that is down = %d, want 502", rec.Code)
	}
}

// A binding that names the same page in two languages uses it once, and a slot with
// no page is not a use at all.
func TestAPageBoundTwiceByOneProductCountsOnce(t *testing.T) {
	b := newBoundFixture(t)
	b.s.loop.Do(func() {
		it, _, _ := b.s.store.Item("mail")
		it.ContentSource = &ContentSource{Source: "wiki", Primary: "de", Pages: map[string]PageRef{
			"de": {Ref: "it_de"}, "fr": {Ref: "it_de"}, "it": {Ref: ""}}}
		_ = b.s.store.SaveItem(it)
	})
	rec := call(t, b.s.HandleBuildingBlocks, b.owner, "GET", "/x?source=wiki&q=VPN", "")
	var out blockResp
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	for _, bl := range out.Blocks {
		if bl.Key == "SVC-12" && (len(bl.UsedBy) != 1 || bl.UsedBy[0] != "mail") {
			t.Errorf("SVC-12 used by %v, want mail once", bl.UsedBy)
		}
	}
}
