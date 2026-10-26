package contentsource

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/contentsource/wikifake"
)

// onePNG is the smallest PNG the picture store accepts.
var onePNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89" +
	"\x00\x00\x00\rIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N\x00\x00\x00\x00IEND\xaeB`\x82")

// wiki is a fake with a service kept as one page per language, each with child
// pages, below a shared root.
func wiki(t *testing.T) *wikifake.Server {
	t.Helper()
	f := wikifake.New()
	t.Cleanup(f.Close)
	f.Put(wikifake.Page{ID: "it_root", Key: "SVC-1", Space: "SVC", Title: "Services"})
	secretStart := len([]rune("Sicherer Zugang für Büro und "))
	f.Put(wikifake.Page{ID: "it_de", Key: "SVC-12", Space: "SVC", Parent: "it_root", Title: "Service X-DE",
		Labels: []string{"vpn", "withdrawn", "Fernzugriff"}, Properties: map[string]any{"bild": "logo.png, zwei.png"},
		Body: []wikifake.Block{
			{Kind: "heading", Text: "Was es ist"},
			{Kind: "paragraph", Text: "Sicherer Zugang für Büro und Kennwort 4711 unterwegs.",
				Marks: []wikifake.Mark{{Type: "secret", Start: secretStart, End: secretStart + len([]rune("Kennwort 4711")), Ref: "sec_pw"}}},
			{Kind: "embed"},
		}})
	f.Put(wikifake.Page{ID: "it_c1", Key: "SVC-20", Space: "SVC", Parent: "it_de", Title: "Details",
		Body: []wikifake.Block{{Kind: "paragraph", Text: "Für alle Mitarbeitenden."}}})
	f.Put(wikifake.Page{ID: "it_c2", Key: "SVC-21", Space: "SVC", Parent: "it_de", Title: "Intern",
		ReadRestriction: []string{"u_atlas"}, Body: []wikifake.Block{{Kind: "paragraph", Text: "Interne Kosten."}}})
	f.Put(wikifake.Page{ID: "it_c3", Key: "SVC-22", Space: "SVC", Parent: "it_c2", Title: "Unter Intern",
		Body: []wikifake.Block{{Kind: "paragraph", Text: "Auch intern."}}})
	f.Put(wikifake.Page{ID: "it_c4", Key: "SVC-23", Space: "SVC", Parent: "it_de", Title: "Anhang",
		EditRestriction: []string{"u_owner"}, Body: []wikifake.Block{{Kind: "paragraph", Text: "Nur lesen."}}})
	f.Put(wikifake.Page{ID: "it_fr", Key: "SVC-13", Space: "SVC", Parent: "it_root", Title: "Service X-FR",
		Labels: []string{"vpn", "accès"}, Body: []wikifake.Block{{Kind: "paragraph", Text: "Accès sécurisé."}}})
	f.Attach("it_de", wikifake.Attachment{ID: "att_1", Name: "logo.png", Type: "image/png", Data: onePNG})
	return f
}

