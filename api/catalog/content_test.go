package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pblumer/atlas/api/contentsource"
	"github.com/pblumer/atlas/api/contentsource/wikifake"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
	"github.com/pblumer/atlas/logging"
)

// A product's content from an external source, end to end through the
// catalogue: linking, the read-only fields, the recheck and its quarantine, the
// withholding on every surface this package serves, and the publish gate
// (ADR-0491).

const minute = int64(60e9)

// boundFixture is a catalogue in German and French, a product homed in it, and a
// wiki keeping the product as one page per language.
type boundFixture struct {
	s     *Service
	wiki  *wikifake.Server
	reg   *contentsource.Registry
	now   *atomic.Int64
	cat   Catalog
	owner *httpapi.Principal
}

func newBoundFixture(t *testing.T) *boundFixture {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })
	now := &atomic.Int64{}
	now.Store(1_000 * minute)
	s := New(loop, store, func() int64 { return now.Load() },
		func(p *httpapi.Principal) bool { return p != nil && p.HasRole("admin") })

	f := wikifake.New()
	t.Cleanup(f.Close)
	f.Put(wikifake.Page{ID: "it_root", Key: "SVC-1", Space: "SVC", Title: "Services"})
	start := len([]rune("Sicherer Zugang für "))
	f.Put(wikifake.Page{ID: "it_de", Key: "SVC-12", Space: "SVC", Parent: "it_root", Title: "VPN-DE",
		Labels: []string{"vpn", "withdrawn"}, Properties: map[string]any{"bild": "logo.png"},
		Body: []wikifake.Block{{Kind: "paragraph", Text: "Sicherer Zugang für Kennwort 4711 alle.",
			Marks: []wikifake.Mark{{Type: "secret", Start: start, End: start + len([]rune("Kennwort 4711")), Ref: "sec_1"}}}}})
	f.Put(wikifake.Page{ID: "it_de1", Key: "SVC-14", Space: "SVC", Parent: "it_de", Title: "Details",
		Body: []wikifake.Block{{Kind: "paragraph", Text: "Für Büro und unterwegs."}}})
	f.Put(wikifake.Page{ID: "it_de2", Key: "SVC-15", Space: "SVC", Parent: "it_de", Title: "Intern",
		ReadRestriction: []string{"u_atlas"}, Body: []wikifake.Block{{Kind: "paragraph", Text: "Kosten intern."}}})
	f.Put(wikifake.Page{ID: "it_fr", Key: "SVC-13", Space: "SVC", Parent: "it_root", Title: "VPN-FR",
		Labels: []string{"accès"}, Body: []wikifake.Block{{Kind: "paragraph", Text: "Accès sécurisé."}}})
	f.Put(wikifake.Page{ID: "it_other", Key: "SVC-30", Space: "SVC", Parent: "it_root", Title: "Mail",
		Body: []wikifake.Block{{Kind: "paragraph", Text: "Post."}}})
	f.Attach("it_de", wikifake.Attachment{ID: "att_1", Name: "logo.png", Type: "image/png", Data: []byte(onePNG)})

	cstore, err := contentsource.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg := contentsource.NewRegistry(loop, cstore, func(ref string) string {
		if ref == "wiki-token" {
			return wikifake.Token
		}
		return ""
	})
	reg.Transport = f.TS.Client().Transport
	loop.Do(func() {
		_ = cstore.Save(contentsource.Source{ID: "wiki", Name: "Wiki", Kind: contentsource.KindWikiWikiBangBang,
			BaseURL: f.URL(), CredentialsRef: "wiki-token", PictureField: "bild", ExcludedLabels: []string{"withdrawn"}})
	})
	s.Content = reg

	owner := user("usr_owner")
	cat := makeCatalog(t, s, owner)
	if rec := as(t, s.HandleUpdateCatalog, owner, "PATCH", `{"languages":["de","fr"],"groups":["grp_shop"]}`, "id", cat.ID); rec.Code != 200 {
		t.Fatalf("patch = %d %s", rec.Code, rec.Body)
	}
	for _, id := range []string{"vpn", "mail"} {
		body := `{"id":"` + id + `","homeCatalog":"` + cat.ID + `","state":"active","texts":{"de":"VPN","fr":"VPN"},` +
			`"approval":{"kind":"none"},"provisionProcess":"p","deprovisionProcess":"d","price":"CHF 5"}`
		if rec := as(t, s.HandleSaveItem, owner, "POST", body); rec.Code != 200 {
			t.Fatalf("save = %d %s", rec.Code, rec.Body)
		}
	}
	if rec := as(t, s.HandleUpdateCatalog, owner, "PATCH", `{"items":["vpn","mail"]}`, "id", cat.ID); rec.Code != 200 {
		t.Fatalf("offer = %d %s", rec.Code, rec.Body)
	}
	return &boundFixture{s: s, wiki: f, reg: reg, now: now, cat: cat, owner: owner}
}

func (b *boundFixture) link(t *testing.T, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	return as(t, b.s.HandleContentLink, b.owner, "POST", body, "id", id)
}

