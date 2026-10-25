package carddav

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// The rules for what a listing may report as a member of the address book: on its host
// and scheme, one segment under its path, nothing else.
func TestIsMemberOf(t *testing.T) {
	coll := mustURL(t, "https://dav.example.ch/book/")
	for s, want := range map[string]bool{
		"https://dav.example.ch/book/a.vcf":      true,
		"https://DAV.example.ch/book/a.vcf":      true,
		"https://dav.example.ch/book/":           false,
		"https://dav.example.ch/book/sub/a.vcf":  false,
		"https://dav.example.ch/other/a.vcf":     false,
		"https://evil.example.ch/book/a.vcf":     false,
		"http://dav.example.ch/book/a.vcf":       false,
		"https://dav.example.ch/bookshelf/a.vcf": false,
		// An encoded slash is two segments once decoded; such a name could not be
		// addressed as a contact id either (catalog.CheckContactID).
		"https://dav.example.ch/book/a%2Fb.vcf": false,
	} {
		if got := isMemberOf(coll, mustURL(t, s)); got != want {
			t.Errorf("isMemberOf(%s) = %v, want %v", s, got, want)
		}
	}
}

func TestURLHelpers(t *testing.T) {
	base := mustURL(t, "https://dav.example.ch/dav/")
	if _, ok := (davResponse{}).href(base); ok {
		t.Error("a response without an href resolved")
	}
	if _, ok := (davResponse{Hrefs: []string{"http://[::1"}}).href(base); ok {
		t.Error("an href that is no URL resolved")
	}
	if hrefOf(nil, base) != nil || hrefOf(&hrefList{}, base) != nil || hrefOf(&hrefList{Hrefs: []string{"http://[::1"}}, base) != nil {
		t.Error("hrefOf resolved nothing into something")
	}
	if got := hrefOf(&hrefList{Hrefs: []string{" /p/ "}}, base); got.String() != "https://dav.example.ch/p/" {
		t.Errorf("hrefOf = %s", got)
	}
	c := collectionURL(mustURL(t, "https://dav.example.ch/a%20b?x=1#f"))
	if c.String() != "https://dav.example.ch/a%20b/" {
		t.Errorf("collectionURL = %s", c)
	}
	raw := mustURL(t, "https://dav.example.ch/a%2Fb")
	if got := collectionURL(raw).EscapedPath(); got != "/a%2Fb/" {
		t.Errorf("collectionURL kept no raw path: %s", got)
	}
	if _, err := memberURL(c, "%zz"); err == nil {
		t.Error("a malformed escape made a URL")
	}
	m, _ := memberURL(c, "x%40y.vcf")
	if m.EscapedPath() != "/a%20b/x%40y.vcf" || m.Path != "/a b/x@y.vcf" {
		t.Errorf("memberURL = %s (%s)", m.EscapedPath(), m.Path)
	}
	if lastSegment(mustURL(t, "https://h/a/b/")) != "b" {
		t.Error("lastSegment")
	}
	// A multistatus with several responses, none for the URL asked, describes nothing.
	ms := &multistatus{Responses: []davResponse{{Hrefs: []string{"/x/"}}, {Hrefs: []string{"/y/"}}}}
	if _, ok := responseFor(ms, base, mustURL(t, "https://dav.example.ch/z/")); ok {
		t.Error("responseFor found a response for a URL nobody answered for")
	}
	if !statusOK("") || !statusOK("HTTP/1.1 207 Multi-Status") || statusOK("HTTP/1.1 404 Not Found") {
		t.Error("statusOK")
	}
}

func TestReachable(t *testing.T) {
	c := &Client{}
	for _, c2 := range []struct {
		from, to string
		ok       bool
	}{
		{"https://a.example/", "https://b.example/", true},
		{"http://127.0.0.1/", "http://localhost:8080/", true},
		{"http://127.0.0.1/", "http://dav.example.ch/", false},
		{"https://a.example/", "http://127.0.0.1/", false},
		{"https://a.example/", "ftp://a.example/", false},
	} {
		err := c.reachable(mustURL(t, c2.from), mustURL(t, c2.to))
		if (err == nil) != c2.ok {
			t.Errorf("%s → %s: %v", c2.from, c2.to, err)
		}
	}
	if got := unwrapURLError(errors.New("plain")); got.Error() != "plain" {
		t.Errorf("unwrapURLError = %v", got)
	}
}

