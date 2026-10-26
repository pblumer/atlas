package catalog

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/contentsource"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/logging"
)

// Binding a product's content to its pages in an external source
// (ADR-draft-service-content-from-an-external-source, § 5–§ 7).
//
// Every route here is a write or a read on one product — or, for the catalogue's
// refresh, on the products of one catalogue — under the gate that changing the
// product runs under: its home catalogue, readable and editable by the caller.
//
// A fetch is network I/O, so it never happens on the run loop (I3). Each write is
// therefore two visits: the first reads the product and checks the gate and the
// revision the caller stated, the fetch runs with the loop free, and the second
// checks that nothing moved in between before it writes the binding and the
// fetched content in one draft save. Nothing here is an event; nothing is fetched
// during replay, because there is no replay of a catalogue.

// ContentSources is how the catalogue reaches the configured sources. Off the
// run loop only: each method visits it itself.
type ContentSources interface {
	Sources() ([]contentsource.Source, error)
	Open(id string) (contentsource.Source, contentsource.Adapter, error)
}

// fetchTimeout bounds one fetch of a whole binding — every language page, every
// child page, the picture.
const fetchTimeout = 2 * time.Minute

// contentRequest is the body of the binding's writes.
type contentRequest struct {
	Source  string            `json:"source"`
	Primary string            `json:"primary"`
	Pages   map[string]string `json:"pages"`
	// Lang names the one slot an unlink removes; empty removes the binding.
	Lang   string `json:"lang"`
	Reason string `json:"reason"`
	// Revision is the precondition of a catalogue save (ADR-0376): the product's
	// revision the caller read. Zero states none.
	Revision int64 `json:"revision"`
}

// sourceSummary is what a product manager may know about a source: enough to
// choose it, nothing of its credential.
type sourceSummary struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	LinkInShop bool   `json:"linkInShop,omitempty"`
}

// contentStatus answers the status route.
type contentStatus struct {
	// Sources are the configured sources; empty means the binding does not
	// exist on this installation at all (§ 3).
	Sources []sourceSummary `json:"sources"`
	Binding *ContentSource  `json:"binding,omitempty"`
	State   *ContentState   `json:"state,omitempty"`
	// Shown says whether the shop shows the product's source content now.
	Shown bool `json:"shown"`
	// Languages are the home catalogue's declared languages: one slot each.
	Languages []string `json:"languages,omitempty"`
}

// itemForEdit reads a product under the edit gate. Runs on the loop.
func (s *Service) itemForEdit(id string, p *httpapi.Principal) (it Item, home Catalog, found, allowed bool, err error) {
	if it, found, err = s.store.Item(id); err != nil || !found {
		return
	}
	var homeOK bool
	if home, homeOK, err = s.store.Catalog(it.HomeCatalog); err != nil || !homeOK {
		found = false
		return
	}
	if found = s.mayRead(home, p); !found {
		return
	}
	allowed = s.mayEdit(home, p)
	return
}

// gateItem answers the gate's refusals; true means the caller passed.
func gateAnswer(w http.ResponseWriter, id string, found, allowed bool, err error) bool {
	switch {
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read product: "+err.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no product "+id)
	case !allowed:
		httpapi.Error(w, http.StatusForbidden, "a product's content is linked by whoever maintains its catalogue")
	default:
		return true
	}
	return false
}

// noSources answers a route of the binding on an installation with no source.
func (s *Service) noSources(w http.ResponseWriter) bool {
	if s.Content == nil {
		httpapi.Error(w, http.StatusNotFound, "no content source is configured on this server")
		return true
	}
	return false
}