func (b *boundFixture) linkVPN(t *testing.T) Item {
	t.Helper()
	rec := b.link(t, "vpn", `{"source":"wiki","primary":"de","pages":{"de":"https://`+b.wiki.Host()+`/#/item/SVC-12","fr":"SVC-13"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("link = %d %s", rec.Code, rec.Body)
	}
	return decode[Item](t, rec)
}

func (b *boundFixture) item(t *testing.T, id string) Item {
	t.Helper()
	var it Item
	b.s.loop.Do(func() { it, _, _ = b.s.store.Item(id) })
	return it
}

func (b *boundFixture) state(t *testing.T, id string) ContentState {
	t.Helper()
	var st ContentState
	b.s.loop.Do(func() { st, _, _ = b.s.store.ContentState(id) })
	return st
}

func (b *boundFixture) publish(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	return as(t, b.s.HandlePublish, b.owner, "POST", "", "id", b.cat.ID)
}

// releases reads the releases as a customer of the catalogue does.
func (b *boundFixture) releases(t *testing.T) []Release {
	t.Helper()
	rec := as(t, b.s.HandleListReleases, user("usr_customer", "grp_shop"), "GET", "", "id", b.cat.ID)
	if rec.Code != 200 {
		t.Fatalf("releases = %d %s", rec.Code, rec.Body)
	}
	return decode[[]Release](t, rec)
}

func itemIn(rel Release, id string) Item {
	for _, it := range rel.Items {
		if it.ID == id {
			return it
		}
	}
	return Item{}
}

func (b *boundFixture) picture(t *testing.T, id string) int {
	t.Helper()
	return as(t, b.s.HandleGetPicture, user("usr_customer", "grp_shop"), "GET", "", "id", id).Code
}

func (b *boundFixture) sweep(t *testing.T) {
	t.Helper()
	r := contentsource.NewRunner(b.reg, b.s.Rechecks(), func() int64 { return b.now.Load() })
	if err := r.Sweep(context.Background(), "wiki"); err != nil {
		t.Fatalf("sweep: %v", err)
	}
}

func TestWithNoSourceNothingIsBound(t *testing.T) {
	s := serviceWithAdmin(t)
	owner := user("usr_owner")
	cat := makeCatalog(t, s, owner)
	id := makeItem(t, s, owner, cat.ID)
	rec := as(t, s.HandleContentStatus, owner, "GET", "", "id", id)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"sources":[]`) {
		t.Fatalf("status = %d %s", rec.Code, rec.Body)
	}
	for name, h := range map[string]http.HandlerFunc{"link": s.HandleContentLink, "search": s.HandleContentSearch,
		"preview": s.HandleContentPreview, "refresh": s.HandleContentRefresh} {
		if rec := as(t, h, owner, "POST", `{}`, "id", id); rec.Code != http.StatusNotFound ||
			!strings.Contains(rec.Body.String(), "no content source is configured") {
			t.Errorf("%s = %d %s", name, rec.Code, rec.Body)
		}
	}
	if rec := as(t, s.HandleCatalogContentRefresh, owner, "POST", "", "id", cat.ID); rec.Code != http.StatusNotFound {
		t.Errorf("catalogue refresh = %d", rec.Code)
	}
}

func TestLinkingWritesTheBindingAndTheContentInOneSave(t *testing.T) {
	b := newBoundFixture(t)
	before := b.item(t, "vpn")
	it := b.linkVPN(t)
	if it.Revision != before.Revision+1 {
		t.Fatalf("revision %d after %d: one save", it.Revision, before.Revision)
	}
	de := it.Descriptions["de"]
	if de != "Sicherer Zugang für alle.\n\nDetails\n\nFür Büro und unterwegs." {
		t.Fatalf("de = %q", de)
	}
	if strings.Contains(de, "4711") || strings.Contains(de, "Kosten") {
		t.Fatalf("protected content in the description: %q", de)
	}
	if it.Descriptions["fr"] != "Accès sécurisé." {
		t.Fatalf("fr = %q", it.Descriptions["fr"])
	}
	if strings.Join(it.Keywords, ",") != "accès,vpn" {
		t.Fatalf("keywords = %v", it.Keywords)
	}
	cs := it.ContentSource
	if cs == nil || cs.Source != "wiki" || cs.Primary != "de" || cs.Pages["de"].Ref != "it_de" ||
		cs.Pages["fr"].Ref != "it_fr" || cs.Pages["de"].FetchedAt != b.now.Load() {
		t.Fatalf("binding = %+v", cs)
	}
	if kids := cs.Pages["de"].Children; len(kids) != 1 || kids[0].Ref != "it_de1" {
		t.Fatalf("children = %+v", kids)
	}
	if b.picture(t, "vpn") != 200 {
		t.Fatal("the picture was not copied into the store")
	}
	if st := b.state(t, "vpn"); st.LastPassed != b.now.Load() || st.Quarantine != nil {
		t.Fatalf("state = %+v", st)
	}
	// Published and shown.
	if rec := b.publish(t); rec.Code != http.StatusCreated {
		t.Fatalf("publish = %d %s", rec.Code, rec.Body)
	}
	shown := itemIn(b.releases(t)[0], "vpn")
	if shown.Descriptions["de"] != de || len(shown.Keywords) != 2 {
		t.Fatalf("release shows %+v", shown)
	}
	// Link in shop is off: the binding is not shown to a customer.
	if shown.ContentSource != nil {
		t.Fatalf("binding shown without linkInShop: %+v", shown.ContentSource)
	}
}

