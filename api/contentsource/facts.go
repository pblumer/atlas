package contentsource

import (
	"context"
	"errors"
	"time"
)

// What an adapter must establish about a page and about the account Atlas reads
// with (§ 10). These types are the whole of the contract between an adapter and
// the rules: the rules never see a source's own JSON, so a second adapter changes
// nothing here.

// Fact is one security fact about a page, as far as its adapter could establish
// it.
//
// It is three-valued on purpose, and the zero value is the third. § 10 says "a
// fact an adapter cannot establish counts as present", and a boolean cannot say
// that: false would read as "established absent" for every field an adapter
// forgot to fill. With Unknown as the zero value, a forgotten field refuses the
// page — the failure is a refusal somebody reads, not content in the shop.
type Fact int

const (
	// Unknown is a fact the adapter could not establish. The rules treat it as
	// present.
	Unknown Fact = iota
	// Absent is a fact the adapter established does not hold.
	Absent
	// Present is a fact the adapter established holds.
	Present
)

// Held reports whether the rules must treat this fact as holding: present, or
// not established.
func (f Fact) Held() bool { return f != Absent }

// FactOf is a fact the adapter established either way.
func FactOf(holds bool) Fact {
	if holds {
		return Present
	}
	return Absent
}

// BlockKind is what a block of a page's body is, in the vocabulary the
// conversion understands. An adapter maps its source's kinds onto these, and
// everything it cannot map is [BlockOther], which is never taken.
type BlockKind string

const (
	// BlockParagraph, BlockHeading and the list items are the text § 7 takes.
	BlockParagraph BlockKind = "paragraph"
	BlockHeading   BlockKind = "heading"
	BlockBullet    BlockKind = "bullet"
	BlockOrdered   BlockKind = "ordered"
	// The blocks the rich description takes besides the text (amendment of
	// 2026-10-28): a quote, code, a panel with its tone, a table whose text holds
	// its rows (one per line) and cells (separated by tabs), a divider, an image
	// attachment of the page (Ref names it) and another item embedded (Ref names
	// it). The plain description still takes only the text kinds above.
	BlockQuote   BlockKind = "quote"
	BlockCode    BlockKind = "code"
	BlockPanel   BlockKind = "panel"
	BlockTable   BlockKind = "table"
	BlockDivider BlockKind = "divider"
	BlockImage   BlockKind = "image"
	BlockEmbed   BlockKind = "embed"
	// BlockOther is everything else — videos, models, computed lists, a kind
	// added to the source later. Dropped and reported, never converted.
	BlockOther BlockKind = "other"
)

// MarkKind is what a mark over a block's text is. Only the two that remove text
// matter to the conversion; every other mark (bold, a link) keeps its text and
// loses its formatting or target.
type MarkKind string

const (
	// MarkPassage is a protected passage (its ADR-0088). Its text is removed
	// whatever the account could read.
	MarkPassage MarkKind = "passage"
	// MarkMention is a mention of a person or an item. Its text is removed: a
	// mention of somebody is not a description of a service.
	MarkMention MarkKind = "mention"
	// MarkOther keeps its text.
	MarkOther MarkKind = "other"
)

// Mark is one range over a block's text, in Unicode code points (runes) — the
// unit wikiwikibangbang counts its marks in.
type Mark struct {
	Kind  MarkKind
	Start int
	End   int
	// Ref names the passage a MarkPassage protects.
	Ref string
}

// Block is one block of a body.
type Block struct {
	Kind BlockKind
	// Source is the source's own name for the block's kind, for the preview's
	// account of what was dropped.
	Source string
	Text   string
	Marks  []Mark
	// Ref names what an image (an attachment of the page) or an embed (another
	// item) shows.
	Ref string
	// Level is a heading's level, 1 to 3; Tone a panel's tone; Checked whether a
	// task is done; Align and Width how an image or a paragraph is laid out.
	Level   int
	Tone    string
	Checked bool
	Align   string
	Width   int
}

