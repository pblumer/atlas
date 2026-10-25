package carddav

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/pblumer/atlas/connector/carddav/catalog"
)

// scanLimit bounds how many cards a find, a search or a listing reads: Google's limit of
// 25 000 contacts per account, taken as the size of an address book a Worker should be
// walking to the end of.
const scanLimit = 25000

// updateAttempts bounds how often a write re-reads a card that changed between its read
// and its write. The server refuses such a write (412) and the worker reads again; a
// card that changes three times in that window is being edited by something that will
// not stop, and the job fails rather than spin.
const updateAttempts = 3

// book is one job's view of an address book: the client, the address book's URL, and —
// once something needed it — every card in it, read once for the job.
type book struct {
	c    *Client
	coll *url.URL

	read     bool
	contacts []view
	groups   []*groupCard
}

// view is a contact as a read of the whole address book keeps it: its projection
// without groups, and what its groups are computed from. The card itself is not kept —
// with a photo in it, it is a hundred times the size.
type view struct {
	id         string
	uid        string
	contact    map[string]any
	categories []string
}

// groupCard is a group kept as a vCard of its own (catalog.GroupsVCards).
type groupCard struct {
	id      string
	name    string
	members map[string]bool // normalized UIDs
}

// scan reads the whole address book once: the listing, then the cards in batches.
// A card that is not a vCard at all fails the read, naming it, rather than being
// skipped: a find that skipped it could answer "unknown" for the customer it holds.
func (b *book) scan(ctx context.Context) error {
	if b.read {
		return nil
	}
	ms, err := b.c.members(ctx, b.coll, scanLimit)
	if errors.Is(err, errTooBig) {
		return fmt.Errorf("carddav: the address book holds more than %d contacts, more than a find or a listing reads", scanLimit)
	}
	if err != nil {
		return err
	}
	for start := 0; start < len(ms); start += multigetBatch {
		batch := ms[start:min(start+multigetBatch, len(ms))]
		got, err := b.fetch(ctx, batch)
		if err != nil {
			return err
		}
		for _, m := range batch {
			f, ok := got[m.url.Path]
			if !ok {
				continue // deleted since the listing
			}
			card, err := ParseCard([]byte(f.data))
			if err != nil {
				return fmt.Errorf("carddav: the address book's %s is not a vCard this Worker can read: %w", m.id, err)
			}
			if isGroupCard(card) {
				b.groups = append(b.groups, &groupCard{id: m.id, name: groupName(card), members: memberUIDs(card)})
				continue
			}
			b.contacts = append(b.contacts, view{
				id: m.id, uid: card.First("UID").Text(),
				contact: project(m.id, card, nil), categories: categoriesOf(card),
			})
		}
	}
	sort.Slice(b.groups, func(i, j int) bool { return strings.ToLower(b.groups[i].name) < strings.ToLower(b.groups[j].name) })
	b.read = true
	return nil
}

// fetch reads a batch of cards, asking for it again in halves when the answer is
// larger than the payload budget — photos make one batch of fifty cards far larger than
// another. A single card above the budget is refused, naming it.
func (b *book) fetch(ctx context.Context, ms []member) (map[string]fetched, error) {
	got, err := b.c.multiget(ctx, b.coll, ms)
	if !errors.Is(err, errAnswerTooLarge) {
		return got, err
	}
	if len(ms) == 1 {
		return nil, fmt.Errorf("carddav: the address book's %s is larger than the installation's payload budget (limits.Payload): %w", ms[0].id, err)
	}
	half := len(ms) / 2
	first, err := b.fetch(ctx, ms[:half])
	if err != nil {
		return nil, err
	}
	rest, err := b.fetch(ctx, ms[half:])
	if err != nil {
		return nil, err
	}
	for k, v := range rest {
		first[k] = v
	}
	return first, nil
}

// groupsOf is a contact's groups in the Worker's form: its categories, or the names of
// the group cards that name its UID.
func (b *book) groupsOf(uid string, categories []string) []string {
	if b.c.groups == catalog.GroupsCategories {
		return categories
	}
	var out []string
	n := normUID(uid)
	for _, g := range b.groups {
		if n != "" && g.members[n] {
			out = append(out, g.name)
		}
	}
	return out
}