func adapter(t *testing.T, f *wikifake.Server) *WikiWikiBangBang {
	t.Helper()
	ad, err := NewWikiWikiBangBang(f.URL(), wikifake.Token, f.TS.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	return ad
}

func testSource(f *wikifake.Server) Source {
	return Source{ID: "wiki", Name: "Wiki", Kind: KindWikiWikiBangBang, BaseURL: f.URL(), CredentialsRef: "wiki",
		PictureField: "bild", ExcludedLabels: []string{"withdrawn"}}.Normalized()
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestAPreviewTakesThePageAndItsReadableChildrenDepthFirst(t *testing.T) {
	f := wiki(t)
	ad := adapter(t, f)
	st, err := ad.Standing(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	l, err := Preview(ctx(t), ad, st, "https://"+f.Host()+"/#/item/SVC-12")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Refusals) != 0 {
		t.Fatalf("refused: %v", l.Refusals)
	}
	if l.Page.ID != "it_de" || l.Page.Title != "Service X-DE" || l.Page.Version != 1 {
		t.Fatalf("page = %+v", l.Page)
	}
	want := "Was es ist\n\nSicherer Zugang für Büro und unterwegs.\n\nDetails\n\nFür alle Mitarbeitenden.\n\nAnhang\n\nNur lesen."
	if l.Text != want {
		t.Fatalf("text =\n%q\nwant\n%q", l.Text, want)
	}
	if strings.Contains(l.Text, "4711") || strings.Contains(l.Text, "Intern") {
		t.Fatalf("the passage or the restricted subtree leaked: %q", l.Text)
	}
	var kids []string
	for _, c := range l.Children {
		kids = append(kids, c.ID)
	}
	// The restricted child is dropped with the page below it; the edit-restricted
	// one is taken, because reading stays open.
	if !reflect.DeepEqual(kids, []string{"it_c1", "it_c4"}) {
		t.Fatalf("children = %v", kids)
	}
	var dropped []string
	for _, d := range l.Dropped {
		dropped = append(dropped, d.Page+":"+d.What)
	}
	if !reflect.DeepEqual(dropped, []string{"SVC-12:passage", "SVC-12:embed", "SVC-21:page"}) {
		t.Fatalf("dropped = %v", dropped)
	}
	if !reflect.DeepEqual(l.Page.Passages, []string{"sec_pw"}) {
		t.Fatalf("passages = %v", l.Page.Passages)
	}
}

// For a reader who may not read the passage, the source answers the placeholder
// under the same mark. The preview is the same, and the guard does not fire.
func TestAPassageTheAccountCannotReadIsRemovedTheSameWay(t *testing.T) {
	f := wiki(t)
	f.Edit("it_de", func(p *wikifake.Page) {
		start := len([]rune("Sicherer Zugang für Büro und "))
		p.Body[1] = wikifake.Block{Kind: "paragraph", Text: "Sicherer Zugang für Büro und " + wikifake.Placeholder + " unterwegs.",
			Marks: []wikifake.Mark{{Type: "secret", Start: start, End: start + 8, Ref: "sec_pw"}}}
	})
	ad := adapter(t, f)
	st, _ := ad.Standing(ctx(t))
	l, err := Preview(ctx(t), ad, st, "SVC-12")
	if err != nil || len(l.Refusals) != 0 {
		t.Fatalf("preview: %v %v", err, l.Refusals)
	}
	if !strings.HasPrefix(l.Text, "Was es ist\n\nSicherer Zugang für Büro und unterwegs.") {
		t.Fatalf("text = %q", l.Text)
	}
}

func TestAnUnmarkedPlaceholderRefusesThePage(t *testing.T) {
	f := wiki(t)
	f.Edit("it_de", func(p *wikifake.Page) { p.Body[1].Marks = nil; p.Body[1].Text = "Zugang " + wikifake.Placeholder })
	ad := adapter(t, f)
	st, _ := ad.Standing(ctx(t))
	l, _ := Preview(ctx(t), ad, st, "it_de")
	if !strings.Contains(strings.Join(l.Refusals, "|"), "placeholder") {
		t.Fatalf("refusals = %v", l.Refusals)
	}
}

func TestARestrictionInheritedFromAnAncestorRefuses(t *testing.T) {
	f := wiki(t)
	f.Edit("it_root", func(p *wikifake.Page) { p.ReadRestriction = []string{"@staff", "u_atlas"} })
	ad := adapter(t, f)
	st, _ := ad.Standing(ctx(t))
	l, _ := Preview(ctx(t), ad, st, "SVC-12")
	if !strings.Contains(strings.Join(l.Refusals, "|"), "ancestor SVC-1 carries a read restriction") {
		t.Fatalf("refusals = %v", l.Refusals)
	}
}

func TestAnUnreadableAncestorRefuses(t *testing.T) {
	f := wiki(t)
	f.Edit("it_root", func(p *wikifake.Page) { p.Unreadable = true })
	ad := adapter(t, f)
	st, _ := ad.Standing(ctx(t))
	l, err := Preview(ctx(t), ad, st, "SVC-12")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(l.Refusals, "|"), "ancestor of the page cannot be read") {
		t.Fatalf("refusals = %v", l.Refusals)
	}
}

func TestAnUnreadablePageIsARefusalNotAnError(t *testing.T) {
	f := wiki(t)
	f.Edit("it_fr", func(p *wikifake.Page) { p.Unreadable = true })
	ad := adapter(t, f)
	st, _ := ad.Standing(ctx(t))
	for _, in := range []string{"SVC-13", "it_fr", "nope"} {
		l, err := Preview(ctx(t), ad, st, in)
		if err != nil || len(l.Refusals) != 1 || !strings.Contains(l.Refusals[0], "cannot be read") {
			t.Fatalf("%s: %v %v", in, err, l.Refusals)
		}
	}
}

func TestAPageInASpaceNotReleasedRefuses(t *testing.T) {
	f := wiki(t)
	f.Put(wikifake.Page{ID: "it_hr", Key: "HR-1", Space: "HR", Title: "Lohn"})
	ad := adapter(t, f)
	st, _ := ad.Standing(ctx(t))
	l, _ := Preview(ctx(t), ad, st, "HR-1")
	if !strings.Contains(strings.Join(l.Refusals, "|"), "space HR is not released") {
		t.Fatalf("refusals = %v", l.Refusals)
	}
}

func TestGatherIsAllOrNothing(t *testing.T) {
	f := wiki(t)
	ad := adapter(t, f)
	src := testSource(f)
	c, err := Gather(ctx(t), ad, src, map[string]string{"de": "SVC-12", "fr": "SVC-13"}, "de", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Keywords, []string{"accès", "fernzugriff", "vpn"}) {
		t.Fatalf("keywords = %v", c.Keywords)
	}
	if c.Picture == nil || c.Picture.Name != "logo.png" || c.Picture.Type != "image/png" {
		t.Fatalf("picture = %+v", c.Picture)
	}
	if c.Languages["fr"].Text != "Accès sécurisé." {
		t.Fatalf("fr = %q", c.Languages["fr"].Text)
	}

	// One page held: the whole binding is refused, naming the page and why.
	f.Edit("it_fr", func(p *wikifake.Page) { p.Held = true })
	_, err = Gather(ctx(t), ad, src, map[string]string{"de": "SVC-12", "fr": "SVC-13"}, "de", 1<<20)
	var refused *Refused
	if !errors.As(err, &refused) || len(refused.Problems) != 1 || refused.Problems[0].Lang != "fr" ||
		!strings.Contains(refused.Error(), "data protection") {
		t.Fatalf("err = %v", err)
	}
}

func TestGatherRefusesTheSamePageTwiceAndAMissingPrimary(t *testing.T) {
	f := wiki(t)
	ad := adapter(t, f)
	_, err := Gather(ctx(t), ad, testSource(f), map[string]string{"de": "SVC-12", "fr": "it_de"}, "de", 1<<20)
	if err == nil || !strings.Contains(err.Error(), "already the de page") {
		t.Fatalf("err = %v", err)
	}
	_, err = Gather(ctx(t), ad, testSource(f), map[string]string{"de": "SVC-12"}, "fr", 1<<20)
	if err == nil || !strings.Contains(err.Error(), "primary") {
		t.Fatalf("err = %v", err)
	}
	_, err = Gather(ctx(t), ad, testSource(f), map[string]string{}, "", 1<<20)
	if err == nil {
		t.Fatal("an empty binding was gathered")
	}
}

func TestGatherRefusesAWrongStanding(t *testing.T) {
	f := wiki(t)
	f.Set(func(s *wikifake.Server) {
		s.Spaces = append(s.Spaces, wikifake.Grant{Space: "SVC", Role: "editor", Via: "@pm"})
	})
	_, err := Gather(ctx(t), adapter(t, f), testSource(f), map[string]string{"de": "SVC-12"}, "de", 1<<20)
	if err == nil || !strings.Contains(err.Error(), "editor") {
		t.Fatalf("err = %v", err)
	}
}

func TestAPictureThatCannotBeTakenRefuses(t *testing.T) {
	f := wiki(t)
	src := testSource(f)
	ad := adapter(t, f)
	// Too large for the limit.
	if _, err := Gather(ctx(t), ad, src, map[string]string{"de": "SVC-12"}, "de", 10); err == nil ||
		!strings.Contains(err.Error(), "limit") {
		t.Fatalf("err = %v", err)
	}
	// Not an image.
	f.Attach("it_de", wikifake.Attachment{ID: "att_2", Name: "logo.png", Type: "image/png", Data: []byte("not a png")})
	if _, err := Gather(ctx(t), ad, src, map[string]string{"de": "SVC-12"}, "de", 1<<20); err == nil ||
		!strings.Contains(err.Error(), "not a PNG") {
		t.Fatalf("err = %v", err)
	}
	// Named and absent.
	f.Edit("it_de", func(p *wikifake.Page) { p.Properties = map[string]any{"bild": "fehlt.png"} })
	if _, err := Gather(ctx(t), ad, src, map[string]string{"de": "SVC-12"}, "de", 1<<20); err == nil ||
		!strings.Contains(err.Error(), "no readable attachment") {
		t.Fatalf("err = %v", err)
	}
	// Named by no field at all: no picture, and no refusal.
	src.PictureField = ""
	c, err := Gather(ctx(t), ad, src, map[string]string{"de": "SVC-12"}, "de", 1<<20)
	if err != nil || c.Picture != nil {
		t.Fatalf("no field: %v %+v", err, c.Picture)
	}
}

func TestRecheckFindsEveryProtectionAndAPassageAddedLater(t *testing.T) {
	f := wiki(t)
	ad := adapter(t, f)
	checks := []Check{{ID: "it_de", Passages: []string{"sec_pw"}}, {ID: "it_c1"}}
	reasons, read, err := Recheck(ctx(t), ad, checks)
	if err != nil || len(reasons) != 0 {
		t.Fatalf("a clean recheck: %v %v", reasons, err)
	}
	if !reflect.DeepEqual(read, []string{"it_c1", "it_de", "it_root"}) {
		t.Fatalf("read = %v", read)
	}

	f.Edit("it_c1", func(p *wikifake.Page) {
		p.Body[0].Marks = []wikifake.Mark{{Type: "secret", Start: 0, End: 3, Ref: "sec_new"}}
	})
	reasons, _, _ = Recheck(ctx(t), ad, checks)
	if len(reasons) != 1 || !strings.Contains(reasons[0], "protected after its content was copied") {
		t.Fatalf("reasons = %v", reasons)
	}

	f.Edit("it_root", func(p *wikifake.Page) { p.ReadRestriction = []string{"u_atlas"} })
	reasons, _, _ = Recheck(ctx(t), ad, checks)
	if !strings.Contains(strings.Join(reasons, "|"), "ancestor SVC-1") {
		t.Fatalf("reasons = %v", reasons)
	}

	f.Edit("it_de", func(p *wikifake.Page) { p.Unreadable = true })
	reasons, _, _ = Recheck(ctx(t), ad, checks)
	if !strings.Contains(strings.Join(reasons, "|"), "can no longer be read") {
		t.Fatalf("reasons = %v", reasons)
	}

	f.Set(func(s *wikifake.Server) { s.Scope = "full" })
	reasons, _, _ = Recheck(ctx(t), ad, checks)
	if len(reasons) == 0 || !strings.Contains(reasons[0], "scope") {
		t.Fatalf("reasons = %v", reasons)
	}

	f.Set(func(s *wikifake.Server) { s.Down = true })
	if _, _, err := Recheck(ctx(t), ad, checks); err == nil {
		t.Fatal("an unreachable source rechecked clean")
	}
}

// A passage in a block that is never taken protects nothing Atlas holds, so its
// presence at the time of the copy is no reason to quarantine later.
func TestAPassageInABlockNeverTakenDoesNotQuarantine(t *testing.T) {
	f := wiki(t)
	f.Edit("it_c1", func(p *wikifake.Page) {
		p.Body = append(p.Body, wikifake.Block{Kind: "table", Text: "geheim",
			Marks: []wikifake.Mark{{Type: "secret", Start: 0, End: 6, Ref: "sec_tab"}}})
	})
	reasons, _, err := Recheck(ctx(t), adapter(t, f), []Check{{ID: "it_c1"}})
	if err != nil || len(reasons) != 0 {
		t.Fatalf("reasons = %v %v", reasons, err)
	}
}

func TestWalkAncestorsRefusesACycle(t *testing.T) {
	read := func(_ context.Context, id string) (Page, error) {
		return Page{ID: id, Parent: map[string]string{"a": "b", "b": "a"}[id]}, nil
	}
	if _, err := WalkAncestors(context.Background(), read, Page{ID: "x", Parent: "a"}); !errors.Is(err, errChain) {
		t.Fatalf("err = %v", err)
	}
	deep := func(_ context.Context, id string) (Page, error) { return Page{ID: id, Parent: id + "x"}, nil }
	if _, err := WalkAncestors(context.Background(), deep, Page{ID: "p", Parent: "q"}); !errors.Is(err, errChain) {
		t.Fatalf("err = %v", err)
	}
}

// An adapter is unusable without https and a token, and never redirects away.
func TestTheAdapterKeepsToItsSource(t *testing.T) {
	f := wiki(t)
	if _, err := NewWikiWikiBangBang("http://"+f.Host(), "t", nil); err == nil {
		t.Fatal("plain http accepted")
	}
	if _, err := NewWikiWikiBangBang(f.URL(), " ", nil); err == nil {
		t.Fatal("no token accepted")
	}
	other := wikifake.New()
	defer other.Close()
	f.Set(func(s *wikifake.Server) { s.Redirect = other.URL() })
	_, err := adapter(t, f).Page(ctx(t), "it_de")
	if err == nil || !strings.Contains(err.Error(), "redirected") {
		t.Fatalf("err = %v", err)
	}
	for _, tok := range other.Tokens {
		if tok != "" {
			t.Fatal("the token reached another host")
		}
	}
	if strings.Contains(err.Error(), wikifake.Token) {
		t.Fatal("the token is in an error")
	}
}

func TestTheAdapterNeverPutsItsTokenInAnError(t *testing.T) {
	f := wiki(t)
	ad := adapter(t, f)
	f.Set(func(s *wikifake.Server) { s.Down = true })
	_, err := ad.Standing(ctx(t))
	if err == nil || strings.Contains(err.Error(), wikifake.Token) || !strings.Contains(err.Error(), "503") {
		t.Fatalf("err = %v", err)
	}
	bad, _ := NewWikiWikiBangBang(f.URL(), "wrong-token", f.TS.Client().Transport)
	f.Set(func(s *wikifake.Server) { s.Down = false })
	if _, err := bad.Standing(ctx(t)); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v", err)
	}
	for _, tok := range f.Tokens {
		if tok != wikifake.Token && tok != "wrong-token" {
			t.Fatalf("presented %q", tok)
		}
	}
	unreachable, _ := NewWikiWikiBangBang("https://127.0.0.1:1", wikifake.Token, nil)
	if _, err := unreachable.Standing(ctx(t)); err == nil || strings.Contains(err.Error(), wikifake.Token) {
		t.Fatalf("err = %v", err)
	}
}

