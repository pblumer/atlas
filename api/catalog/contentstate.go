package catalog

import (
	"github.com/pblumer/atlas/api/contentsource"
)

// What the recheck found about a bound product, and the one exception to a frozen
// release (§ 9 of ADR-draft-service-content-from-an-external-source).
//
// A release is frozen, and a page can be restricted after its content was copied.
// So the content a release holds is shown only while the recheck says it may be,
// and that is read at the moment it is shown — from this record, never from the
// source — which is what lets it take effect without a publish. Price and rules
// stay exactly as frozen; only the showing of the source's content is withdrawn.

// ContentState is one bound product's recheck record.
type ContentState struct {
	ItemID string `json:"itemId"`
	Source string `json:"source,omitempty"`
	// LastChecked is when the recheck last ran for it, and LastPassed when it
	// last passed, in the server's Unix nanoseconds.
	LastChecked int64 `json:"lastChecked,omitempty"`
	LastPassed  int64 `json:"lastPassed,omitempty"`
	// Quarantine is set from the first failed recheck until a refresh passes.
	// A recheck that passes again on its own does not lift it: the content
	// Atlas holds is the content that was in question.
	Quarantine *Quarantine `json:"quarantine,omitempty"`
	// Distrusted is the start of the latest quarantine a refresh lifted. Content
	// copied before it is the content that was in question, and it stays
	// withheld wherever it still is — in the releases published before the
	// refresh — while the refreshed copy, taken after, is shown.
	Distrusted int64 `json:"distrusted,omitempty"`
}

// Quarantine is why and since when a product's source content is withheld.
type Quarantine struct {
	Since  int64  `json:"since"`
	Reason string `json:"reason"`
}

// RecheckInterval is the sweep's period and the age a passed recheck may reach
// before publishing refuses (§ 9).
var RecheckInterval = int64(contentsource.RecheckInterval)

// staleAfter is the age past which a passed recheck no longer vouches for content
// at all. Twice the interval, so a single late sweep withholds nothing, and a
// recheck that has stopped running — the goroutine gone, the server down for an
// hour — withholds everything rather than vouching for content nobody asked
// about. It is not a quarantine: the next recheck that passes shows it again.
var staleAfter = 2 * RecheckInterval

// ContentState reads one product's recheck record.
func (s *Store) ContentState(itemID string) (ContentState, bool, error) {
	return s.contents.Get(itemID)
}

// SaveContentState writes one.
func (s *Store) SaveContentState(c ContentState) error { return s.contents.Save(c) }

// contentShown reports whether the source content a binding holds may be shown
// outside the catalogue screen at now.
//
// A product with no binding is shown: its content is the product manager's. A
// bound one is shown only when every condition holds — a record exists, no
// quarantine, the copy was taken after the latest lifted quarantine began, and a
// recheck passed recently. Each missing fact withholds: the record exists to
// prove the content may be shown, and its absence proves nothing.
func contentShown(b *ContentSource, st ContentState, found bool, now int64) bool {
	if b == nil {
		return true
	}
	switch {
	case !found, st.Quarantine != nil:
		return false
	case b.Oldest() <= st.Distrusted:
		return false
	case st.LastPassed == 0 || now-st.LastPassed > staleAfter:
		return false
	}
	return true
}

// withheld is a release's item with its source content removed: the
// descriptions, the keywords and the binding. The name and the price stay, so an
// order and an approval stay readable.
func withheld(it Item) Item {
	it.Descriptions = nil
	it.Keywords = nil
	it.ContentSource = nil
	return it
}

// publicBinding is the part of a binding a reader outside the catalogue screen
// may see: the address of each page where the source lets the shop link to it,
// and nothing otherwise. The rest — page ids, versions, the passages removed —
// is maintenance detail.
func publicBinding(b *ContentSource, linkInShop bool) *ContentSource {
	if b == nil || !linkInShop {
		return nil
	}
	out := &ContentSource{Source: b.Source, Primary: b.Primary, Pages: map[string]PageRef{}}
	for lang, p := range b.Pages {
		out.Pages[lang] = PageRef{Ref: p.Ref, URL: p.URL, Title: p.Title}
	}
	return out
}