// What a listing reports beside the contacts is left out: a sub-collection, a member
// it answers 404 for, a resource that is not a vCard, and a href outside the book.
func TestMembersLeavesOutWhatIsNoContact(t *testing.T) {
	f := seeded(t)
	inner := f.srv.Config.Handler
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PROPFIND" && r.Header.Get("Depth") == "1" && strings.HasSuffix(r.URL.Path, "/kontakte/") {
			w.WriteHeader(http.StatusMultiStatus)
			_, _ = w.Write([]byte(`<d:multistatus xmlns:d="DAV:">` +
				`<d:response><d:href>/dav/addressbooks/anna/kontakte/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>` +
				`<d:response><d:href>/dav/addressbooks/anna/kontakte/anna.vcf</d:href><d:propstat><d:prop><d:getetag>"1"</d:getetag><d:getcontenttype>text/vcard</d:getcontenttype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>` +
				`<d:response><d:href>/dav/addressbooks/anna/kontakte/sub/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>` +
				`<d:response><d:href>/dav/addressbooks/anna/kontakte/weg.vcf</d:href><d:status>HTTP/1.1 404 Not Found</d:status></d:response>` +
				`<d:response><d:href>/dav/addressbooks/anna/kontakte/notiz.txt</d:href><d:propstat><d:prop><d:getcontenttype>text/plain</d:getcontenttype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>` +
				`<d:response><d:href>/anderswo/x.vcf</d:href><d:propstat><d:prop><d:getcontenttype>text/vcard</d:getcontenttype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>` +
				`<d:response><d:propstat><d:prop/></d:propstat></d:response>` +
				`</d:multistatus>`))
			return
		}
		inner.ServeHTTP(w, r)
	})
	if got := ids(list1(t, run1(t, registry(t, f, "", ""), Job{Operation: "list-contacts", MaxResults: 10}))); got != "anna.vcf" {
		t.Errorf("listing = %q", got)
	}
	coll := mustURL(t, f.srv.URL+"/dav/addressbooks/anna/kontakte/")
	c, _ := NewClient(coll.String(), f.bundle(""), Options{})
	if _, err := c.members(context.Background(), coll, 0); !errors.Is(err, errTooBig) {
		t.Errorf("a listing above its limit: %v", err)
	}
}

func TestGetCardOfSomethingElse(t *testing.T) {
	f := seeded(t)
	f.put("text.vcf", "das ist kein vcard")
	_, err := Run(context.Background(), Job{Connector: "verkauf", Operation: "get-contact", ContactID: "text.vcf", ResultVariable: "r"}, registry(t, f, "", ""))
	if err == nil || !strings.Contains(err.Error(), "no vCard") {
		t.Errorf("err = %v", err)
	}
}

func TestVCardEdges(t *testing.T) {
	c := mustParse(t, "BEGIN:VCARD\r\nEMAIL;TYPE=\"home,work\",pref:a@b.ch\r\nX-Feld:1\r\nx-feld:2\r\nEND:VCARD\r\n")
	if got := strings.Join(c.First("EMAIL").Types(), ","); got != "home,work,pref" {
		t.Errorf("types = %s", got)
	}
	// Two spellings of one custom field are one field, the first spelling's value.
	if got := jsonOf(project("x", c, nil)["fields"]); got != `{"Feld":"1"}` {
		t.Errorf("fields = %s", got)
	}
	c.Insert(99, &Prop{Name: "NOTE", Value: "ende"})
	c.Insert(-1, &Prop{Name: "NOTE", Value: "auch"})
	if c.Props[len(c.Props)-1].Value != "auch" || c.index(&Prop{}) != -1 {
		t.Error("Insert out of range appends; index of a stranger is -1")
	}
	if isDigits("") || !isDigits("123") {
		t.Error("isDigits")
	}
	// A card whose preferred address is replaced by one naming a type takes that type.
	a := mustParse(t, "BEGIN:VCARD\r\nVERSION:3.0\r\nADR;TYPE=HOME:;;Weg 1;Bern;;;\r\nEND:VCARD\r\n")
	if err := apply(a, map[string]any{"address": map[string]any{"city": "Thun", "type": "work"}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(a.Bytes()), "ADR;TYPE=WORK:;;;Thun;;;") {
		t.Errorf("out:\n%s", a.Bytes())
	}
	// A card with no address gets the one written.
	n := NewCard("u")
	_ = apply(n, map[string]any{"address": map[string]any{"city": "Chur"}, "emails": []any{}})
	if !strings.Contains(string(n.Bytes()), "ADR:;;;Chur;;;") {
		t.Errorf("out:\n%s", n.Bytes())
	}
}