func TestStandingFromTheTwoRoutes(t *testing.T) {
	f := wiki(t)
	ad := adapter(t, f)
	st, err := ad.Standing(ctx(t))
	if err != nil || len(CheckStanding(st)) != 0 {
		t.Fatalf("standing %+v %v %v", st, err, CheckStanding(st))
	}
	f.Set(func(s *wikifake.Server) { s.Admin = true })
	st, _ = ad.Standing(ctx(t))
	if !reflect.DeepEqual(st.SystemRoles, []string{"admin"}) {
		t.Fatalf("system roles = %v", st.SystemRoles)
	}
	f.Set(func(s *wikifake.Server) { s.Admin, s.System = false, []string{"erasure"}; s.Erasure = true })
	st, _ = ad.Standing(ctx(t))
	if !reflect.DeepEqual(st.SystemRoles, []string{"erasure"}) {
		t.Fatalf("system roles = %v", st.SystemRoles)
	}
	// A source without the roles query has not proved anything about the account.
	f.Set(func(s *wikifake.Server) { s.RolesMissing, s.System, s.Erasure = true, nil, false })
	st, err = ad.Standing(ctx(t))
	if err != nil || st.SpacesKnown || st.SystemRolesKnown || len(CheckStanding(st)) != 2 {
		t.Fatalf("standing %+v %v", st, err)
	}
}

