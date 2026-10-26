package archsketch

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/panorama"
)

func TestASelectionIsReadFromWhatALinkSpells(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want Selection
	}{
		{"", DefaultSelection},
		{" , ", DefaultSelection},
		{"application", SelectionOf(LayerApplication)},
		{"Technology, application", DefaultSelection},
		{"data,strategy,data", SelectionOf(LayerStrategy, LayerData)},
		{"strategy,business,application,data,technology", SelectionOf(layerOrder...)},
	} {
		got, err := ParseSelection(tc.raw)
		if err != nil || got != tc.want {
			t.Errorf("ParseSelection(%q) = %v, %v; want %v", tc.raw, got, err, tc.want)
		}
	}
	_, err := ParseSelection("application,netzwerk")
	if err == nil || !strings.Contains(err.Error(), `"netzwerk"`) ||
		!strings.Contains(err.Error(), "strategy,business,application,data,technology") {
		t.Errorf("an unknown layer gives %v, want an error naming it and the layers there are", err)
	}
	if got := SelectionOf(LayerTechnology, LayerStrategy).String(); got != "strategy,technology" {
		t.Errorf("String() = %q, want the drawing order", got)
	}
	if SelectionOf("netzwerk") != 0 {
		t.Error("an unknown name selects something")
	}
}

func TestASelectionNarrowsAReadAndLeavesTheSnapshotAlone(t *testing.T) {
	s := physicalSample()

	logical := s.Select(SelectionOf(LayerApplication))
	if logical.Physical != nil || len(logical.EntryPoints) == 0 || len(logical.Dependencies) == 0 {
		t.Errorf("the application layer alone = physical %v, %d ways in, %d dependencies; want the logical part only",
			logical.Physical, len(logical.EntryPoints), len(logical.Dependencies))
	}
	if logical.Recorded() != DefaultSelection {
		t.Errorf("Recorded() after a selection = %v, want what the stored snapshot holds", logical.Recorded())
	}

	installation := s.Select(SelectionOf(LayerTechnology))
	if installation.EntryPoints != nil || installation.Dependencies != nil || len(installation.Processes) == 0 {
		t.Errorf("the technology layer alone keeps %d ways in, %d dependencies, %d processes; want only the processes",
			len(installation.EntryPoints), len(installation.Dependencies), len(installation.Processes))
	}
	if p := installation.Physical; p == nil || p.PublicStartLinks != nil || len(p.Workers) != 3 {
		t.Errorf("the technology layer alone has physical part %+v, want the workers without the public start links", p)
	}
	if len(s.Physical.PublicStartLinks) != 2 || s.Physical == installation.Physical {
		t.Error("selecting a layer changed the snapshot it was called on")
	}
	if !installation.technologyOnly() || logical.technologyOnly() || sample().Select(SelectionOf(LayerTechnology)).technologyOnly() {
		t.Error("only a snapshot with a physical part, narrowed to the technology layer, is drawn as the installation alone")
	}
	if sample().Recorded() != SelectionOf(LayerApplication) {
		t.Errorf("a snapshot without a physical part records %v, want the application layer only", sample().Recorded())
	}
}

func TestTheDefaultSelectionDrawsWhatWasDrawnBefore(t *testing.T) {
	for name, s := range map[string]Snapshot{"logical": sample(), "physical": physicalSample()} {
		chosen := s.Select(DefaultSelection)
		if !bytes.Equal(SVG(chosen, meta()), SVG(s, meta())) || !bytes.Equal(ArchiMate(chosen, meta()), ArchiMate(s, meta())) {
			t.Errorf("%s: the default selection renders differently from no selection", name)
		}
		if strings.Contains(string(SVG(s, meta())), "Layers shown") {
			t.Errorf("%s: a picture nobody narrowed talks about layers", name)
		}
	}
}