// HandleContentStatus answers what a product is bound to, whether it is
// quarantined, and which sources there are to bind it to.
func (s *Service) HandleContentStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := httpapi.PrincipalFrom(r.Context())
	var (
		out            contentStatus
		found, allowed bool
		err            error
	)
	s.loop.Do(func() {
		var it Item
		var home Catalog
		if it, home, found, allowed, err = s.itemForEdit(id, p); err != nil || !found || !allowed {
			return
		}
		out.Binding, out.Languages = it.ContentSource, home.Languages
		st, has, e := s.store.ContentState(id)
		if err = e; err != nil {
			return
		}
		if has {
			out.State = &st
		}
		out.Shown = contentShown(it.ContentSource, st, has, s.now())
	})
	if !gateAnswer(w, id, found, allowed, err) {
		return
	}
	out.Sources = []sourceSummary{}
	if s.Content != nil {
		all, err := s.Content.Sources()
		if err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "list content sources: "+err.Error())
			return
		}
		for _, src := range all {
			out.Sources = append(out.Sources, sourceSummary{ID: src.ID, Name: src.Name, Kind: src.Kind, LinkInShop: src.LinkInShop})
		}
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// gateOnly checks the edit gate for a product and answers a refusal.
func (s *Service) gateOnly(w http.ResponseWriter, r *http.Request, id string) (Item, Catalog, bool) {
	p := httpapi.PrincipalFrom(r.Context())
	var (
		it             Item
		home           Catalog
		found, allowed bool
		err            error
	)
	s.loop.Do(func() { it, home, found, allowed, err = s.itemForEdit(id, p) })
	return it, home, gateAnswer(w, id, found, allowed, err)
}

// openSource opens a source and answers its failure.
func (s *Service) openSource(w http.ResponseWriter, id string) (contentsource.Source, contentsource.Adapter, bool) {
	if strings.TrimSpace(id) == "" {
		httpapi.Error(w, http.StatusBadRequest, "name the content source")
		return contentsource.Source{}, nil, false
	}
	src, ad, err := s.Content.Open(id)
	switch {
	case errors.Is(err, contentsource.ErrNoSource):
		httpapi.Error(w, http.StatusBadRequest, "no content source "+id)
		return src, nil, false
	case err != nil:
		httpapi.Error(w, http.StatusBadGateway, err.Error())
		return src, nil, false
	}
	return src, ad, true
}

// HandleContentSearch searches a source for pages to bind (§ 5): title, space,
// key and state of each hit.
func (s *Service) HandleContentSearch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, _, ok := s.gateOnly(w, r, id); !ok || s.noSources(w) {
		return
	}
	_, ad, ok := s.openSource(w, r.URL.Query().Get("source"))
	if !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		httpapi.Error(w, http.StatusBadRequest, "name what to search for: ?q=")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), fetchTimeout)
	defer cancel()
	hits, err := ad.Search(ctx, q)
	if err != nil {
		httpapi.Error(w, http.StatusBadGateway, "search the content source: "+err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, hits)
}

// contentPreview is what the preview answers: the page as a fetch would take it,
// and everything that would refuse or drop part of it.
type contentPreview struct {
	contentsource.Language
	// Keywords are the page's labels after the source's exclusions.
	Keywords []string `json:"keywords,omitempty"`
	// BoundTo names another product already bound to this page.
	BoundTo string `json:"boundTo,omitempty"`
}