func TestAPageTheSourceSaysNothingAboutIsUnknown(t *testing.T) {
	f := wiki(t)
	f.Put(wikifake.Page{ID: "it_x", Key: "SVC-99", Space: "SVC", OmitPresence: true})
	p, err := adapter(t, f).Page(ctx(t), "it_x")
	if err != nil || p.Withdrawn != Unknown || p.ReadRestricted != Absent || p.Held != Absent {
		t.Fatalf("page = %+v %v", p, err)
	}
	f.Put(wikifake.Page{ID: "it_t", Key: "SVC-98", Space: "SVC", Presence: "trashed"})
	p, _ = adapter(t, f).Page(ctx(t), "it_t")
	if p.Withdrawn != Present {
		t.Fatalf("a trashed page is withdrawn: %+v", p)
	}
	if _, err := adapter(t, f).Page(ctx(t), "../etc"); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("a path in an id: %v", err)
	}
}

func TestResolveRefusesAnotherHost(t *testing.T) {
	f := wiki(t)
	ad := adapter(t, f)
	for in, want := range map[string]string{
		"https://" + f.Host() + "/#/item/SVC-12":      "it_de",
		"https://" + f.Host() + "/#/item/SVC-12/edit": "it_de",
		"SVC-13": "it_fr",
		"it_fr":  "it_fr",
	} {
		got, err := ad.Resolve(ctx(t), in)
		if err != nil || got != want {
			t.Errorf("%s: %q %v", in, got, err)
		}
	}
	for _, in := range []string{"https://evil.example.org/#/item/SVC-12", "http://" + f.Host() + "/#/item/SVC-12",
		"https://" + f.Host() + "/#/space/SVC", "SVC 12", "a/b", "https://%zz"} {
		if _, err := ad.Resolve(ctx(t), in); err == nil {
			t.Errorf("%s resolved", in)
		}
	}
}