// Page is one page as its adapter read it, with the security facts of § 8.
type Page struct {
	ID      string
	Key     string
	Title   string
	Space   string
	Parent  string
	URL     string
	Version int
	State   string
	Labels  []string
	// Properties are the page's typed properties, as the source answered them.
	Properties map[string]any
	Body       []Block

	// ReadRestricted is a read restriction on the page itself, whoever it
	// names — including the account (§ 8).
	ReadRestricted Fact
	// Held is an open data protection hold.
	Held Fact
	// Withdrawn is a page not live: trashed, purged or redacted.
	Withdrawn Fact
	// Presence is the source's own word for where the page stands, for the
	// refusal's sentence.
	Presence string
}

// SpaceGrant is one role the account holds in one space, directly or through a
// group.
type SpaceGrant struct {
	Space string
	Role  string
	Via   string
}

// Standing is the account's own standing at the source (§ 4).
//
// The two Known flags are the three-valued rule for a list: an empty list the
// source answered and a list it never answered are different statements, and only
// the first one is "none".
type Standing struct {
	// Scope is the token's scope; only "read" passes.
	Scope            string
	SystemRoles      []string
	SystemRolesKnown bool
	Spaces           []SpaceGrant
	SpacesKnown      bool
}

// Hit is one search result (§ 5).
type Hit struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Title string `json:"title"`
	Space string `json:"space"`
	State string `json:"state,omitempty"`
	URL   string `json:"url,omitempty"`
}

// Change is one entry of the source's change feed (§ 11).
type Change struct {
	Seq int64
	// Item is the changed page's id, or empty for "a watched page you can no
	// longer read changed" — which names nothing, so everything is rechecked.
	Item string
}

// Changes is one delivery of the change feed: what changed after the cursor, and
// the cursor to ask from next.
type Changes struct {
	Changes []Change
	Last    int64
}

// ErrUnreadable is a page the account cannot read, or that is gone. The source
// does not say which, and must not have to: an unreadable item is non-existent
// to whoever cannot read it (its D10), and either way the page is refused.
var ErrUnreadable = errors.New("the page cannot be read")

// Adapter is one source kind (§ 10). Every method talks to the source and none
// may be called on the run loop.
//
// A new adapter is admitted only when it proves the facts the rules read, and
// where its source has no equivalent of a protection it reports the fact as
// [Unknown] — which refuses — rather than as absent.
type Adapter interface {
	// Standing reads the account's own standing.
	Standing(ctx context.Context) (Standing, error)
	// Search lists pages matching a query.
	Search(ctx context.Context, q string) ([]Hit, error)
	// Resolve turns what a product manager pasted — an address, a key or an id
	// — into the page's stable id. An address of another host than the source's
	// is refused.
	Resolve(ctx context.Context, input string) (string, error)
	// Page reads one page by id, with its security facts.
	Page(ctx context.Context, id string) (Page, error)
	// Ancestors reads the whole parent chain of a page, nearest first. An
	// ancestor the account cannot read is [ErrUnreadable].
	Ancestors(ctx context.Context, p Page) ([]Page, error)
	// Children lists a page's readable child pages, by id, in sibling order.
	Children(ctx context.Context, p Page) ([]string, error)
	// Attachment reads the file of a page named name — or whose id is name, as
	// an image block names it — at most max bytes, with the media type the
	// source gives it.
	Attachment(ctx context.Context, pageID, name string, max int64) ([]byte, string, error)
	// Watch asks the source to report changes to a page in its feed.
	Watch(ctx context.Context, id string) error
	// Unwatch ends the watch of a page Atlas no longer holds anything of, by
	// id — which ends it even when the page can no longer be read.
	Unwatch(ctx context.Context, id string) error
	// Head is the feed's current position, from which no change is yet known.
	Head(ctx context.Context) (int64, error)
	// Changes waits up to wait for changes after the cursor.
	Changes(ctx context.Context, after int64, wait time.Duration) (Changes, error)
}
