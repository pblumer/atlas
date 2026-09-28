package api_test

import (
	"net/http"
	"testing"
)

// The portal is called the shop, at /shop.html and under /api/v1/shop/.
//
// The page's old address is the one thing a rename cannot reach: it sits in
// bookmarks, in mails already sent and on intranet pages. So it is answered with a
// redirect rather than a 404 that reads as the service having been switched off.
// The API's old routes are not kept — they were read by this page alone, and a
// second name for every route would be two surfaces to hold in step for ever.

// noFollow is a client that reports a redirect instead of following it, so the
// test reads what the server said rather than where the browser ended up.
func noFollow() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// TestTheOldShopAddressLeadsToTheNewOne.
//
// Signed out, because that is the bookmark's ordinary case, and with the query
// kept, because it is where a returning sign-in says how it went.
func TestTheOldShopAddressLeadsToTheNewOne(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	res, err := noFollow().Get(ts.URL + "/portal.html?sso=failed")
	if err != nil {
		t.Fatalf("GET /portal.html: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("GET /portal.html = %d, want 301", res.StatusCode)
	}
	if got := res.Header.Get("Location"); got != "/shop.html?sso=failed" {
		t.Errorf("Location = %q, want /shop.html?sso=failed", got)
	}

	res, err = http.Get(ts.URL + "/shop.html")
	if err != nil {
		t.Fatalf("GET /shop.html: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("GET /shop.html = %d, want 200: the page the redirect leads to is not served",
			res.StatusCode)
	}
}

// TestTheShopReadsItsCatalogueUnderItsOwnName: the route the page reads answers
// under /api/v1/shop/, and the old one is gone rather than silently kept.
func TestTheShopReadsItsCatalogueUnderItsOwnName(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	c := newClient(t)
	if login(t, c, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("login failed")
	}
	if code, body := cReq(t, c, ts, "GET", "/api/v1/shop/favourites", ""); code != http.StatusOK {
		t.Errorf("GET /api/v1/shop/favourites = %d (%s), want 200", code, body)
	}
	if code, _ := cReq(t, c, ts, "GET", "/api/v1/portal/favourites", ""); code == http.StatusOK {
		t.Error("GET /api/v1/portal/favourites still answers; the old name was meant to go")
	}
}