// project is one card as a model reads it. In the group-vcards form its groups are on
// other cards, so the address book is read for them.
func (b *book) project(ctx context.Context, id string, card *Card) (any, error) {
	if b.c.groups == catalog.GroupsVCards {
		if err := b.scan(ctx); err != nil {
			return nil, err
		}
	}
	return project(id, card, b.groupsOf(card.First("UID").Text(), categoriesOf(card))), nil
}

func (b *book) projectViews(vs []view) []any {
	out := make([]any, 0, len(vs))
	for _, v := range vs {
		c := make(map[string]any, len(v.contact))
		for k, x := range v.contact {
			c[k] = x
		}
		gl := []any{}
		for _, g := range b.groupsOf(v.uid, v.categories) {
			gl = append(gl, g)
		}
		c[catalog.KeyGroups] = gl
		out = append(out, c)
	}
	return out
}

// inGroup is the filter a read narrowed to one group applies. In the categories form a
// group exists by being on a contact, so a name nobody uses yet matches nobody; in the
// group-vcards form the group must exist, and a misspelt name is refused.
func (b *book) inGroup(name string) (func(view) bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return func(view) bool { return true }, nil
	}
	if b.c.groups == catalog.GroupsCategories {
		return func(v view) bool { return containsFold(v.categories, name) }, nil
	}
	g, err := b.resolveGroup(name)
	if err != nil {
		return nil, err
	}
	return func(v view) bool { return v.uid != "" && g.members[normUID(v.uid)] }, nil
}

// resolveGroup finds a group card by its name — exactly, or else ignoring case when that
// is unambiguous. An unknown name is refused rather than created: a misspelt group
// would otherwise become a second group nobody looks at.
func (b *book) resolveGroup(name string) (*groupCard, error) {
	name = strings.TrimSpace(name)
	var exact, folded []*groupCard
	for _, g := range b.groups {
		switch {
		case g.name == name:
			exact = append(exact, g)
		case strings.EqualFold(g.name, name):
			folded = append(folded, g)
		}
	}
	match := exact
	if len(match) == 0 {
		match = folded
	}
	switch len(match) {
	case 1:
		return match[0], nil
	case 0:
		var known []string
		for _, g := range b.groups {
			known = append(known, g.name)
		}
		if len(known) > 10 {
			known = append(known[:10], "…")
		}
		return nil, fmt.Errorf("carddav: this address book has no group %q (its groups: %s); create it in the provider's contacts app first", name, strings.Join(known, ", "))
	}
	return nil, fmt.Errorf("carddav: %d groups are called %q; rename one of them in the provider's contacts app", len(match), name)
}

// find reads the address book and keeps the contacts whose e-mail address or phone
// number equals the query — or, with a field, whose custom field of that name does.
// It reads rather than asks the server to filter (addressbook-query) on purpose: text
// matching differs between servers in collation and in what they support, and a find
// that silently answered "unknown" on one of them would create a duplicate customer.
func (b *book) find(ctx context.Context, j Job, max int) (any, error) {
	if err := b.scan(ctx); err != nil {
		return nil, err
	}
	in, err := b.inGroup(j.Group)
	if err != nil {
		return nil, err
	}
	match := matcher(strings.TrimSpace(j.Query), strings.TrimSpace(j.Field))
	var hits []view
	for _, v := range b.contacts {
		if in(v) && match(v.contact) {
			hits = append(hits, v)
		}
	}
	if len(hits) > max {
		return nil, fmt.Errorf("carddav: find-contacts matched %d contacts, more than the maxResults cap of %d; a lookup that matches that many is not finding one", len(hits), max)
	}
	sortViews(hits)
	return b.projectViews(hits), nil
}

// search keeps the contacts whose name, organization, e-mail address or phone number
// contains the query, ignoring case, in the listing's order, and answers with the first
// max of them: a search answers with its best matches, and more of them are simply not
// returned.
func (b *book) search(ctx context.Context, query string, max int) (any, error) {
	if err := b.scan(ctx); err != nil {
		return nil, err
	}
	q := strings.ToLower(query)
	digits := phoneDigits(query)
	var hits []view
	for _, v := range b.contacts {
		if searchMatch(v.contact, q, digits) {
			hits = append(hits, v)
		}
	}
	sortViews(hits)
	if len(hits) > max {
		hits = hits[:max]
	}
	return b.projectViews(hits), nil
}