// HandleContentPreview reads one page for one language as linking would (§ 5):
// its resolved id, title, state and version, the text it would supply, every
// passage and child page the rules would drop, and every reason they refuse.
// Nothing is written.
func (s *Service) HandleContentPreview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, _, ok := s.gateOnly(w, r, id); !ok || s.noSources(w) {
		return
	}
	src, ad, ok := s.openSource(w, r.URL.Query().Get("source"))
	if !ok {
		return
	}
	input := strings.TrimSpace(r.URL.Query().Get("page"))
	if input == "" {
		httpapi.Error(w, http.StatusBadRequest, "name the page: ?page= an address, a key or an id")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), fetchTimeout)
	defer cancel()
	st, err := ad.Standing(ctx)
	if err != nil {
		httpapi.Error(w, http.StatusBadGateway, "read the account's standing: "+err.Error())
		return
	}
	l, err := contentsource.Preview(ctx, ad, st, input)
	if err != nil {
		httpapi.Error(w, http.StatusBadGateway, "read the page: "+err.Error())
		return
	}
	l.Refusals = append(contentsource.CheckStanding(st), l.Refusals...)
	out := contentPreview{Language: l}
	if len(l.Refusals) == 0 {
		out.Keywords = contentsource.Keywords(src, l.Labels)
	}
	if l.Page.ID != "" {
		var err error
		s.loop.Do(func() { out.BoundTo, err = s.boundElsewhere(src.ID, []string{l.Page.ID}, id) })
		if err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "read bindings: "+err.Error())
			return
		}
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// boundElsewhere names a product other than self bound to one of these pages of
// a source, or "". Runs on the loop.
func (s *Service) boundElsewhere(source string, refs []string, self string) (string, error) {
	all, err := s.store.Items()
	if err != nil {
		return "", err
	}
	for _, it := range all {
		if it.ID == self || it.ContentSource == nil || it.ContentSource.Source != source {
			continue
		}
		// A child page counts as much as a language page: its text is in the
		// other product's description either way.
		for _, c := range checksOf(it.ContentSource) {
			if slices.Contains(refs, c.ID) {
				return it.ID, nil
			}
		}
	}
	return "", nil
}

// contentWrite is a link's or a refresh's outcome, for the HTTP answer.
type contentWrite struct {
	item     Item
	status   int
	message  string
	problems []contentsource.SlotProblem
}

// HandleContentLink binds a product to one page per language and writes the
// binding and the fetched content in one draft save (§ 5). Linking refuses a page
// the account cannot read, one in a space not released for the shop, one failing
// any rule of § 8, the same page in two slots, and a page bound to another
// product, naming it.
func (s *Service) HandleContentLink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, home, ok := s.gateOnly(w, r, id)
	if !ok || s.noSources(w) {
		return
	}
	var in contentRequest
	if err := decodeBody(r, &in); err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(in.Pages) == 0 {
		httpapi.Error(w, http.StatusBadRequest, "name a page for at least one language: pages {lang: address, key or id}")
		return
	}
	if in.Primary == "" && len(in.Pages) == 1 {
		for lang := range in.Pages {
			in.Primary = lang
		}
	}
	for lang, page := range in.Pages {
		if !slices.Contains(home.Languages, lang) {
			httpapi.Error(w, http.StatusBadRequest, "the catalogue declares no language "+lang+
				"; a product has one slot per language its catalogue declares")
			return
		}
		if strings.TrimSpace(page) == "" {
			httpapi.Error(w, http.StatusBadRequest, "the "+lang+" slot names no page")
			return
		}
	}
	if _, ok := in.Pages[in.Primary]; !ok {
		httpapi.Error(w, http.StatusBadRequest, "primary must name one of the slots")
		return
	}
	res := s.writeContent(r.Context(), httpapi.PrincipalFrom(r.Context()), id, in.Source, in.Primary, in.Pages, in.Revision)
	answerContentWrite(w, res)
}

