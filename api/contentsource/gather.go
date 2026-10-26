package contentsource

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/pblumer/atlas/api/brandimage"
)

// Reading a service's content, and rechecking it.
//
// Both walk the same facts and apply the same rules. They differ in what they
// take: a fetch takes the text, a recheck only asks whether what was taken may
// still be shown (§ 9 — "the protection of every bound page, not its content").

// maxAncestors bounds a parent chain. A chain longer than this is either a cycle
// the source should not have or a tree nobody can reason about; either way the
// chain cannot be established, and the page is refused.
const maxAncestors = 64

// maxBelow bounds how many child pages one language page contributes. Past it the
// description is a handbook, not a description, and a fetch that walks it would
// hold a request open for as long as the walk takes.
const maxBelow = 500

// Taken is one page whose text a description holds.
type Taken struct {
	ID       string   `json:"ref"`
	Key      string   `json:"key,omitempty"`
	Title    string   `json:"title,omitempty"`
	URL      string   `json:"url,omitempty"`
	Version  int      `json:"version"`
	Passages []string `json:"passages,omitempty"`
}

// Dropped is something a description does not carry, and why.
type Dropped struct {
	// Page names the page it was on, by key where it has one.
	Page string `json:"page"`
	// What is "page" for a child page dropped with everything below it,
	// "passage" for a protected passage removed, or the source's kind of a block
	// not taken.
	What    string   `json:"what"`
	Reasons []string `json:"reasons,omitempty"`
}

// Language is one language page as a fetch read it (§ 5, § 7).
type Language struct {
	// Input is what was asked for — an address, a key or an id.
	Input string `json:"input"`
	// Page is the language page itself.
	Page  Taken  `json:"page"`
	Space string `json:"space,omitempty"`
	State string `json:"state,omitempty"`
	// Labels are the page's own labels, as the source answered them.
	Labels []string `json:"labels,omitempty"`
	// Children are the child pages taken, depth first in sibling order.
	Children []Taken `json:"children,omitempty"`
	// Text is the description: the page's body followed by its children's.
	Text string `json:"text"`
	// Dropped is everything the rules or the conversion left out.
	Dropped []Dropped `json:"dropped,omitempty"`
	// Refusals refuse the page. Empty is the only pass.
	Refusals []string `json:"refusals,omitempty"`

	page Page
}

// Picture is the first picture the primary page names, copied out of the source
// so the shop never asks the source for anything (ADR-0391).
type Picture struct {
	Name string
	Type string
	Data []byte
}

// Content is everything a binding supplies: a description per language, the
// keywords, and the picture.
type Content struct {
	Languages map[string]Language
	Keywords  []string
	Picture   *Picture
}

// Refused is a fetch that may not be written, with every reason at once: a
// product manager who fixes one refusal per attempt eventually works around the
// gate.
type Refused struct {
	// Problems name the slot (a language tag, or "" for the whole binding) and
	// why.
	Problems []SlotProblem
}

// SlotProblem is one reason a binding cannot be written.
type SlotProblem struct {
	Lang    string `json:"lang,omitempty"`
	Page    string `json:"page,omitempty"`
	Message string `json:"message"`
}

func (r *Refused) Error() string {
	var parts []string
	for _, p := range r.Problems {
		s := p.Message
		if p.Page != "" {
			s = "page " + p.Page + ": " + s
		}
		if p.Lang != "" {
			s = p.Lang + ": " + s
		}
		parts = append(parts, s)
	}
	return "refused: " + strings.Join(parts, "; ")
}

// pages is a per-call cache of the pages read, so a chain shared by twenty pages
// is read once. It lives as long as one fetch or one recheck and no longer: a
// page read a minute ago is a fact a minute old.
type pages struct {
	ad   Adapter
	seen map[string]Page
}

func newPages(ad Adapter) *pages { return &pages{ad: ad, seen: map[string]Page{}} }

func (c *pages) page(ctx context.Context, id string) (Page, error) {
	if p, ok := c.seen[id]; ok {
		return p, nil
	}
	p, err := c.ad.Page(ctx, id)
	if err != nil {
		return Page{}, err
	}
	c.seen[id] = p
	return p, nil
}

// chain is the page's parent chain through the cache.
func (c *pages) chain(ctx context.Context, p Page) ([]Page, error) {
	return WalkAncestors(ctx, c.page, p)
}