func TestSearchChildrenPagingAndTheFeed(t *testing.T) {
	f := wiki(t)
	ad := adapter(t, f)
	hits, err := ad.Search(ctx(t), "service x")
	if err != nil || len(hits) != 2 || hits[0].Key != "SVC-12" || !strings.HasSuffix(hits[0].URL, "/#/item/SVC-12") {
		t.Fatalf("hits = %+v %v", hits, err)
	}
	for i := range 150 {
		f.Put(wikifake.Page{ID: "it_k" + string(rune('a'+i/26)) + string(rune('a'+i%26)), Space: "SVC", Parent: "it_fr"})
	}
	kids, err := ad.Children(ctx(t), Page{ID: "it_fr", Space: "SVC"})
	if err != nil || len(kids) != 150 || kids[0] != "it_kaa" {
		t.Fatalf("children = %d %v", len(kids), err)
	}
	if _, err := ad.Children(ctx(t), Page{ID: "it_fr", Space: "a/b"}); err == nil {
		t.Fatal("a space with a slash was listed")
	}

	head, err := ad.Head(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := ad.Watch(ctx(t), "it_de"); err != nil {
		t.Fatal(err)
	}
	f.Edit("it_de", func(p *wikifake.Page) { p.Title = "neu" })
	got, err := ad.Changes(ctx(t), head, time.Second)
	if err != nil || len(got.Changes) != 1 || got.Changes[0].Item != "it_de" || got.Last != head+1 {
		t.Fatalf("changes = %+v %v", got, err)
	}
	f.Edit("it_de", func(p *wikifake.Page) { p.Unreadable = true })
	got, _ = ad.Changes(ctx(t), got.Last, time.Second)
	if len(got.Changes) != 1 || got.Changes[0].Item != "" {
		t.Fatalf("an unreadable watched page must be named by nobody: %+v", got)
	}
	// By id the watch ends even now that the page cannot be read; twice is fine.
	if err := ad.Unwatch(ctx(t), "it_de"); err != nil || len(f.Watched()) != 0 {
		t.Fatalf("unwatch: %v %v", err, f.Watched())
	}
	if err := ad.Unwatch(ctx(t), "it_de"); err != nil {
		t.Fatalf("second unwatch: %v", err)
	}
	if err := ad.Watch(ctx(t), "it_de"); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("watching an unreadable page: %v", err)
	}
	if ad.Watch(ctx(t), "a b") == nil || ad.Unwatch(ctx(t), "a b") == nil {
		t.Fatal("an id that is no id was sent")
	}
	if _, _, err := ad.Attachment(ctx(t), "it_fr", "none.png", 10); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("attachment: %v", err)
	}
	if _, _, err := ad.Attachment(ctx(t), "a/b", "x", 10); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("attachment: %v", err)
	}
}

func TestDialKind(t *testing.T) {
	if _, err := DialKind(Source{Kind: "confluence"}, "t", nil); err == nil {
		t.Fatal("dialled an unknown kind")
	}
	ad, err := DialKind(Source{Kind: KindWikiWikiBangBang, BaseURL: "https://wiki.example.org"}, "t", http.DefaultTransport)
	if err != nil || ad == nil {
		t.Fatal(err)
	}
}
