package catalog

import (
	"net/http"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// A product's picture, and the two questions it answers separately: who may see
// one, and who may put one there.
//
// The gate inventory proves an outsider is refused all three. These hold the two
// cases the inventory cannot express, because both callers are people the
// catalogue knows.

// onePNG is a valid PNG header and nothing else. The server validates the format
// and never decodes the image, so a header is a picture for every purpose here.
const onePNG = "\x89PNG\r\n\x1a\n" + "bytes"

// catalogueWithAPicturedProduct sets up a catalogue whose audience is grp_inside,
// carrying one product with a picture on it.
func catalogueWithAPicturedProduct(t *testing.T) (*Service, Catalog) {
	t.Helper()
	s := serviceWithAdmin(t)
	owner := user("usr_owner")
	cat := makeCatalog(t, s, owner)
	as(t, s.HandleUpdateCatalog, owner, "PATCH", `{"groups":["grp_inside"]}`, "id", cat.ID)
	makeItem(t, s, owner, cat.ID)
	return s, cat
}

// TestTheAudienceSeesThePicture.
//
// The portal's reader is not the product's maintainer and never will be: they
// belong to a group the catalogue is offered to. A picture only its maintainer can
// see is a picture nobody sees.
func TestTheAudienceSeesThePicture(t *testing.T) {
	s, _ := catalogueWithAPicturedProduct(t)
	reader := &httpapi.Principal{UserID: "usr_customer", Roles: []string{"user"},
		GroupIDs: []string{"grp_inside"}}

	rec := as(t, s.HandleGetPicture, reader, "GET", "", "id", "prd_gate")
	if rec.Code != http.StatusOK {
		t.Fatalf("a customer of the catalogue reading the picture = %d (%s), want 200",
			rec.Code, rec.Body)
	}
	if rec.Body.String() != onePNG {
		t.Errorf("the bytes served are not the bytes stored: %q", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("served as %q, want image/png", ct)
	}
}

// TestAReaderIsNotAnIllustrator.
//
// The half the outsider case cannot prove: somebody the catalogue *is* for reaches
// the read gate and must still be refused the write. A picture is part of how a
// product is offered, and somebody who may not rename it may not re-illustrate it.
func TestAReaderIsNotAnIllustrator(t *testing.T) {
	s, _ := catalogueWithAPicturedProduct(t)
	reader := &httpapi.Principal{UserID: "usr_customer", Roles: []string{"productmanager"},
		GroupIDs: []string{"grp_inside"}}

	for _, tc := range []struct{ name, method string }{
		{"putting one there", "PUT"},
		{"taking it away", "DELETE"},
	} {
		rec := asTyped(t, s.HandleSetPicture, reader, tc.method, "image/png", onePNG, "id", "prd_gate")
		if tc.method == "DELETE" {
			rec = as(t, s.HandleDeletePicture, reader, "DELETE", "", "id", "prd_gate")
		}
		if rec.Code != http.StatusForbidden {
			t.Errorf("a customer %s = %d (%s), want 403 — they may see the catalogue, "+
				"which is not the same as maintaining it", tc.name, rec.Code, rec.Body)
		}
	}
}

// TestAPictureIsReplacedAndNotAccumulated.
//
// Two formats for one product is one file too many: whichever the reader walks
// first is the picture, which makes the answer depend on the order of a list.
func TestAPictureIsReplacedAndNotAccumulated(t *testing.T) {
	s, _ := catalogueWithAPicturedProduct(t)
	owner := user("usr_owner")

	const oneJPEG = "\xff\xd8\xffbytes"
	if rec := asTyped(t, s.HandleSetPicture, owner, "PUT", "image/jpeg", oneJPEG,
		"id", "prd_gate"); rec.Code != http.StatusNoContent {
		t.Fatalf("replace the picture = %d (%s)", rec.Code, rec.Body)
	}
	rec := as(t, s.HandleGetPicture, owner, "GET", "", "id", "prd_gate")
	if got := rec.Header().Get("Content-Type"); got != "image/jpeg" {
		t.Errorf("after replacing a PNG with a JPEG the product serves %q — the old "+
			"file is still there and is found first", got)
	}

	// And removing it removes it, whatever format it ended up in.
	if rec := as(t, s.HandleDeletePicture, owner, "DELETE", "", "id", "prd_gate"); rec.Code != http.StatusNoContent {
		t.Fatalf("remove the picture = %d (%s)", rec.Code, rec.Body)
	}
	if rec := as(t, s.HandleGetPicture, owner, "GET", "", "id", "prd_gate"); rec.Code != http.StatusNotFound {
		t.Errorf("after removal the product still serves a picture: %d (%s)", rec.Code, rec.Body)
	}
}

// TestWhatIsNotAPicture.
//
// The body is validated as the type it claims, because these bytes are served back
// to a browser: a file that says PNG and is something else is a file the browser
// decides about on its own.
func TestWhatIsNotAPicture(t *testing.T) {
	s, _ := catalogueWithAPicturedProduct(t)
	owner := user("usr_owner")

	for _, tc := range []struct {
		name, contentType, body string
		want                    int
	}{
		{"a type nothing may be uploaded as", "application/pdf", "%PDF-1.7", http.StatusUnsupportedMediaType},
		{"bytes that are not what they claim", "image/png", "not a png at all", http.StatusBadRequest},
		{"nothing at all", "image/png", "", http.StatusBadRequest},
	} {
		rec := asTyped(t, s.HandleSetPicture, owner, "PUT", tc.contentType, tc.body, "id", "prd_gate")
		if rec.Code != tc.want {
			t.Errorf("%s = %d (%s), want %d", tc.name, rec.Code, rec.Body, tc.want)
		}
	}
}

// TestAProductCarriedByASecondCatalogueShowsItsPictureThere.
//
// A product is referenced by catalogues rather than owned by one: one home decides
// who may change it, and any number of catalogues may offer it. So the question the
// read gate asks is not "may you read its home" — a customer of the second
// catalogue never can — but "does a catalogue you may read offer it". Gated on the
// home alone, that customer sees a name and no picture: the one row on the page
// that looks broken.
func TestAProductCarriedByASecondCatalogueShowsItsPictureThere(t *testing.T) {
	s, _ := catalogueWithAPicturedProduct(t)

	// A second catalogue, for a different audience, carrying the same product.
	elsewhere := user("usr_other")
	second := makeCatalog(t, s, elsewhere)
	as(t, s.HandleUpdateCatalog, elsewhere, "PATCH",
		`{"groups":["grp_second"],"items":["prd_gate"]}`, "id", second.ID)

	reader := &httpapi.Principal{UserID: "usr_elsewhere", Roles: []string{"user"},
		GroupIDs: []string{"grp_second"}}
	rec := as(t, s.HandleGetPicture, reader, "GET", "", "id", "prd_gate")
	if rec.Code != http.StatusOK {
		t.Fatalf("a customer of the catalogue that carries the product = %d (%s), "+
			"want 200 — they are ordering it, and the picture is how it is shown",
			rec.Code, rec.Body)
	}
}