// WalkAncestors reads a page's parent chain, nearest first, one page at a time.
// It is what an adapter's Ancestors is made of, and what a recheck uses through
// its cache. A cycle or a chain past [maxAncestors] cannot be established, and is
// an error rather than a shorter chain.
func WalkAncestors(ctx context.Context, read func(context.Context, string) (Page, error), p Page) ([]Page, error) {
	var out []Page
	seen := map[string]bool{p.ID: true}
	for parent := p.Parent; parent != ""; {
		if seen[parent] || len(out) >= maxAncestors {
			return nil, fmt.Errorf("%w: of %s", errChain, name(p))
		}
		seen[parent] = true
		a, err := read(ctx, parent)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
		parent = a.Parent
	}
	return out, nil
}

// Preview reads one language page as a fetch would take it, with everything the
// rules would drop and every reason they refuse (§ 5). It never fails on a rule:
// a refusal is part of the answer. It fails on a source it cannot talk to.
func Preview(ctx context.Context, ad Adapter, st Standing, input string) (Language, error) {
	return preview(ctx, newPages(ad), st, input)
}

func preview(ctx context.Context, c *pages, st Standing, input string) (Language, error) {
	l := Language{Input: input}
	id, err := c.ad.Resolve(ctx, input)
	switch {
	case errors.Is(err, ErrUnreadable):
		l.Refusals = []string{"the page cannot be read: it does not exist, or the account may not read it"}
		return l, nil
	case err != nil:
		return l, err
	}
	p, err := c.page(ctx, id)
	switch {
	case errors.Is(err, ErrUnreadable):
		l.Refusals = []string{"the page cannot be read: it does not exist, or the account may not read it"}
		return l, nil
	case err != nil:
		return l, err
	}
	l.page, l.Space, l.State, l.Labels = p, p.Space, p.State, p.Labels
	l.Page = Taken{ID: p.ID, Key: p.Key, Title: p.Title, URL: p.URL, Version: p.Version}

	anc, err := c.chain(ctx, p)
	if err != nil {
		if !unestablished(err) {
			return l, err
		}
		l.Refusals = append(l.Refusals, "an ancestor of the page cannot be read, so whether it restricts the page cannot be established")
	}
	l.Refusals = append(l.Refusals, CheckPage(p, anc, st)...)
	conv := Convert(p)
	l.Refusals = append(l.Refusals, conv.Problems...)
	if len(l.Refusals) > 0 {
		return l, nil
	}
	l.Page.Passages = conv.Passages
	l.Dropped = append(l.Dropped, droppedOf(p, conv)...)

	parts := []string{conv.Text}
	below := 0
	var walk func(parent Page, chain []Page) error
	walk = func(parent Page, chain []Page) error {
		ids, err := c.ad.Children(ctx, parent)
		if err != nil {
			return err
		}
		for _, cid := range ids {
			if below++; below > maxBelow {
				return errTooMany
			}
			child, err := c.page(ctx, cid)
			if errors.Is(err, ErrUnreadable) {
				l.Dropped = append(l.Dropped, Dropped{Page: cid, What: "page",
					Reasons: []string{"the page cannot be read"}})
				continue
			}
			if err != nil {
				return err
			}
			// The chain is the walk's own, which is exactly the child's when the
			// source agrees the child is below its parent. When it does not, the
			// chain is read again rather than assumed.
			childChain := append([]Page{parent}, chain...)
			if child.Parent != parent.ID {
				if childChain, err = c.chain(ctx, child); err != nil {
					l.Dropped = append(l.Dropped, Dropped{Page: name(child), What: "page",
						Reasons: []string{"its parent chain cannot be established"}})
					continue
				}
			}
			why := CheckPage(child, childChain, st)
			cc := Convert(child)
			why = append(why, cc.Problems...)
			if strings.ContainsRune(child.Title, Placeholder) {
				why = append(why, "the title holds the placeholder "+string(Placeholder))
			}
			if len(why) > 0 {
				// Dropped with everything below it: a restriction is inherited
				// downwards, and a held or withdrawn page's children are what it
				// was hiding them under.
				l.Dropped = append(l.Dropped, Dropped{Page: name(child), What: "page", Reasons: why})
				continue
			}
			l.Children = append(l.Children, Taken{ID: child.ID, Key: child.Key, Title: child.Title,
				URL: child.URL, Version: child.Version, Passages: cc.Passages})
			l.Dropped = append(l.Dropped, droppedOf(child, cc)...)
			if text := strings.TrimSpace(child.Title + "\n\n" + cc.Text); text != "" {
				parts = append(parts, text)
			}
			if err := walk(child, childChain); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(p, anc); err != nil {
		if errors.Is(err, errTooMany) {
			l.Refusals = append(l.Refusals, fmt.Sprintf("more than %d pages lie below the page", maxBelow))
			return l, nil
		}
		return l, err
	}
	var kept []string
	for _, s := range parts {
		if strings.TrimSpace(s) != "" {
			kept = append(kept, s)
		}
	}
	l.Text = strings.Join(kept, "\n\n")
	return l, nil
}

var errTooMany = errors.New("too many pages below")

// errChain is a parent chain that cannot be established: a cycle, or longer than
// any tree a person keeps.
var errChain = errors.New("the parent chain cannot be established")

// unestablished reports whether a chain failed for a reason the rules answer with
// a refusal rather than an outage.
func unestablished(err error) bool {
	return errors.Is(err, ErrUnreadable) || errors.Is(err, errChain)
}

// droppedOf is what the conversion of one page left out, for the preview.
func droppedOf(p Page, c Converted) []Dropped {
	var out []Dropped
	for range c.Passages {
		out = append(out, Dropped{Page: name(p), What: "passage"})
	}
	for _, kind := range c.Dropped {
		out = append(out, Dropped{Page: name(p), What: kind})
	}
	return out
}

// Gather reads every language page of a binding, and the picture of the primary
// one, and refuses the whole of it if any part fails (§ 7: per item, all or
// nothing). slots maps a language tag to what to read for it.
func Gather(ctx context.Context, ad Adapter, src Source, slots map[string]string, primary string, maxPicture int64) (Content, error) {
	st, err := ad.Standing(ctx)
	if err != nil {
		return Content{}, err
	}
	refused := &Refused{}
	for _, why := range CheckStanding(st) {
		refused.Problems = append(refused.Problems, SlotProblem{Message: why})
	}
	if len(refused.Problems) > 0 {
		return Content{}, refused
	}
	if len(slots) == 0 {
		return Content{}, &Refused{Problems: []SlotProblem{{Message: "a binding needs at least one language page"}}}
	}
	if _, ok := slots[primary]; !ok {
		return Content{}, &Refused{Problems: []SlotProblem{{Lang: primary,
			Message: "the primary language has no page"}}}
	}

	c := newPages(ad)
	out := Content{Languages: map[string]Language{}}
	langs := make([]string, 0, len(slots))
	for lang := range slots {
		langs = append(langs, lang)
	}
	slices.Sort(langs)
	where := map[string]string{}
	var labels [][]string
	for _, lang := range langs {
		l, err := preview(ctx, c, st, slots[lang])
		if err != nil {
			return Content{}, err
		}
		for _, why := range l.Refusals {
			refused.Problems = append(refused.Problems, SlotProblem{Lang: lang, Page: nonEmpty(l.Page.Key, l.Input), Message: why})
		}
		if l.Page.ID != "" {
			if other, dup := where[l.Page.ID]; dup {
				refused.Problems = append(refused.Problems, SlotProblem{Lang: lang, Page: nonEmpty(l.Page.Key, l.Page.ID),
					Message: "the same page is already the " + other + " page"})
			}
			where[l.Page.ID] = lang
		}
		out.Languages[lang] = l
		labels = append(labels, l.Labels)
	}
	if len(refused.Problems) > 0 {
		return Content{}, refused
	}
	out.Keywords = Keywords(src, labels...)

	prim := out.Languages[primary]
	if names := PictureNames(prim.page, src.PictureField); len(names) > 0 {
		// The first named attachment is the picture until the picture store holds
		// more than one (ADR-0391); the rest are a follow-up.
		pic, why, err := picture(ctx, ad, prim.page, names[0], maxPicture)
		if err != nil {
			return Content{}, err
		}
		if why != "" {
			return Content{}, &Refused{Problems: []SlotProblem{{Lang: primary, Page: name(prim.page), Message: why}}}
		}
		out.Picture = pic
	}
	return out, nil
}

// picture copies one named attachment and holds it to what the picture store
// accepts, so a file the shop would refuse to serve is refused here, where the
// person who chose it is looking.
func picture(ctx context.Context, ad Adapter, p Page, file string, max int64) (*Picture, string, error) {
	data, ct, err := ad.Attachment(ctx, p.ID, file, max)
	switch {
	case errors.Is(err, ErrUnreadable):
		return nil, "the picture field names " + file + ", which the page has no readable attachment of", nil
	case errors.Is(err, errTooLarge):
		return nil, fmt.Sprintf("the picture %s exceeds the %d byte limit", file, max), nil
	case err != nil:
		return nil, "", err
	}
	ct = brandimage.NormalizeType(ct)
	if _, ok := brandimage.Picture.ExtFor(ct); !ok || !brandimage.Valid(ct, data) {
		return nil, "the picture " + file + " is not a PNG, JPEG or SVG image", nil
	}
	return &Picture{Name: file, Type: ct, Data: data}, "", nil
}

// errTooLarge is an attachment past the limit it was asked for.
var errTooLarge = errors.New("the attachment exceeds the limit")

// Check is one page a recheck asks about: its id, and the passages the copy of it
// was taken without.
type Check struct {
	ID       string   `json:"id"`
	Passages []string `json:"passages,omitempty"`
}

// Recheck asks whether the content copied from these pages may still be shown
// (§ 9). It reads the account's standing and every page's protection — never its
// content — and returns every reason it may not, and the ids it read, for the
// change feed to watch: the pages and every ancestor, because a restriction set
// on an ancestor protects the page without changing it.
//
// A passage protected after the copy was taken refuses as well. Its text was
// plain when it was copied, so Atlas holds it; § 8 removes a passage *before*
// converting, and this is the same rule applied to a copy that already exists.
func Recheck(ctx context.Context, ad Adapter, checks []Check) ([]string, []string, error) {
	st, err := ad.Standing(ctx)
	if err != nil {
		return nil, nil, err
	}
	if why := CheckStanding(st); len(why) > 0 {
		return why, nil, nil
	}
	c := newPages(ad)
	reasons, touched, err := recheck(ctx, c, st, checks)
	if err != nil {
		return nil, nil, err
	}
	return reasons, touched, nil
}

// recheck is [Recheck] for a standing already read and checked, through a cache
// a sweep shares across every item of a source.
func recheck(ctx context.Context, c *pages, st Standing, checks []Check) ([]string, []string, error) {
	var reasons, touched []string
	touch := func(id string) {
		if !slices.Contains(touched, id) {
			touched = append(touched, id)
		}
	}
	for _, ch := range checks {
		// The page is watched even when it cannot be read: its watch is what
		// reports it readable again, and it is ended by id when nothing needs it.
		touch(ch.ID)
		p, err := c.page(ctx, ch.ID)
		if errors.Is(err, ErrUnreadable) {
			reasons = append(reasons, "page "+ch.ID+" can no longer be read: it is gone, or the account may no longer read it")
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		anc, err := c.chain(ctx, p)
		if err != nil {
			if !unestablished(err) {
				return nil, nil, err
			}
			reasons = append(reasons, "an ancestor of page "+name(p)+" cannot be read")
		}
		for _, a := range anc {
			touch(a.ID)
		}
		for _, why := range CheckPage(p, anc, st) {
			reasons = append(reasons, "page "+name(p)+": "+why)
		}
		for _, ref := range passagesOf(p) {
			if !slices.Contains(ch.Passages, ref) {
				reasons = append(reasons, "page "+name(p)+": a passage was protected after its content was copied")
				break
			}
		}
	}
	slices.Sort(touched)
	return reasons, touched, nil
}

// passagesOf is every passage the text blocks of a page's body mark, by id — the
// blocks a copy takes. A passage in a block that is never taken protects nothing
// Atlas holds.
func passagesOf(p Page) []string {
	var out []string
	for _, b := range p.Body {
		if !textKind(b.Kind) {
			continue
		}
		for _, m := range b.Marks {
			if m.Kind == MarkPassage && !slices.Contains(out, nonEmpty(m.Ref, "?")) {
				out = append(out, nonEmpty(m.Ref, "?"))
			}
		}
	}
	return out
}