func TestTheApplicationLayerAloneIsTheLogicalView(t *testing.T) {
	s := physicalSample().Select(SelectionOf(LayerApplication))
	svg := string(SVG(s, meta()))
	for _, want := range []string{
		"Logical view · ",
		" · layers: application",
		"Layers shown: application. Switched off for this picture: technology.",
		"who connects to whom is in the technology layer, switched off for this picture",
		"Message zahlung-eingegangen",
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("the application layer alone lacks %q", want)
		}
	}
	for _, leak := range []string{"erp.example.com", "relay.example.com", "Public start form", "worker-a", "not recorded in this snapshot"} {
		if strings.Contains(svg, leak) {
			t.Errorf("the application layer alone draws %q, which only the installation records", leak)
		}
	}
	if doc := string(ArchiMate(s, meta())); !strings.Contains(doc, "Switched off for this picture: technology.") ||
		strings.Contains(doc, "erp.example.com") {
		t.Error("the ArchiMate document of the application layer alone does not say what it left out, or carries an endpoint")
	}
}

func TestTheTechnologyLayerAloneIsTheInstallation(t *testing.T) {
	s := physicalSample().Select(SelectionOf(LayerTechnology))
	svg := string(SVG(s, meta()))
	for _, want := range []string{
		"Physical view of prod-1 (PROD)",
		" · layers: technology",
		"Worker processes and the systems reached",
		"System software",
		"→ https://erp.example.com:8443",
		"→ relay.example.com:587",
		"opened by Atlas itself",
		"Job type archivieren",
		"at https://atlas.example.com",
		"Switched off for this picture: application.",
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("the technology layer alone lacks %q", want)
		}
	}
	for _, leak := range []string{"Message zahlung-eingegangen", "Public start form", "Application component"} {
		if strings.Contains(svg, leak) {
			t.Errorf("the technology layer alone draws %q, which belongs to the application layer", leak)
		}
	}

	doc := ArchiMate(s, meta())
	if res := panorama.Validate(doc); len(res.Problems) != 0 {
		t.Fatalf("panorama.Validate reports %+v", res.Problems)
	}
	types, rels := readExchange(t, doc)
	for id, typ := range types {
		if strings.HasPrefix(typ, "Application") {
			t.Errorf("element %s is a %s in the installation alone", id, typ)
		}
	}
	paths := 0
	for _, r := range rels {
		ok, refusal := panorama.MayConnect(r.typ, types[r.source], types[r.target])
		if ok {
			continue
		}
		if types[r.source] == "Path" && refusal.Reason == panorama.RefusedOutOfSubset {
			paths++
			continue
		}
		t.Errorf("%s from %s (%s) to %s (%s) is refused: %s",
			r.typ, r.source, types[r.source], r.target, types[r.target], refusal.Message)
	}
	// The leases of the two worker processes and the two job types, and the
	// connections to the two target systems whose endpoints are known.
	if paths != 12 {
		t.Errorf("%d path associations, want 12", paths)
	}
	if !strings.Contains(string(doc), "physical architecture on prod-1 (PROD)") {
		t.Error("the document does not say it is the installation alone")
	}
}

func TestAWithheldEndpointStaysOutOfTheInstallationAlone(t *testing.T) {
	s := physicalSample()
	red := s.Physical.Redacted(func(string) bool { return false }, false)
	s.Physical = &red
	s = s.Select(SelectionOf(LayerTechnology))
	for name, out := range map[string]string{"svg": string(SVG(s, meta())), "xml": string(ArchiMate(s, meta()))} {
		if strings.Contains(out, "erp.example.com") || strings.Contains(out, "worker-a") {
			t.Errorf("%s of the installation alone carries a withheld endpoint or instance", name)
		}
		if !strings.Contains(out, "withheld") {
			t.Errorf("%s of the installation alone does not say what it withheld", name)
		}
	}
}

func TestALayerAskedForButNotRecordedIsNamed(t *testing.T) {
	s := physicalSample().Select(SelectionOf(LayerApplication, LayerData, LayerTechnology))
	note := "Layers shown: application,technology. Asked for, but not recorded in this snapshot: data."
	if got := s.layerNote(); got != note {
		t.Errorf("layerNote = %q, want %q", got, note)
	}
	if !strings.Contains(string(SVG(s, meta())), note) {
		t.Error("the picture does not say which layer it was asked for and does not have")
	}
}