// list reads every contact, or one group's members, failing above max: a short list is
// a wrong answer a process would act on, and it is never cut to fit.
func (b *book) list(ctx context.Context, group string, max int) (any, error) {
	if err := b.scan(ctx); err != nil {
		return nil, err
	}
	in, err := b.inGroup(group)
	if err != nil {
		return nil, err
	}
	var out []view
	for _, v := range b.contacts {
		if in(v) {
			out = append(out, v)
		}
	}
	if len(out) > max {
		return nil, fmt.Errorf("carddav: the listing holds %d contacts, more than the maxResults cap of %d; narrow it to a group or raise maxResults (truncating would be a wrong answer, not a partial one)", len(out), max)
	}
	sortViews(out)
	return b.projectViews(out), nil
}

// sortViews orders contacts as a person reads an address book: by family name, then
// given name, then display name.
func sortViews(vs []view) {
	key := func(v view) []string {
		return []string{strings.ToLower(strOf(v.contact, catalog.KeyFamilyName)), strings.ToLower(strOf(v.contact, catalog.KeyGivenName)),
			strings.ToLower(strOf(v.contact, catalog.KeyDisplayName)), v.id}
	}
	sort.SliceStable(vs, func(i, j int) bool {
		a, b := key(vs[i]), key(vs[j])
		for k := range a {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return false
	})
}

func strOf(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// matcher builds the test find applies, on the projected contact. Without a field, the
// query is compared with every e-mail address, ignoring case, and with every phone
// number by its digits, so "+41 79 123 45 67" finds "+41791234567". With a field it is
// compared with that custom field's value, ignoring case and surrounding space.
func matcher(query, field string) func(map[string]any) bool {
	if field != "" {
		return func(c map[string]any) bool {
			fields, _ := c[catalog.KeyFields].(map[string]any)
			for k, v := range fields {
				s, _ := v.(string)
				if strings.EqualFold(k, field) && strings.EqualFold(strings.TrimSpace(s), query) {
					return true
				}
			}
			return false
		}
	}
	digits := phoneDigits(query)
	return func(c map[string]any) bool {
		for _, e := range entryValues(c, catalog.KeyEmails) {
			if strings.EqualFold(e, query) {
				return true
			}
		}
		if digits == "" {
			return false
		}
		for _, p := range entryValues(c, catalog.KeyPhones) {
			if phoneDigits(p) == digits {
				return true
			}
		}
		return false
	}
}

// searchMatch is the search's test: a substring of the names, the organization or an
// e-mail address, or of a phone number's digits.
func searchMatch(c map[string]any, q, digits string) bool {
	for _, k := range []string{catalog.KeyDisplayName, catalog.KeyGivenName, catalog.KeyFamilyName, catalog.KeyOrganization} {
		if strings.Contains(strings.ToLower(strOf(c, k)), q) {
			return true
		}
	}
	for _, e := range entryValues(c, catalog.KeyEmails) {
		if strings.Contains(strings.ToLower(e), q) {
			return true
		}
	}
	if digits != "" {
		for _, p := range entryValues(c, catalog.KeyPhones) {
			if strings.Contains(phoneDigits(p), digits) {
				return true
			}
		}
	}
	return false
}

// entryValues are the values of a projected contact's e-mail or phone entries.
func entryValues(c map[string]any, key string) []string {
	list, _ := c[key].([]any)
	var out []string
	for _, e := range list {
		m, _ := e.(map[string]any)
		if s, _ := m["value"].(string); s != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

// phoneDigits is a phone number reduced to its digits, or "" for a value that is not a
// phone number at all — anything with a letter in it, or fewer than five digits, so an
// e-mail address or a short code never matches a number by accident.
func phoneDigits(s string) string {
	var d strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			d.WriteRune(r)
		case strings.ContainsRune("+ ()-./", r):
		default:
			return ""
		}
	}
	if d.Len() < 5 {
		return ""
	}
	return d.String()
}

// groupNames are the groups a create names, trimmed, each once.
func groupNames(contact map[string]any) []string {
	raw, _ := contact[catalog.KeyGroups].([]any)
	var out []string
	for _, g := range raw {
		s, _ := g.(string)
		if s = strings.TrimSpace(s); s != "" && !containsFold(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// create creates a contact under the name its job's create key gives it, with
// If-None-Match: * — so a retry of a create that succeeded is refused with 412 instead
// of making a second contact, and completes with the one the first round made.
//
// In the categories form the groups travel in the same PUT, so the contact exists with
// them or not at all. In the group-vcards form they are written to the group cards
// afterwards; every group is checked to exist before anything is written, and a retry
// writes them again, which a member already present makes no change.
func (b *book) create(ctx context.Context, j Job) (any, error) {
	uid := CreateUID(j.CreateKey)
	id := uid + ".vcf"
	u, err := memberURL(b.coll, id)
	if err != nil {
		return nil, err
	}
	names := groupNames(j.Contact)
	var targets []*groupCard
	if b.c.groups == catalog.GroupsVCards && len(names) > 0 {
		if err := b.scan(ctx); err != nil {
			return nil, err
		}
		for _, n := range names {
			g, err := b.resolveGroup(n)
			if err != nil {
				return nil, err
			}
			targets = append(targets, g)
		}
	}
	card := NewCard(uid)
	if err := apply(card, j.Contact); err != nil {
		return nil, err
	}
	if b.c.groups == catalog.GroupsCategories && len(names) > 0 {
		setCategories(card, names)
	}
	err = b.c.putCard(ctx, "PUT contact", u, card, "", true)
	if isStatus(err, 412) {
		// Already there: this job's earlier round created it. Its UID is this job's own,
		// so it is not somebody else's contact.
		existing, _, gerr := b.c.getCard(ctx, "GET contact", u)
		if gerr != nil {
			return nil, gerr
		}
		card, err = existing, nil
	}
	if err != nil {
		return nil, err
	}
	var groups []string
	if b.c.groups == catalog.GroupsCategories {
		groups = categoriesOf(card)
	}
	for _, g := range targets {
		if err := b.member(ctx, g, uid, true); err != nil {
			return nil, err
		}
		groups = append(groups, g.name)
	}
	return project(id, card, groups), nil
}

// update reads the contact's current version, applies the model's changes to it and
// writes it back under that version's ETag.
func (b *book) update(ctx context.Context, j Job, id string) (any, error) {
	u, err := memberURL(b.coll, id)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := 0; attempt < updateAttempts; attempt++ {
		card, etag, err := b.c.getCard(ctx, "GET contact", u)
		if isStatus(err, 404, 410) {
			return nil, &notFound{op: j.Operation}
		}
		if err != nil {
			return nil, err
		}
		if !anyValue(j.Contact) {
			// Every value was null: nothing to change, and the answer is the contact as
			// it is.
			return b.project(ctx, id, card)
		}
		if err := apply(card, j.Contact); err != nil {
			return nil, err
		}
		b.touch(card)
		err = b.c.putCard(ctx, "PUT contact", u, card, etag, false)
		if isStatus(err, 412) {
			lastErr = err
			continue
		}
		if isStatus(err, 404, 410) {
			return nil, &notFound{op: j.Operation}
		}
		if err != nil {
			return nil, err
		}
		return b.project(ctx, id, card)
	}
	return nil, fmt.Errorf("carddav: update-contact: the contact changed between reading and writing it %d times in a row: %w", updateAttempts, lastErr)
}

func anyValue(contact map[string]any) bool {
	for _, v := range contact {
		if v != nil {
			return true
		}
	}
	return false
}

// touch stamps REV on a card that carries one, so a client that compares it sees the
// change; a card without REV does not get one — clients sync on ETags.
func (b *book) touch(card *Card) {
	if rev := card.First("REV"); rev != nil {
		rev.Params = nil
		rev.SetText(b.c.now().UTC().Format("20060102T150405Z"))
	}
}

// label adds a contact to a group or removes it from one. Both are safe to repeat: a
// member added again, or a non-member removed, is no change and no write.
func (b *book) label(ctx context.Context, j Job, id string) error {
	add := j.Operation == "add-to-group"
	u, err := memberURL(b.coll, id)
	if err != nil {
		return err
	}
	if b.c.groups == catalog.GroupsCategories {
		var lastErr error
		for attempt := 0; attempt < updateAttempts; attempt++ {
			card, etag, err := b.c.getCard(ctx, "GET contact", u)
			if isStatus(err, 404, 410) {
				return &notFound{op: j.Operation}
			}
			if err != nil {
				return err
			}
			var changed bool
			if add {
				changed = addCategory(card, strings.TrimSpace(j.Group))
			} else {
				changed = removeCategory(card, strings.TrimSpace(j.Group))
			}
			if !changed {
				return nil
			}
			b.touch(card)
			err = b.c.putCard(ctx, "PUT contact", u, card, etag, false)
			if isStatus(err, 412) {
				lastErr = err
				continue
			}
			if isStatus(err, 404, 410) {
				return &notFound{op: j.Operation}
			}
			return err
		}
		return fmt.Errorf("carddav: %s: the contact changed between reading and writing it %d times in a row: %w", j.Operation, updateAttempts, lastErr)
	}
	card, _, err := b.c.getCard(ctx, "GET contact", u)
	if isStatus(err, 404, 410) {
		return &notFound{op: j.Operation}
	}
	if err != nil {
		return err
	}
	uid := card.First("UID").Text()
	if strings.TrimSpace(uid) == "" {
		return fmt.Errorf("carddav: %s: the contact has no UID, so no group card can name it as a member", j.Operation)
	}
	if err := b.scan(ctx); err != nil {
		return err
	}
	g, err := b.resolveGroup(j.Group)
	if err != nil {
		return err
	}
	return b.member(ctx, g, uid, add)
}

// member adds a UID to a group card or removes it, under the group card's ETag.
func (b *book) member(ctx context.Context, g *groupCard, uid string, add bool) error {
	u, err := memberURL(b.coll, g.id)
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 0; attempt < updateAttempts; attempt++ {
		gc, etag, err := b.c.getCard(ctx, "GET group", u)
		if isStatus(err, 404, 410) {
			return fmt.Errorf("carddav: the group %q was deleted while this job ran", g.name)
		}
		if err != nil {
			return err
		}
		if setMember(gc, uid, add) {
			b.touch(gc)
			err = b.c.putCard(ctx, "PUT group", u, gc, etag, false)
			if isStatus(err, 412) {
				lastErr = err
				continue
			}
			if err != nil {
				return err
			}
		}
		if add {
			g.members[normUID(uid)] = true
		} else {
			delete(g.members, normUID(uid))
		}
		return nil
	}
	return fmt.Errorf("carddav: the group %q changed between reading and writing it %d times in a row: %w", g.name, updateAttempts, lastErr)
}

// isGroupCard reports whether a card is a group rather than a contact: Apple's
// X-ADDRESSBOOKSERVER-KIND (vCard 3.0) or KIND (4.0) says "group".
func isGroupCard(c *Card) bool {
	for _, name := range []string{"KIND", "X-ADDRESSBOOKSERVER-KIND"} {
		if p := c.First(name); p != nil && strings.EqualFold(strings.TrimSpace(p.Text()), "group") {
			return true
		}
	}
	return false
}

// groupName is a group card's name as Apple Contacts shows it.
func groupName(c *Card) string {
	if fn := strings.TrimSpace(c.First("FN").Text()); fn != "" {
		return fn
	}
	if n := c.First("N").Components(); len(n) > 0 {
		return strings.TrimSpace(n[0])
	}
	return ""
}

// memberProps are a group card's member lines.
func memberProps(c *Card) []*Prop {
	return append(c.All("MEMBER"), c.All("X-ADDRESSBOOKSERVER-MEMBER")...)
}

func memberUIDs(c *Card) map[string]bool {
	out := map[string]bool{}
	for _, p := range memberProps(c) {
		if n := normUID(p.Text()); n != "" {
			out[n] = true
		}
	}
	return out
}

// normUID is a UID as a member line names it, for comparing: without urn:uuid:, lower
// case.
func normUID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.TrimPrefix(s, "urn:uuid:")
}

// setMember adds a UID to a group card's members or removes it, reporting whether the
// card changed. A vCard 4.0 group card names members with MEMBER, a 3.0 one — Apple's —
// with X-ADDRESSBOOKSERVER-MEMBER.
func setMember(gc *Card, uid string, add bool) bool {
	n := normUID(uid)
	var have []*Prop
	for _, p := range memberProps(gc) {
		if normUID(p.Text()) == n {
			have = append(have, p)
		}
	}
	if add {
		if len(have) > 0 {
			return false
		}
		name := "X-ADDRESSBOOKSERVER-MEMBER"
		if gc.First("KIND") != nil || gc.v4() {
			name = "MEMBER"
		}
		value := uid
		if !strings.HasPrefix(strings.ToLower(uid), "urn:uuid:") {
			value = "urn:uuid:" + uid
		}
		gc.Add(&Prop{Name: name, Value: value})
		return true
	}
	if len(have) == 0 {
		return false
	}
	gc.Remove(have...)
	return true
}
