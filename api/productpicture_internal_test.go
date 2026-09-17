package api

import (
	"strings"
	"testing"
)

// A product is shown, not only named
// (ADR-0391).
//
// A catalogue row is a name and a price, and somebody choosing between two phones
// is choosing between two names. The picture is what a shop has that a list does
// not — and the three ways it quietly stops being one are held here.

// TestThePictureIsReadByTheAudienceAndWrittenByTheMaintainer.
//
// The two halves are different questions and they must not collapse into one role.
// Set to the maintainer's role, the read is a picture only its author can see;
// opened to the audience, the write lets a customer re-illustrate a catalogue.
func TestThePictureIsReadByTheAudienceAndWrittenByTheMaintainer(t *testing.T) {
	const path = "/api/v1/catalog-products/{id}/picture"
	want := map[string]string{
		"GET":    RoleUser,
		"PUT":    RoleProductManager,
		"DELETE": RoleProductManager,
	}
	seen := map[string]bool{}
	for _, r := range accessTestServer(t).apiRoutes() {
		if r.pattern != path {
			continue
		}
		seen[r.method] = true
		if got := r.op.role; got != want[r.method] {
			t.Errorf("%s %s needs %q, want %q — reading a picture is for whoever the "+
				"catalogue is for, and changing one is for whoever maintains it",
				r.method, path, got, want[r.method])
		}
	}
	for method := range want {
		if !seen[method] {
			t.Errorf("%s %s is not registered at all", method, path)
		}
	}
}

// TestAProductWithNoPictureLeavesNoHole.
//
// Most products have none, and that is not a failure — it is the ordinary case the
// page falls back from. An <img> pointing at a 404 renders a broken-image icon,
// which is the page telling somebody that something went wrong with a product where
// nothing did.
func TestAProductWithNoPictureLeavesNoHole(t *testing.T) {
	body := webRegion(t, readWeb(t, "portal.js"), "function productPicture(", "\n}")
	if !strings.Contains(body, "onerror") || !strings.Contains(body, "remove()") {
		t.Error("a product with no picture leaves a broken image on the page, which " +
			"reads as a fault where the ordinary answer is simply 'none'")
	}
	if !strings.Contains(body, "/picture") {
		t.Error("the picture is read from somewhere other than the product's own " +
			"picture route")
	}
	// And the panel actually shows it. A renderer nothing calls is a feature that
	// passes every test and appears nowhere.
	if !strings.Contains(webRegion(t, readWeb(t, "portal.js"),
		"function infoPanel(", "\n}"), "productPicture(") {
		t.Error("the product panel never renders the picture, so a catalogue with " +
			"pictures shows none of them")
	}
}

// TestThePictureFollowsTheProductItBelongsTo.
//
// The upload is a second request, and the order of the two is the whole of what
// makes it safe: a product refused by the server — a bad revision, a home somebody
// else maintains — must not leave a picture filed under its id.
func TestTheEditorUploadsOnlyAfterTheProductIsSaved(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	saved := strings.Index(src, `await api("POST", "/api/v1/catalog-products", body)`)
	if saved < 0 {
		t.Fatal("the product editor no longer saves through the products route; this " +
			"guard has lost its subject")
	}
	uploaded := strings.Index(src, "await savePicture(")
	if uploaded < 0 {
		t.Fatal("the product editor never stores the picture somebody chose")
	}
	if uploaded < saved {
		t.Error("the picture is uploaded before the product is saved, so a product the " +
			"server refuses still leaves a picture filed under its id")
	}
	// The bytes go up as bytes, under their own type. Sent as JSON they arrive as a
	// string the server rejects, and the person is told their picture is not a
	// picture.
	if !strings.Contains(webRegion(t, src, "async function savePicture(", "\n}"), "apiBytes(") {
		t.Error("the picture is sent through the JSON helper rather than as bytes")
	}
}