func TestTheStatusAndThePreview(t *testing.T) {
	b := newBoundFixture(t)
	rec := as(t, b.s.HandleContentStatus, b.owner, "GET", "", "id", "vpn")
	var st contentStatus
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	if len(st.Sources) != 1 || st.Sources[0].ID != "wiki" || st.Binding != nil || !st.Shown ||
		strings.Contains(rec.Body.String(), "wiki-token") {
		t.Fatalf("status = %s", rec.Body)
	}
	rec = call(t, b.s.HandleContentPreview, b.owner, "GET", "/x?source=wiki&page=SVC-12", "", "id", "vpn")
	if rec.Code != 200 {
		t.Fatalf("preview = %d %s", rec.Code, rec.Body)
	}
	var pv contentPreview
	_ = json.Unmarshal(rec.Body.Bytes(), &pv)
	if pv.Page.ID != "it_de" || pv.Page.Title != "VPN-DE" || pv.Page.Version != 1 || len(pv.Refusals) != 0 ||
		strings.Join(pv.Keywords, ",") != "vpn" || len(pv.Dropped) != 2 {
		t.Fatalf("preview = %s", rec.Body)
	}
	rec = call(t, b.s.HandleContentSearch, b.owner, "GET", "/x?source=wiki&q=vpn", "", "id", "vpn")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "SVC-12") {
		t.Fatalf("search = %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, b.s.HandleContentSearch, b.owner, "GET", "/x?source=wiki", "", "id", "vpn"); rec.Code != 400 {
		t.Fatalf("empty search = %d", rec.Code)
	}
	if rec := call(t, b.s.HandleContentPreview, b.owner, "GET", "/x?source=nope&page=a", "", "id", "vpn"); rec.Code != 400 {
		t.Fatalf("unknown source = %d", rec.Code)
	}
	if rec := call(t, b.s.HandleContentPreview, b.owner, "GET", "/x?source=wiki", "", "id", "vpn"); rec.Code != 400 {
		t.Fatalf("no page = %d", rec.Code)
	}
	// A restricted page previews with its refusal, and a page another product
	// is bound to says which.
	b.linkVPN(t)
	rec = call(t, b.s.HandleContentPreview, b.owner, "GET", "/x?source=wiki&page=SVC-13", "", "id", "mail")
	_ = json.Unmarshal(rec.Body.Bytes(), &pv)
	if pv.BoundTo != "vpn" {
		t.Fatalf("boundTo = %q", pv.BoundTo)
	}
	rec = call(t, b.s.HandleContentPreview, b.owner, "GET", "/x?source=wiki&page=SVC-15", "", "id", "mail")
	_ = json.Unmarshal(rec.Body.Bytes(), &pv)
	if len(pv.Refusals) == 0 || !strings.Contains(pv.Refusals[0], "read restriction") {
		t.Fatalf("restricted preview = %s", rec.Body)
	}
}

func TestWhatLinkingRefuses(t *testing.T) {
	b := newBoundFixture(t)
	cases := []struct {
		name, body string
		code       int
		want       string
	}{
		{"a restricted page", `{"source":"wiki","pages":{"de":"SVC-15"}}`, 422, "read restriction"},
		{"an unreadable page", `{"source":"wiki","pages":{"de":"SVC-404"}}`, 422, "cannot be read"},
		{"another host", `{"source":"wiki","pages":{"de":"https://evil.example.org/#/item/SVC-12"}}`, 502, "not on"},
		{"the same page twice", `{"source":"wiki","primary":"de","pages":{"de":"SVC-12","fr":"it_de"}}`, 422, "already the de page"},
		{"an undeclared language", `{"source":"wiki","pages":{"it":"SVC-12"}}`, 400, "declares no language it"},
		{"no pages", `{"source":"wiki"}`, 400, "at least one"},
		{"a blank page", `{"source":"wiki","pages":{"de":" "}}`, 400, "names no page"},
		{"a primary without a slot", `{"source":"wiki","primary":"fr","pages":{"de":"SVC-12"}}`, 400, "primary"},
		{"an unknown source", `{"source":"other","pages":{"de":"SVC-12"}}`, 400, "no content source other"},
		{"a stale revision", `{"source":"wiki","pages":{"de":"SVC-12"},"revision":99}`, 409, "changed since"},
		{"a broken body", `{`, 400, "malformed"},
	}
	for _, c := range cases {
		rec := b.link(t, "vpn", c.body)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body)
		}
	}
	if it := b.item(t, "vpn"); it.ContentSource != nil || it.Descriptions != nil {
		t.Fatalf("a refused link wrote %+v", it)
	}
	// A page bound to another product is refused, naming it.
	b.linkVPN(t)
	rec := b.link(t, "mail", `{"source":"wiki","pages":{"de":"SVC-12"}}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "product vpn") {
		t.Fatalf("bound elsewhere = %d %s", rec.Code, rec.Body)
	}
	// So is a child page another product's description holds.
	rec = b.link(t, "mail", `{"source":"wiki","pages":{"de":"SVC-14"}}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("child bound elsewhere = %d %s", rec.Code, rec.Body)
	}
	// The source unreachable: nothing is written.
	b.wiki.Set(func(s *wikifake.Server) { s.Down = true })
	if rec := b.link(t, "mail", `{"source":"wiki","pages":{"de":"SVC-30"}}`); rec.Code != http.StatusBadGateway {
		t.Fatalf("down = %d %s", rec.Code, rec.Body)
	}
}