// HandleContentRefresh fetches every bound page of a product again and writes the
// result into the draft (§ 7). All or nothing: a refresh that cannot read a page
// names it and the reason and leaves the product exactly as it was. It is the one
// act that ends a quarantine.
func (s *Service) HandleContentRefresh(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	it, _, ok := s.gateOnly(w, r, id)
	if !ok || s.noSources(w) {
		return
	}
	var in contentRequest
	if r.ContentLength != 0 {
		if err := decodeBody(r, &in); err != nil {
			httpapi.Error(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if it.ContentSource == nil {
		httpapi.Error(w, http.StatusConflict, "product "+id+" is not bound to a content source")
		return
	}
	answerContentWrite(w, s.refresh(r.Context(), httpapi.PrincipalFrom(r.Context()), it, in.Revision))
}

// refresh is one product's refresh: the recheck first (§ 9: before every
// refresh), whose finding is recorded whatever the fetch then does, and then the
// fetch of the binding's own page ids.
func (s *Service) refresh(ctx context.Context, p *httpapi.Principal, it Item, revision int64) contentWrite {
	b := it.ContentSource
	if b == nil {
		return contentWrite{status: http.StatusConflict, message: "product " + it.ID + " is not bound to a content source"}
	}
	if _, ad, err := s.Content.Open(b.Source); err == nil {
		rctx, cancel := context.WithTimeout(ctx, fetchTimeout)
		reasons, _, rerr := contentsource.Recheck(rctx, ad, checksOf(b))
		cancel()
		res := contentsource.Result{Item: it.ID, At: s.now(), Passed: rerr == nil && len(reasons) == 0}
		switch {
		case rerr != nil:
			res.Reason = "the content source cannot be reached: " + rerr.Error()
		case len(reasons) > 0:
			res.Reason = reasons[0]
		}
		if err := s.record(b.Source, []contentsource.Result{res}); err != nil {
			return contentWrite{status: http.StatusInternalServerError, message: "record recheck: " + err.Error()}
		}
	}
	slots := map[string]string{}
	for lang, pg := range b.Pages {
		slots[lang] = pg.Ref
	}
	return s.writeContent(ctx, p, it.ID, b.Source, b.Primary, slots, revision)
}

// writeContent fetches a binding and writes it with its content. It is link and
// refresh alike: a refresh is a link to the pages already bound.
func (s *Service) writeContent(ctx context.Context, p *httpapi.Principal, id, source, primary string,
	slots map[string]string, revision int64) contentWrite {
	// The first visit: the product as the caller means to change it.
	var (
		before         Item
		found, allowed bool
		err            error
	)
	s.loop.Do(func() { before, _, found, allowed, err = s.itemForEdit(id, p) })
	switch {
	case err != nil:
		return contentWrite{status: http.StatusInternalServerError, message: "read product: " + err.Error()}
	case !found:
		return contentWrite{status: http.StatusNotFound, message: "no product " + id}
	case !allowed:
		return contentWrite{status: http.StatusForbidden, message: "not an editor of the product's catalogue"}
	case revision != 0 && revision != before.Revision:
		return contentWrite{status: http.StatusConflict, message: "product " + id +
			" changed since it was read; read it again and state the revision you read"}
	}

	src, ad, err := s.Content.Open(source)
	switch {
	case errors.Is(err, contentsource.ErrNoSource):
		return contentWrite{status: http.StatusBadRequest, message: "no content source " + source}
	case err != nil:
		return contentWrite{status: http.StatusBadGateway, message: err.Error()}
	}
	fctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	content, err := contentsource.Gather(fctx, ad, src, slots, primary, s.budgets().Asset)
	var refused *contentsource.Refused
	switch {
	case errors.As(err, &refused):
		return contentWrite{status: http.StatusUnprocessableEntity, message: "the pages may not supply the shop",
			problems: refused.Problems}
	case err != nil:
		return contentWrite{status: http.StatusBadGateway, message: "read the content source: " + err.Error()}
	}

	at := s.now()
	binding := &ContentSource{Source: src.ID, Primary: primary, Pages: map[string]PageRef{}}
	descriptions := map[string]string{}
	var refs []string
	for lang, l := range content.Languages {
		ref := PageRef{Ref: l.Page.ID, Version: l.Page.Version, FetchedAt: at, URL: l.Page.URL,
			Title: l.Page.Title, Passages: l.Page.Passages, Labels: contentsource.Keywords(src, l.Labels)}
		refs = append(refs, l.Page.ID)
		for _, ch := range l.Children {
			ref.Children = append(ref.Children, ChildRef{Ref: ch.ID, Version: ch.Version, Title: ch.Title, Passages: ch.Passages})
			refs = append(refs, ch.ID)
		}
		binding.Pages[lang] = ref
		if strings.TrimSpace(l.Text) != "" {
			descriptions[lang] = l.Text
		}
	}

	// The second visit: nothing moved, and the pages are nobody else's.
	var (
		out     Item
		stale   bool
		other   string
		lifted  *Quarantine
		saveErr error
	)
	s.loop.Do(func() {
		var cur Item
		if cur, _, found, allowed, saveErr = s.itemForEdit(id, p); saveErr != nil || !found || !allowed {
			return
		}
		if cur.Revision != before.Revision {
			stale = true
			return
		}
		if other, saveErr = s.boundElsewhere(src.ID, refs, id); saveErr != nil || other != "" {
			return
		}
		if content.Picture != nil {
			saveErr = s.store.SavePicture(id, content.Picture.Data, content.Picture.Type)
		} else {
			saveErr = s.store.ClearPicture(id)
		}
		if saveErr != nil {
			return
		}
		cur.ContentSource = binding
		cur.Descriptions = nil
		if len(descriptions) > 0 {
			cur.Descriptions = descriptions
		}
		cur.Keywords = nil
		if len(content.Keywords) > 0 {
			cur.Keywords = content.Keywords
		}
		cur.Revision++
		cur.UpdatedAt = at
		if saveErr = s.store.SaveItem(cur); saveErr != nil {
			return
		}
		// A link or refresh that passes every rule is a passed recheck, and the
		// one act that lifts a quarantine. What was quarantined stays withheld
		// wherever a copy of it remains — the releases before this one.
		st, _, e := s.store.ContentState(id)
		if saveErr = e; saveErr != nil {
			return
		}
		if st.Quarantine != nil {
			lifted = st.Quarantine
			st.Distrusted = max(st.Distrusted, st.Quarantine.Since)
			st.Quarantine = nil
		}
		st.ItemID, st.Source, st.LastChecked, st.LastPassed = id, src.ID, at, at
		if saveErr = s.store.SaveContentState(st); saveErr != nil {
			return
		}
		out = cur
	})
	switch {
	case saveErr != nil:
		return contentWrite{status: http.StatusInternalServerError, message: "save product: " + saveErr.Error()}
	case !found:
		return contentWrite{status: http.StatusNotFound, message: "no product " + id}
	case !allowed:
		return contentWrite{status: http.StatusForbidden, message: "not an editor of the product's catalogue"}
	case stale:
		return contentWrite{status: http.StatusConflict, message: "product " + id +
			" changed while its pages were read; read it again and repeat"}
	case other != "":
		return contentWrite{status: http.StatusConflict, message: "a page is already bound to product " + other +
			"; one page describes one service"}
	}
	if lifted != nil {
		logging.Info(logging.ContentQuarantineLifted, "a refresh passed every rule and ended a product's quarantine",
			slog.String("item", id), slog.String("source", src.ID), slog.String("reason_was", lifted.Reason))
	}
	return contentWrite{item: out, status: http.StatusOK}
}

func answerContentWrite(w http.ResponseWriter, res contentWrite) {
	switch {
	case res.status == http.StatusOK:
		httpapi.JSON(w, http.StatusOK, res.item)
	case len(res.problems) > 0:
		httpapi.JSON(w, res.status, map[string]any{"error": res.message, "problems": res.problems})
	default:
		httpapi.Error(w, res.status, res.message)
	}
}

// catalogRefresh is one product's line in a catalogue's refresh.
type catalogRefresh struct {
	Item     string                      `json:"item"`
	Status   string                      `json:"status"`
	Message  string                      `json:"message,omitempty"`
	Problems []contentsource.SlotProblem `json:"problems,omitempty"`
}

// HandleCatalogContentRefresh refreshes every bound product a catalogue offers
// (§ 7), each all or nothing on its own. A product homed in a catalogue the
// caller does not maintain is skipped and said so.
func (s *Service) HandleCatalogContentRefresh(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := httpapi.PrincipalFrom(r.Context())
	var (
		found, allowed bool
		items          []Item
		mine           map[string]bool
		err            error
	)
	s.loop.Do(func() {
		var c Catalog
		if c, found, err = s.store.Catalog(id); err != nil || !found {
			return
		}
		if found = s.mayRead(c, p); !found {
			return
		}
		if allowed = s.mayEdit(c, p); !allowed {
			return
		}
		mine = map[string]bool{}
		for _, itemID := range c.Items {
			it, ok, e := s.store.Item(itemID)
			if err = e; err != nil {
				return
			}
			if !ok || it.ContentSource == nil {
				continue
			}
			home, ok, e := s.store.Catalog(it.HomeCatalog)
			if err = e; err != nil {
				return
			}
			mine[it.ID] = ok && s.mayEdit(home, p)
			items = append(items, it)
		}
	})
	switch {
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read catalogue: "+err.Error())
		return
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no catalogue "+id)
		return
	case !allowed:
		httpapi.Error(w, http.StatusForbidden, "not an editor of catalogue "+id)
		return
	}
	if s.noSources(w) {
		return
	}
	out := []catalogRefresh{}
	for _, it := range items {
		if !mine[it.ID] {
			out = append(out, catalogRefresh{Item: it.ID, Status: "skipped",
				Message: "its home catalogue is maintained by somebody else"})
			continue
		}
		res := s.refresh(r.Context(), p, it, 0)
		line := catalogRefresh{Item: it.ID, Status: "refreshed"}
		if res.status != http.StatusOK {
			line.Status, line.Message, line.Problems = "refused", res.message, res.problems
		}
		out = append(out, line)
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// HandleContentUnlink removes one language page of a product's binding, or the
// whole binding, with a reason the product manager must give (§ 6). The fields
// become editable again and keep the last fetched values — unless the product is
// quarantined, in which case the content in question is dropped. Every unlink is
// recorded in the audit trail.
//
// Removing one slot removes what it supplied: that language's description and the
// keywords only its labels gave. Content no binding names any more would be
// content no recheck covers. The primary slot goes only with the whole binding,
// because the picture is its.
func (s *Service) HandleContentUnlink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := httpapi.PrincipalFrom(r.Context())
	if _, _, ok := s.gateOnly(w, r, id); !ok {
		return
	}
	var in contentRequest
	if err := decodeBody(r, &in); err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" {
		httpapi.Error(w, http.StatusBadRequest, "an unlink needs a reason: the product owner's page stops supplying the shop, and the record says why")
		return
	}
	var (
		found, allowed bool
		status         int
		message        string
		out            Item
		removed        []string
		source         string
		quarantined    bool
		err            error
	)
	s.loop.Do(func() {
		var it Item
		if it, _, found, allowed, err = s.itemForEdit(id, p); err != nil || !found || !allowed {
			return
		}
		switch {
		case in.Revision != 0 && in.Revision != it.Revision:
			status, message = http.StatusConflict, "product "+id+" changed since it was read; read it again and state the revision you read"
			return
		case it.ContentSource == nil:
			status, message = http.StatusConflict, "product "+id+" is not bound to a content source"
			return
		}
		b := it.ContentSource.Copy()
		source = b.Source
		st, _, e := s.store.ContentState(id)
		if err = e; err != nil {
			return
		}
		quarantined = st.Quarantine != nil
		whole := in.Lang == ""
		if !whole {
			pg, ok := b.Pages[in.Lang]
			switch {
			case !ok:
				status, message = http.StatusBadRequest, "product "+id+" has no "+in.Lang+" page"
				return
			case in.Lang == b.Primary && len(b.Pages) > 1:
				status, message = http.StatusConflict, "the primary page names the picture; it goes only with the whole binding"
				return
			}
			removed = append(removed, pg.Ref)
			if len(b.Pages) == 1 {
				whole = true
			} else {
				delete(b.Pages, in.Lang)
				delete(it.Descriptions, in.Lang)
				var labels [][]string
				for _, rest := range b.Pages {
					labels = append(labels, rest.Labels)
				}
				it.Keywords = contentsource.Keywords(contentsource.Source{}, labels...)
				if len(it.Keywords) == 0 {
					it.Keywords = nil
				}
				it.ContentSource = b
			}
		}
		if whole {
			for _, pg := range b.Pages {
				if !slices.Contains(removed, pg.Ref) {
					removed = append(removed, pg.Ref)
				}
			}
			it.ContentSource = nil
			if quarantined {
				// The content in question is dropped rather than handed to the
				// product manager as theirs (§ 6).
				it.Descriptions, it.Keywords = nil, nil
				if err = s.store.ClearPicture(id); err != nil {
					return
				}
			}
		}
		if len(it.Descriptions) == 0 {
			it.Descriptions = nil
		}
		it.Revision++
		it.UpdatedAt = s.now()
		if err = s.store.SaveItem(it); err != nil {
			return
		}
		out = it
	})
	if !gateAnswer(w, id, found, allowed, err) {
		return
	}
	if status != 0 {
		httpapi.Error(w, status, message)
		return
	}
	slices.Sort(removed)
	attrs := []slog.Attr{slog.String("client_ip", httpapi.ClientIP(r)), slog.String("item", id),
		slog.String("source", source), slog.String("lang", in.Lang), slog.String("pages", strings.Join(removed, ",")),
		slog.String("reason", in.Reason), slog.Bool("quarantined", quarantined)}
	if p != nil {
		attrs = append(attrs, slog.String("actor", p.Username), slog.String("actor_id", p.UserID))
	}
	logging.Info(logging.ContentUnlinked, "a product's content binding was removed", attrs...)
	if s.Unlinked != nil {
		s.Unlinked(source)
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// checksOf is what a recheck asks about a binding: every page whose text it
// holds, with the passages each was copied without.
func checksOf(b *ContentSource) []contentsource.Check {
	if b == nil {
		return nil
	}
	var out []contentsource.Check
	for _, lang := range slices.Sorted(maps.Keys(b.Pages)) {
		pg := b.Pages[lang]
		out = append(out, contentsource.Check{ID: pg.Ref, Passages: pg.Passages})
		for _, ch := range pg.Children {
			out = append(out, contentsource.Check{ID: ch.Ref, Passages: ch.Passages})
		}
	}
	return out
}

// boundSave carries a product's binding through an ordinary save (§ 1: while
// bound, the fields a source supplies are read-only). The stored binding is kept
// whatever the body says, because only link, refresh and unlink write it; a body
// that leaves a supplied field out keeps the stored value, which is what a form
// that never rendered the field sends; and a body that changes one is refused,
// naming it, rather than saved as if the change had been made.
//
// A product nobody bound yet cannot be bound by a save either.
func boundSave(prev Item, prevOK bool, in Item) (Item, string) {
	if !prevOK || prev.ContentSource == nil {
		if in.ContentSource != nil {
			return in, "a product is bound to a content source by linking its pages, not by a save"
		}
		return in, ""
	}
	if in.ContentSource != nil && !sameBinding(in.ContentSource, prev.ContentSource) {
		return in, "the product's content binding is changed by linking, refreshing or unlinking, not by a save"
	}
	in.ContentSource = prev.ContentSource
	var changed []string
	if in.Descriptions == nil {
		in.Descriptions = prev.Descriptions
	} else if !maps.Equal(in.Descriptions, prev.Descriptions) {
		changed = append(changed, "descriptions")
	}
	if in.Keywords == nil {
		in.Keywords = prev.Keywords
	} else if !slices.Equal(in.Keywords, prev.Keywords) {
		changed = append(changed, "keywords")
	}
	if len(changed) > 0 {
		return in, "product " + in.ID + " takes its " + strings.Join(changed, " and ") +
			" from content source " + prev.ContentSource.Source + "; they cannot be changed while it is bound — " +
			"refresh it, or unlink it first"
	}
	return in, ""
}

// sameBinding reports whether two bindings name the same pages as copied at the
// same moments — what a read-modify-write sends back unchanged.
func sameBinding(a, b *ContentSource) bool {
	if a.Source != b.Source || a.Primary != b.Primary || len(a.Pages) != len(b.Pages) {
		return false
	}
	for lang, p := range a.Pages {
		q, ok := b.Pages[lang]
		if !ok || p.Ref != q.Ref || p.Version != q.Version || p.FetchedAt != q.FetchedAt {
			return false
		}
	}
	return true
}
