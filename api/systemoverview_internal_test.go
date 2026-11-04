package api

import (
	"bytes"
	"io/fs"
	"os"
	"regexp"
	"testing"
)

// The system overview is drawn once, in docs/architecture/system-overview.svg, where
// the architecture document shows it. The Console's welcome card, its help menu and the
// handbook show it too, and they can only serve what `//go:embed web` carries — so
// api/web holds a copy. A copy drifts: the diagram gains a layer in docs/ and every
// person opening the Console goes on reading the old one, with nothing to say so.
//
// This holds the copy to the original, byte for byte.
func TestTheConsoleShowsTheCurrentSystemOverview(t *testing.T) {
	const original = "../docs/architecture/system-overview.svg"
	want, err := os.ReadFile(original)
	if err != nil {
		t.Fatalf("read %s: %v", original, err)
	}
	got, err := webFS.ReadFile("web/system-overview.svg")
	if err != nil {
		t.Fatalf("the Console serves no system overview: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("api/web/system-overview.svg is not %s, so the Console and the handbook "+
			"show an outdated diagram. The docs file is the one that is edited; copy it over:\n"+
			"\tcp docs/architecture/system-overview.svg api/web/system-overview.svg", original)
	}
}

// The overview opens with the atlas mark. The mark is drawn once, in
// docs/brand/atlas-mark.svg, and a redrawn mark left the overview showing the old
// one — a triangle with a cross — in the Console, the handbook and the architecture
// document alike. This holds the overview to every shape of the shipped mark.
func TestTheSystemOverviewCarriesTheCurrentAtlasMark(t *testing.T) {
	const mark = "../docs/brand/atlas-mark.svg"
	src, err := os.ReadFile(mark)
	if err != nil {
		t.Fatalf("read %s: %v", mark, err)
	}
	shapes := regexp.MustCompile(`<path fill="#fff" d="([^"]+)"/>`).FindAllSubmatch(src, -1)
	if len(shapes) != 4 {
		t.Fatalf("%s has %d white shapes, want the four of the mark (lower band, upper band, stem, cross)", mark, len(shapes))
	}
	overview, err := os.ReadFile("../docs/architecture/system-overview.svg")
	if err != nil {
		t.Fatalf("read the system overview: %v", err)
	}
	for _, s := range shapes {
		if !bytes.Contains(overview, []byte(`d="`+string(s[1])+`"`)) {
			t.Errorf("the system overview lacks the mark's shape %q from %s, so it shows an outdated logo", s[1], mark)
		}
	}
}

// Every page the Console serves names the atlas favicon, so a browser tab shows the
// mark rather than a blank page icon or a former logo. The pages are buildless and
// carry it inline as a data URI (docs/brand/README.md); this holds each to the four
// shapes of docs/brand/favicon.svg, the heavier cut drawn for 16px.
func TestEveryConsolePageCarriesTheAtlasFavicon(t *testing.T) {
	const favicon = "../docs/brand/favicon.svg"
	src, err := os.ReadFile(favicon)
	if err != nil {
		t.Fatalf("read %s: %v", favicon, err)
	}
	shapes := regexp.MustCompile(`<path fill="#fff" d="([^"]+)"/>`).FindAllSubmatch(src, -1)
	if len(shapes) != 4 {
		t.Fatalf("%s has %d white shapes, want the four of the mark", favicon, len(shapes))
	}
	pages, err := fs.Glob(webFS, "web/*.html")
	if err != nil || len(pages) == 0 {
		t.Fatalf("list the Console pages: %v (found %d)", err, len(pages))
	}
	icon := regexp.MustCompile(`<link rel="icon" href="data:image/svg\+xml,[^"]+"`)
	for _, page := range pages {
		body, err := webFS.ReadFile(page)
		if err != nil {
			t.Fatalf("read %s: %v", page, err)
		}
		link := icon.Find(body)
		if link == nil {
			t.Errorf("%s names no inline favicon, so its tab shows no atlas mark", page)
			continue
		}
		for _, s := range shapes {
			if !bytes.Contains(link, s[1]) {
				t.Errorf("%s: its favicon lacks the shape %q from %s, so it shows an outdated mark", page, s[1], favicon)
			}
		}
	}
}