func TestWhileBoundTheSuppliedFieldsAreReadOnly(t *testing.T) {
	b := newBoundFixture(t)
	it := b.linkVPN(t)
	body := func(mut func(map[string]any)) string {
		raw, _ := json.Marshal(it)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		mut(m)
		out, _ := json.Marshal(m)
		return string(out)
	}
	// Sent back unchanged, as an agent's read-modify-write does: saved.
	rec := as(t, b.s.HandleSaveItem, b.owner, "POST", body(func(m map[string]any) { m["price"] = "CHF 9" }))
	if rec.Code != 200 {
		t.Fatalf("unchanged save = %d %s", rec.Code, rec.Body)
	}
	it = decode[Item](t, rec)
	// Left out, as the Console's form does: the stored values are kept.
	rec = as(t, b.s.HandleSaveItem, b.owner, "POST", body(func(m map[string]any) {
		delete(m, "descriptions")
		delete(m, "keywords")
		delete(m, "contentSource")
	}))
	if rec.Code != 200 {
		t.Fatalf("omitting save = %d %s", rec.Code, rec.Body)
	}
	kept := decode[Item](t, rec)
	if kept.ContentSource == nil || kept.Descriptions["fr"] != "Accès sécurisé." || len(kept.Keywords) != 2 {
		t.Fatalf("omitting dropped the source's fields: %+v", kept)
	}
	it = kept
	for name, mut := range map[string]func(map[string]any){
		"descriptions": func(m map[string]any) { m["descriptions"] = map[string]any{"de": "Eigener Text"} },
		"keywords":     func(m map[string]any) { m["keywords"] = []any{"eigen"} },
		"binding": func(m map[string]any) {
			m["contentSource"] = map[string]any{"source": "wiki", "pages": map[string]any{}}
		},
	} {
		rec := as(t, b.s.HandleSaveItem, b.owner, "POST", body(mut))
		if rec.Code != http.StatusConflict {
			t.Errorf("changing %s = %d %s", name, rec.Code, rec.Body)
		}
	}
	// A picture of its own is refused too.
	if rec := asTyped(t, b.s.HandleSetPicture, b.owner, "PUT", "image/png", onePNG, "id", "vpn"); rec.Code != http.StatusConflict {
		t.Fatalf("set picture = %d", rec.Code)
	}
	if rec := as(t, b.s.HandleDeletePicture, b.owner, "DELETE", "", "id", "vpn"); rec.Code != http.StatusConflict {
		t.Fatalf("delete picture = %d", rec.Code)
	}
	// And a binding cannot be created by a save.
	rec = as(t, b.s.HandleSaveItem, b.owner, "POST", `{"id":"new","homeCatalog":"`+b.cat.ID+`","state":"active",`+
		`"texts":{"de":"N"},"approval":{"kind":"none"},"contentSource":{"source":"wiki","pages":{}}}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("binding by save = %d %s", rec.Code, rec.Body)
	}
	// Nor through an import.
	doc := `{"products":[` + body(func(m map[string]any) { m["keywords"] = []any{"eigen"} }) + `]}`
	if code, _ := importDoc(t, b.s, b.owner, doc); code == http.StatusOK {
		t.Fatalf("import changed a bound field: %d", code)
	}
}

// The recheck and the quarantine, read through every surface this package
// serves: the releases (the shop's listing, its detail and its keyword search),
// the picture, and the exports.
func TestAQuarantineWithholdsContentFromEverySurfaceAtOnce(t *testing.T) {
	b := newBoundFixture(t)
	b.linkVPN(t)
	if rec := b.publish(t); rec.Code != http.StatusCreated {
		t.Fatalf("publish = %d %s", rec.Code, rec.Body)
	}
	b.sweep(t)
	if st := b.state(t, "vpn"); st.Quarantine != nil {
		t.Fatalf("a clean sweep quarantined: %+v", st)
	}

	// The product owner restricts the root after the copy was taken.
	b.wiki.Edit("it_root", func(p *wikifake.Page) { p.ReadRestriction = []string{"u_atlas", "@staff"} })
	b.now.Add(minute)
	b.sweep(t)
	st := b.state(t, "vpn")
	if st.Quarantine == nil || !strings.Contains(st.Quarantine.Reason, "SVC-1") {
		t.Fatalf("state = %+v", st)
	}

	// Without a publish: the release withholds descriptions, keywords and the
	// binding, and keeps the name and the price.
	shown := itemIn(b.releases(t)[0], "vpn")
	if shown.Descriptions != nil || shown.Keywords != nil || shown.ContentSource != nil {
		t.Fatalf("quarantined content shown: %+v", shown)
	}
	if shown.Texts["de"] != "VPN" || shown.Price != "CHF 5" {
		t.Fatalf("name or price withheld: %+v", shown)
	}
	// An unbound product beside it is untouched.
	if other := itemIn(b.releases(t)[0], "mail"); other.Texts["de"] != "VPN" {
		t.Fatalf("an unbound product changed: %+v", other)
	}
	if code := b.picture(t, "vpn"); code != http.StatusNotFound {
		t.Fatalf("picture = %d", code)
	}
	// The export leaves Atlas, so it is outside the catalogue screen too.
	exp := exportOf(t, call(t, b.s.HandleExportCatalog, b.owner, "GET", "/x", "", "id", b.cat.ID))
	for _, it := range exp.Products {
		if it.ID == "vpn" && (it.Descriptions != nil || it.Keywords != nil || it.ContentSource != nil) {
			t.Fatalf("export carries quarantined content: %+v", it)
		}
	}
	exp = exportOf(t, call(t, b.s.HandleExportProducts, b.owner, "GET", "/x?ids=vpn", ""))
	if exp.Products[0].Descriptions != nil {
		t.Fatalf("product export carries quarantined content: %+v", exp.Products[0])
	}
	// The catalogue screen still shows the product manager what is in question.
	rec := as(t, b.s.HandleListItems, b.owner, "GET", "")
	if !strings.Contains(rec.Body.String(), "Sicherer Zugang") {
		t.Fatal("the catalogue screen lost the content")
	}
	status := as(t, b.s.HandleContentStatus, b.owner, "GET", "", "id", "vpn").Body.String()
	if !strings.Contains(status, `"quarantine"`) || !strings.Contains(status, `"shown":false`) {
		t.Fatalf("status = %s", status)
	}

	// Publishing refuses while it is quarantined.
	rec = b.publish(t)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "quarantined") {
		t.Fatalf("publish = %d %s", rec.Code, rec.Body)
	}

	// The restriction lifted: a recheck that passes again lifts nothing.
	b.wiki.Edit("it_root", func(p *wikifake.Page) { p.ReadRestriction = nil })
	b.now.Add(minute)
	b.sweep(t)
	if st := b.state(t, "vpn"); st.Quarantine == nil || st.LastPassed != b.now.Load() {
		t.Fatalf("a passing recheck lifted the quarantine: %+v", st)
	}
	if itemIn(b.releases(t)[0], "vpn").Descriptions != nil {
		t.Fatal("content shown again without a refresh")
	}

	// A refresh that passes every rule ends it.
	b.now.Add(minute)
	rec = as(t, b.s.HandleContentRefresh, b.owner, "POST", "", "id", "vpn")
	if rec.Code != 200 {
		t.Fatalf("refresh = %d %s", rec.Code, rec.Body)
	}
	if st := b.state(t, "vpn"); st.Quarantine != nil || st.Distrusted == 0 {
		t.Fatalf("state after refresh = %+v", st)
	}
	if b.picture(t, "vpn") != 200 {
		t.Fatal("the refreshed picture is withheld")
	}
	// The release published before it still holds the content in question, and
	// keeps withholding it; the next release shows the refreshed copy.
	if itemIn(b.releases(t)[0], "vpn").Descriptions != nil {
		t.Fatal("the copy in question is shown again")
	}
	if rec := b.publish(t); rec.Code != http.StatusCreated {
		t.Fatalf("publish after refresh = %d %s", rec.Code, rec.Body)
	}
	rels := b.releases(t)
	if itemIn(rels[0], "vpn").Descriptions["de"] == "" {
		t.Fatal("the refreshed release withholds")
	}
	// Only the newest release is rechecked, so only it shows source content.
	if itemIn(rels[1], "vpn").Descriptions != nil {
		t.Fatal("an older release shows source content")
	}
}

func TestAnUnreachableSourceQuarantines(t *testing.T) {
	b := newBoundFixture(t)
	b.linkVPN(t)
	b.wiki.Set(func(s *wikifake.Server) { s.Down = true })
	b.sweep(t)
	if st := b.state(t, "vpn"); st.Quarantine == nil || !strings.Contains(st.Quarantine.Reason, "cannot be reached") {
		t.Fatalf("state = %+v", st)
	}
	// A refresh while it is down fails and leaves the product as it was.
	before := b.item(t, "vpn")
	rec := as(t, b.s.HandleContentRefresh, b.owner, "POST", "", "id", "vpn")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("refresh = %d %s", rec.Code, rec.Body)
	}
	if after := b.item(t, "vpn"); after.Revision != before.Revision {
		t.Fatal("a failed refresh wrote")
	}
}

func TestARefreshRechecksFirstAndIsAllOrNothing(t *testing.T) {
	b := newBoundFixture(t)
	it := b.linkVPN(t)
	b.wiki.Edit("it_fr", func(p *wikifake.Page) { p.Held = true })
	rec := as(t, b.s.HandleContentRefresh, b.owner, "POST", `{"revision":`+itoa(it.Revision)+`}`, "id", "vpn")
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "SVC-13") {
		t.Fatalf("refresh = %d %s", rec.Code, rec.Body)
	}
	if b.item(t, "vpn").Revision != it.Revision {
		t.Fatal("a refused refresh wrote")
	}
	// The recheck before the refresh recorded what it found.
	if st := b.state(t, "vpn"); st.Quarantine == nil {
		t.Fatalf("state = %+v", st)
	}
	if rec := as(t, b.s.HandleContentRefresh, b.owner, "POST", `{"revision":1}`, "id", "vpn"); rec.Code != http.StatusConflict {
		t.Fatalf("stale refresh = %d", rec.Code)
	}
	if rec := as(t, b.s.HandleContentRefresh, b.owner, "POST", "", "id", "mail"); rec.Code != http.StatusConflict {
		t.Fatalf("refreshing an unbound product = %d", rec.Code)
	}
}

func TestPublishingNeedsARecentPassedRecheck(t *testing.T) {
	b := newBoundFixture(t)
	b.linkVPN(t)
	b.now.Add(16 * minute)
	rec := b.publish(t)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "no passed recheck") {
		t.Fatalf("publish = %d %s", rec.Code, rec.Body)
	}
	b.sweep(t)
	if rec := b.publish(t); rec.Code != http.StatusCreated {
		t.Fatalf("publish after the recheck = %d %s", rec.Code, rec.Body)
	}
	// A recheck that has stopped for two intervals withholds without a
	// quarantine, and the next one that passes shows it again.
	b.now.Add(31 * minute)
	if itemIn(b.releases(t)[0], "vpn").Descriptions != nil || b.picture(t, "vpn") != http.StatusNotFound {
		t.Fatal("content vouched for by a stale recheck is shown")
	}
	b.sweep(t)
	if itemIn(b.releases(t)[0], "vpn").Descriptions == nil || b.picture(t, "vpn") != http.StatusOK {
		t.Fatal("a passing recheck does not show it again")
	}
}

func TestUnlinkingIsRecordedAndDropsQuarantinedContent(t *testing.T) {
	sink := captureCatalogLog(t)
	b := newBoundFixture(t)
	it := b.linkVPN(t)
	if rec := as(t, b.s.HandleContentUnlink, b.owner, "POST", `{"lang":"fr"}`, "id", "vpn"); rec.Code != 400 {
		t.Fatalf("unlink without reason = %d", rec.Code)
	}
	if rec := as(t, b.s.HandleContentUnlink, b.owner, "POST", `{"lang":"de","reason":"x"}`, "id", "vpn"); rec.Code != http.StatusConflict {
		t.Fatalf("unlinking the primary alone = %d", rec.Code)
	}
	if rec := as(t, b.s.HandleContentUnlink, b.owner, "POST", `{"lang":"it","reason":"x"}`, "id", "vpn"); rec.Code != 400 {
		t.Fatalf("unlinking a slot that is not there = %d", rec.Code)
	}
	if rec := as(t, b.s.HandleContentUnlink, b.owner, "POST", `{"reason":"x","revision":1}`, "id", "vpn"); rec.Code != http.StatusConflict {
		t.Fatalf("stale unlink = %d", rec.Code)
	}
	rec := as(t, b.s.HandleContentUnlink, b.owner, "POST", `{"lang":"fr","reason":"Französisch kommt aus dem Katalog","revision":`+itoa(it.Revision)+`}`, "id", "vpn")
	if rec.Code != 200 {
		t.Fatalf("unlink fr = %d %s", rec.Code, rec.Body)
	}
	got := decode[Item](t, rec)
	if _, still := got.Descriptions["fr"]; still || got.ContentSource.Pages["fr"].Ref != "" || strings.Join(got.Keywords, ",") != "vpn" {
		t.Fatalf("after the slot unlink = %+v", got)
	}
	if !strings.Contains(sink.String(), `"event":"content.unlinked"`) || !strings.Contains(sink.String(), "Französisch kommt aus dem Katalog") ||
		!strings.Contains(sink.String(), `"actor_id":"usr_owner"`) {
		t.Fatalf("no audit record: %s", sink.String())
	}

	// Quarantined, then unlinked whole: the content in question is dropped.
	b.wiki.Edit("it_de", func(p *wikifake.Page) { p.Held = true })
	b.sweep(t)
	rec = as(t, b.s.HandleContentUnlink, b.owner, "POST", `{"reason":"Seite gesperrt"}`, "id", "vpn")
	if rec.Code != 200 {
		t.Fatalf("unlink = %d %s", rec.Code, rec.Body)
	}
	got = decode[Item](t, rec)
	if got.ContentSource != nil || got.Descriptions != nil || got.Keywords != nil {
		t.Fatalf("quarantined content kept: %+v", got)
	}
	var has bool
	b.s.loop.Do(func() { _, _, has, _ = b.s.store.Picture("vpn") })
	if has {
		t.Fatal("the quarantined picture was kept")
	}
	if rec := as(t, b.s.HandleContentUnlink, b.owner, "POST", `{"reason":"x"}`, "id", "vpn"); rec.Code != http.StatusConflict {
		t.Fatalf("unlinking an unbound product = %d", rec.Code)
	}
	// Unbound, the fields are the product manager's again.
	rec = as(t, b.s.HandleSaveItem, b.owner, "POST", `{"id":"vpn","homeCatalog":"`+b.cat.ID+`","state":"active",`+
		`"texts":{"de":"VPN","fr":"VPN"},"approval":{"kind":"none"},"provisionProcess":"p","deprovisionProcess":"d","descriptions":{"de":"Eigen","fr":"Propre"}}`)
	if rec.Code != 200 {
		t.Fatalf("save after unlink = %d %s", rec.Code, rec.Body)
	}
}

func TestUnlinkingWithoutQuarantineKeepsTheLastValues(t *testing.T) {
	b := newBoundFixture(t)
	b.linkVPN(t)
	rec := as(t, b.s.HandleContentUnlink, b.owner, "POST", `{"reason":"we write it ourselves"}`, "id", "vpn")
	got := decode[Item](t, rec)
	if rec.Code != 200 || got.ContentSource != nil || got.Descriptions["fr"] != "Accès sécurisé." || len(got.Keywords) != 2 {
		t.Fatalf("unlink = %d %+v", rec.Code, got)
	}
	unlinked := ""
	b.s.BindingChanged = func(src string) { unlinked = src }
	b.linkVPN(t)
	as(t, b.s.HandleContentUnlink, b.owner, "POST", `{"lang":"de","reason":"x"}`, "id", "vpn")
	as(t, b.s.HandleContentUnlink, b.owner, "POST", `{"lang":"fr","reason":"x"}`, "id", "vpn")
	if got := b.item(t, "vpn"); got.ContentSource == nil || len(got.ContentSource.Pages) != 1 {
		t.Fatalf("after the fr unlink = %+v", got.ContentSource)
	}
	// The last slot is the whole binding.
	rec = as(t, b.s.HandleContentUnlink, b.owner, "POST", `{"lang":"de","reason":"x"}`, "id", "vpn")
	if got := decode[Item](t, rec); got.ContentSource != nil || unlinked != "wiki" {
		t.Fatalf("after the last slot = %+v %q", got.ContentSource, unlinked)
	}
}

func TestACatalogueRefreshesEveryBoundProduct(t *testing.T) {
	b := newBoundFixture(t)
	b.linkVPN(t)
	if rec := b.link(t, "mail", `{"source":"wiki","pages":{"de":"SVC-30"}}`); rec.Code != 200 {
		t.Fatalf("link mail = %d %s", rec.Code, rec.Body)
	}
	b.wiki.Edit("it_other", func(p *wikifake.Page) { p.Presence = "trashed" })
	rec := as(t, b.s.HandleCatalogContentRefresh, b.owner, "POST", "", "id", b.cat.ID)
	var lines []catalogRefresh
	_ = json.Unmarshal(rec.Body.Bytes(), &lines)
	if rec.Code != 200 || len(lines) != 2 {
		t.Fatalf("refresh = %d %s", rec.Code, rec.Body)
	}
	byItem := map[string]catalogRefresh{}
	for _, l := range lines {
		byItem[l.Item] = l
	}
	if byItem["vpn"].Status != "refreshed" || byItem["mail"].Status != "refused" {
		t.Fatalf("lines = %+v", lines)
	}
	// A viewer of the catalogue may not.
	if rec := as(t, b.s.HandleCatalogContentRefresh, user("usr_customer", "grp_shop"), "POST", "", "id", b.cat.ID); rec.Code != http.StatusForbidden {
		t.Fatalf("customer refresh = %d", rec.Code)
	}
}

func TestBindingsTheRecheckCovers(t *testing.T) {
	b := newBoundFixture(t)
	b.linkVPN(t)
	refs, err := b.s.Rechecks().Referenced()
	if err != nil || strings.Join(refs, ",") != "wiki" {
		t.Fatalf("referenced = %v %v", refs, err)
	}
	targets, _ := b.s.Rechecks().Targets("wiki")
	if len(targets) != 1 || targets[0].Item != "vpn" || len(targets[0].Checks) != 3 {
		t.Fatalf("targets = %+v", targets)
	}
	bound, _ := b.s.BoundTo("wiki")
	if strings.Join(bound, ",") != "vpn" {
		t.Fatalf("bound = %v", bound)
	}
	if other, _ := b.s.Rechecks().Targets("elsewhere"); len(other) != 0 {
		t.Fatalf("other source = %+v", other)
	}
}

func TestLinkInShopShowsOnlyTheAddresses(t *testing.T) {
	b := newBoundFixture(t)
	b.linkVPN(t)
	b.publish(t)
	b.s.Content = linkingSources{ContentSources: b.s.Content}
	shown := itemIn(b.releases(t)[0], "vpn")
	if shown.ContentSource == nil || shown.ContentSource.Pages["de"].URL == "" || shown.ContentSource.Pages["de"].Passages != nil ||
		shown.ContentSource.Pages["de"].FetchedAt != 0 {
		t.Fatalf("public binding = %+v", shown.ContentSource)
	}
}

// TestTheShopsWikiLinkIsWithheldExactlyWithTheContent holds the shop's "more in the
// wiki" to the quarantine: the page address travels in the release's binding, and the
// binding is withheld whenever the description is — quarantined, or vouched for by no
// recent recheck — so the shop cannot link to a page whose content it may not show.
func TestTheShopsWikiLinkIsWithheldExactlyWithTheContent(t *testing.T) {
	b := newBoundFixture(t)
	b.linkVPN(t)
	b.publish(t)
	b.s.Content = linkingSources{ContentSources: b.s.Content}
	linked := func() (bool, bool) {
		it := itemIn(b.releases(t)[0], "vpn")
		return it.ContentSource != nil && it.ContentSource.Pages["de"].URL != "", it.Descriptions != nil
	}
	if url, desc := linked(); !url || !desc {
		t.Fatalf("shown content without its link: url %v, description %v", url, desc)
	}

	// No recheck for longer than the stale limit: both go, without a quarantine.
	b.now.Add(staleAfter + minute)
	if url, desc := linked(); url || desc {
		t.Fatalf("a stale recheck still shows: url %v, description %v", url, desc)
	}
	b.sweep(t)
	if url, desc := linked(); !url || !desc {
		t.Fatalf("a passing recheck did not show them again: url %v, description %v", url, desc)
	}

	// Quarantined: both go at once.
	b.wiki.Edit("it_root", func(p *wikifake.Page) { p.ReadRestriction = []string{"u_atlas"} })
	b.now.Add(minute)
	b.sweep(t)
	if url, desc := linked(); url || desc {
		t.Fatalf("a quarantined product still shows: url %v, description %v", url, desc)
	}
}

// linkingSources answers every source as one the shop may link to.
type linkingSources struct{ ContentSources }

func (l linkingSources) Sources() ([]contentsource.Source, error) {
	all, err := l.ContentSources.Sources()
	for i := range all {
		all[i].LinkInShop = true
	}
	return all, err
}

func TestTheWithholdingRules(t *testing.T) {
	b := &ContentSource{Pages: map[string]PageRef{"de": {FetchedAt: 100}, "fr": {FetchedAt: 50}}}
	if b.Oldest() != 50 {
		t.Fatal("Oldest")
	}
	now := int64(200)
	cases := []struct {
		name  string
		st    ContentState
		found bool
		want  bool
	}{
		{"no record", ContentState{}, false, false},
		{"passed recently", ContentState{LastPassed: 190}, true, true},
		{"never passed", ContentState{}, true, false},
		{"quarantined", ContentState{LastPassed: 190, Quarantine: &Quarantine{Since: 150}}, true, false},
		{"copied before a lifted quarantine", ContentState{LastPassed: 190, Distrusted: 60}, true, false},
		{"copied after a lifted quarantine", ContentState{LastPassed: 190, Distrusted: 40}, true, true},
	}
	for _, c := range cases {
		if got := contentShown(b, c.st, c.found, now); got != c.want {
			t.Errorf("%s: shown = %v", c.name, got)
		}
	}
	if !contentShown(nil, ContentState{}, false, now) {
		t.Error("an unbound product is always shown")
	}
	if contentShown(b, ContentState{LastPassed: 1}, true, 1+staleAfter+1) {
		t.Error("a stale recheck vouches")
	}
	if !pictureShown(Item{}, ContentState{}, false, now) || pictureShown(Item{}, ContentState{Quarantine: &Quarantine{}}, true, now) {
		t.Error("pictureShown of an unbound product")
	}
	if publicBinding(b, false) != nil || publicBinding(nil, true) != nil {
		t.Error("publicBinding")
	}
	var nilBinding *ContentSource
	if nilBinding.Copy() != nil {
		t.Error("Copy of nil")
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

// captureCatalogLog points the process logger at a buffer, as the api package's
// audit tests do.
func captureCatalogLog(t *testing.T) *strings.Builder {
	t.Helper()
	sink := &logSink{}
	if err := logging.Setup(sink, logging.FormatJSON, logging.DefaultLevel); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logging.Setup(os.Stderr, logging.DefaultFormat, logging.DefaultLevel) })
	return &sink.Builder
}

type logSink struct{ strings.Builder }