// pictureShown reports whether a product's picture may be served. A picture is
// not frozen into a release (ADR-0391): there is one file per product, so the
// question is about the product, not about a release. A quarantine withholds it,
// and so does a bound product whose recheck has gone stale.
func pictureShown(it Item, st ContentState, found bool, now int64) bool {
	if found && st.Quarantine != nil {
		return false
	}
	if it.ContentSource == nil {
		return true
	}
	return found && st.LastPassed != 0 && now-st.LastPassed <= staleAfter
}

// contentProblems is the publish gate of § 9: a bound product refuses while it is
// quarantined, while its copy predates a lifted quarantine, or while no recheck
// passed within the last interval. It reads Atlas's own record only. Runs on the
// loop.
func (s *Service) contentProblems(items []Item) ([]Problem, error) {
	var out []Problem
	now := s.now()
	for _, it := range items {
		if it.ContentSource == nil {
			continue
		}
		st, found, err := s.store.ContentState(it.ID)
		if err != nil {
			return nil, err
		}
		switch {
		case found && st.Quarantine != nil:
			out = append(out, Problem{Item: it.ID, Message: "is quarantined — " + st.Quarantine.Reason +
				". Refresh it once its pages pass again, or unlink it"})
		case found && it.ContentSource.Oldest() <= st.Distrusted:
			out = append(out, Problem{Item: it.ID, Message: "holds content copied before its last quarantine; refresh it"})
		case !found || st.LastPassed == 0 || now-st.LastPassed > RecheckInterval:
			out = append(out, Problem{Item: it.ID, Message: "has no passed recheck of its content source within the last " +
				"15 minutes; the source may be unreachable — refresh it, or wait for the next recheck"})
		}
	}
	return out, nil
}

// withholdReleases applies the quarantine to a catalogue's releases as they are
// read (§ 9). Only the newest release's source content can be shown — it is the
// one the recheck covers — and only while [contentShown] says so; every older
// release's bound content is withheld for good. Unbound products are untouched.
// Runs on the loop.
func (s *Service) withholdReleases(rels []Release, linkInShop map[string]bool) ([]Release, error) {
	now := s.now()
	states := map[string]ContentState{}
	known := map[string]bool{}
	for i := range rels {
		var items []Item
		for _, it := range rels[i].Items {
			if it.ContentSource == nil {
				items = append(items, it)
				continue
			}
			if _, read := known[it.ID]; !read {
				st, found, err := s.store.ContentState(it.ID)
				if err != nil {
					return nil, err
				}
				states[it.ID], known[it.ID] = st, found
			}
			if i > 0 || !contentShown(it.ContentSource, states[it.ID], known[it.ID], now) {
				items = append(items, withheld(it))
				continue
			}
			it.ContentSource = publicBinding(it.ContentSource, linkInShop[it.ContentSource.Source])
			items = append(items, it)
		}
		rels[i].Items = items
	}
	return rels, nil
}

// linksInShop is which sources let the shop link to a page (§ 3). Off the loop.
func (s *Service) linksInShop() (map[string]bool, error) {
	out := map[string]bool{}
	if s.Content == nil {
		return out, nil
	}
	all, err := s.Content.Sources()
	if err != nil {
		return nil, err
	}
	for _, src := range all {
		out[src.ID] = src.LinkInShop
	}
	return out, nil
}

// exportable is a product as an export carries it. A document leaves Atlas, so
// it is outside the catalogue screen: the binding is this installation's and
// stays behind, and source content the shop would not show now is not carried
// either. Runs on the loop.
func (s *Service) exportable(it Item) (Item, error) {
	if it.ContentSource == nil {
		return exported(it), nil
	}
	st, found, err := s.store.ContentState(it.ID)
	if err != nil {
		return Item{}, err
	}
	if !contentShown(it.ContentSource, st, found, s.now()) {
		it = withheld(it)
	}
	it.ContentSource = nil
	return exported(it), nil
}
