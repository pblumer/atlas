package archsketch

import (
	"bytes"
	"strings"
	"testing"
)

// ADR-0493, as amended: the sketch a page outside Atlas shows to everybody withholds
// everything a read withholds from some reader of the release, and the people.

// minimalSample is every layer with something in it a reader may be refused: a
// Worker's endpoint and configuration, its instances, a product, other applications
// called and coupled, a feed endpoint, and the owners of a capability and a value
// stream.
func minimalSample() Snapshot {
	s := couplingSample()
	l := layeredSample()
	s.Business, s.Strategy = l.Business, l.Strategy
	s.Normalize()
	return s
}

func TestTheMinimalSketchWithholdsWhatSomeReaderMayNotSee(t *testing.T) {
	s := minimalSample()
	all := SelectionOf(LayerStrategy, LayerBusiness, LayerApplication, LayerData, LayerTechnology)
	before := string(SVG(s.Select(all), meta()))
	m := s.Minimal("app-1")

	svg, doc := string(SVG(m.Select(all), meta())), string(ArchiMate(m.Select(all), meta()))
	for _, leak := range []string{
		"Erika Muster", "Leiterin Finanzen", "role Vertrieb", // the owners
		"Mahnwesen", "mahnlauf", "kalender", "Shop", // other applications, called and coupled
		"lake.example.com", "siem.example.com", "datalake", // the feed's endpoints
		"Abonnement", // a product
	} {
		if strings.Contains(svg, leak) || strings.Contains(doc, leak) {
			t.Errorf("the minimal sketch shows %q", leak)
		}
	}
	for _, w := range m.Physical.Workers {
		if w.Endpoint != nil && !w.Endpoint.Withheld {
			t.Errorf("worker %s keeps its endpoint %+v", w.Name, w.Endpoint)
		}
	}
	for _, want := range []string{"owner withheld from this reader", "another application", "endpoint withheld from this reader"} {
		if !strings.Contains(svg, want) {
			t.Errorf("the minimal sketch does not say %q", want)
		}
	}
	if !strings.Contains(doc, "withheld from this reader") {
		t.Error("the minimal document does not say the owner is withheld")
	}
	assertWellFormed(t, []byte(svg))

	// The release's own application keeps its name, and so does a call into it.
	own := s
	own.Dependencies = append([]Dependency(nil), s.Dependencies...)
	for i, d := range own.Dependencies {
		if d.Kind == DependencyCall {
			own.Dependencies[i].TargetApplicationID, own.Dependencies[i].TargetApplication = "app-1", "Rechnungswesen"
		}
	}
	for _, d := range own.Minimal("app-1").Dependencies {
		if d.Kind == DependencyCall && (d.TargetWithheld || d.TargetApplication != "Rechnungswesen") {
			t.Errorf("a call into the release's own application = %+v, want it named", d)
		}
	}

	// Nothing stored is changed, and the same snapshot gives the same picture.
	if after := string(SVG(s.Select(all), meta())); after != before {
		t.Error("the stored snapshot was changed by the minimal read")
	}
	if !bytes.Equal(SVG(s.Minimal("app-1"), meta()), SVG(m, meta())) {
		t.Error("the minimal sketch is not the same twice")
	}
	for _, c := range s.Strategy.Capabilities {
		if c.Owner != nil && c.Owner.Withheld {
			t.Error("the stored owner was marked withheld")
		}
	}
}
