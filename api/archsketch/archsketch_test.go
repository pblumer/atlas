package archsketch

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/panorama"
)

// sample is a release with one of everything a snapshot records, in an order that is
// not the one Normalize settles on.
func sample() Snapshot {
	return Snapshot{
		FormatVersion: FormatVersion, MappingVersion: MappingVersion, TakenAt: 1_792_000_000,
		Processes: []Process{
			{ProcessID: "rechnung", Name: "Rechnung", Version: 3},
			{ProcessID: "eingang", Name: "Eingang", Version: 1},
		},
		EntryPoints: []EntryPoint{
			{Kind: EntryMessage, Name: "zahlung-eingegangen", UsedBy: []Use{{ProcessID: "rechnung", ElementID: "Catch_Zahlung"}}},
			{Kind: EntryMessage, Name: "auftrag", Starts: true, UsedBy: []Use{{ProcessID: "eingang", ElementID: "Start"}}},
			// The same name from a second element merges into one entry point.
			{Kind: EntryMessage, Name: "zahlung-eingegangen", UsedBy: []Use{{ProcessID: "eingang", ElementID: "Boundary_Zahlung"}}},
		},
		Dependencies: []Dependency{
			{Kind: DependencyDecision, Name: "rabatt", UsedBy: []Use{{ProcessID: "rechnung", ElementID: "Rule"}}},
			{Kind: DependencyCall, Name: "mahnung", TargetApplicationID: "app-mahnwesen", TargetApplication: "Mahnwesen",
				UsedBy: []Use{{ProcessID: "rechnung", ElementID: "Call_Mahnung"}}},
			{Kind: DependencyWorker, Name: "smtp-main", WorkerType: "mail", UsedBy: []Use{{ProcessID: "rechnung", ElementID: "Send"}}},
			{Kind: DependencyWorker, Name: "erp", WorkerType: "rest", UsedBy: []Use{
				{ProcessID: "rechnung", ElementID: "Book"}, {ProcessID: "rechnung", ElementID: "Book"},
			}},
			{Kind: DependencyRuntimeWorker, Name: "=tenant.worker", WorkerType: "entra", UsedBy: []Use{{ProcessID: "eingang", ElementID: "Provision"}}},
		},
	}
}

func meta() Meta {
	return Meta{ApplicationID: "app-rechnung", Application: "Rechnungswesen", ReleaseID: "rel-7", Version: 7}
}

func TestNormalizeMergesAndOrders(t *testing.T) {
	s := sample()
	s.Normalize()

	if got := []string{s.Processes[0].ProcessID, s.Processes[1].ProcessID}; got[0] != "eingang" || got[1] != "rechnung" {
		t.Errorf("processes = %v, want sorted by id", got)
	}
	if len(s.EntryPoints) != 2 {
		t.Fatalf("entry points = %+v, want the two names merged to two entries", s.EntryPoints)
	}
	zahlung := s.EntryPoints[1]
	if zahlung.Name != "zahlung-eingegangen" || len(zahlung.UsedBy) != 2 {
		t.Errorf("merged entry point = %+v, want both receipts under one name", zahlung)
	}
	kinds := []string{}
	for _, d := range s.Dependencies {
		kinds = append(kinds, d.Kind+":"+d.Name)
	}
	want := "worker:erp,worker:smtp-main,runtimeWorker:=tenant.worker,call:mahnung,decision:rabatt"
	if strings.Join(kinds, ",") != want {
		t.Errorf("dependencies = %v, want %s (leaving the server first, local decisions last)", kinds, want)
	}
	if erp := s.Dependencies[0]; len(erp.UsedBy) != 1 {
		t.Errorf("erp uses = %+v, want the duplicate element collapsed", erp.UsedBy)
	}
	if got := len(s.Outbound()); got != 4 {
		t.Errorf("Outbound() = %d dependencies, want 4 — a local decision does not cross the boundary", got)
	}
}

// TestRenderingsAreDeterministic: the same release must produce the same bytes
// whatever order its members were read in, or a committed rendering would change on
// every regeneration and a diff would stop meaning anything.
func TestRenderingsAreDeterministic(t *testing.T) {
	a := sample()
	a.Normalize()
	b := sample()
	b.EntryPoints[0], b.EntryPoints[2] = b.EntryPoints[2], b.EntryPoints[0]
	b.Dependencies[0], b.Dependencies[3] = b.Dependencies[3], b.Dependencies[0]
	b.Normalize()

	if !bytes.Equal(SVG(a, meta()), SVG(b, meta())) {
		t.Error("SVG differs for the same release read in another order")
	}
	if !bytes.Equal(ArchiMate(a, meta()), ArchiMate(b, meta())) {
		t.Error("ArchiMate document differs for the same release read in another order")
	}
}

func TestSVGIsWellFormedAndSaysWhatItIs(t *testing.T) {
	s := sample()
	s.Normalize()
	out := SVG(s, meta())

	assertWellFormed(t, out)
	text := string(out)
	for _, want := range []string{
		"Architecture sketch — Rechnungswesen v7",
		"snapshot taken 2026-10-14 17:46 UTC",
		fmt.Sprintf("mapping version %d", MappingVersion),
		"nothing here was drawn by a person",
		"Not shown:",
		"Message auftrag", "starts an instance",
		"smtp-main", "mail worker",
		"Decided at run time",
		"in Mahnwesen",
		"1 local decision",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("SVG lacks %q", want)
		}
	}
	if strings.Contains(text, "rabatt") {
		t.Error("a local decision is drawn as a dependency beside the application; it runs inside it")
	}
	for _, banned := range []string{"<script", "href=", "xlink:", "<image", "<foreignObject"} {
		if strings.Contains(text, banned) {
			t.Errorf("SVG contains %q — the file is embedded in pages Atlas does not control", banned)
		}
	}
}

// TestHostileNamesAreEscaped: every label is model content, and a model is somebody's
// input. A name that is markup must arrive as text in both documents, and neither may
// stop being well-formed because of it.
func TestHostileNamesAreEscaped(t *testing.T) {
	s := Snapshot{
		FormatVersion: FormatVersion, MappingVersion: MappingVersion,
		Processes: []Process{{ProcessID: "p", Version: 1}},
		EntryPoints: []EntryPoint{{Kind: EntryMessage, Name: `<script>alert("x")</script>&`,
			UsedBy: []Use{{ProcessID: "p", ElementID: "e"}}}},
		Dependencies: []Dependency{{Kind: DependencyWorker, Name: "a\x00b'\"", WorkerType: "rest",
			UsedBy: []Use{{ProcessID: "p"}}}},
	}
	m := Meta{Application: "<b>App</b>", Version: 1}

	for name, out := range map[string][]byte{"svg": SVG(s, m), "archimate": ArchiMate(s, m)} {
		assertWellFormed(t, out)
		if bytes.Contains(out, []byte("<script>")) || bytes.Contains(out, []byte("<b>App")) {
			t.Errorf("%s: markup from a name reached the document unescaped", name)
		}
		if !bytes.Contains(out, []byte("&lt;script&gt;")) {
			t.Errorf("%s: the hostile name is missing rather than escaped", name)
		}
	}
}

// TestArchiMateIsValidForPanorama: a document exported here is meant to be imported
// into Panorama and other tools. It must pass Panorama's own validator, and every
// relationship in it must be one Panorama's authoring rules accept.
func TestArchiMateIsValidForPanorama(t *testing.T) {
	s := sample()
	s.Normalize()
	out := ArchiMate(s, meta())

	if res := panorama.Validate(out); len(res.Problems) != 0 {
		t.Fatalf("panorama.Validate reports %+v", res.Problems)
	}

	types, rels := readExchange(t, out)
	if len(rels) == 0 {
		t.Fatal("no relationships written")
	}
	for _, r := range rels {
		ok, refusal := panorama.MayConnect(r.typ, types[r.source], types[r.target])
		if !ok {
			t.Errorf("%s from %s (%s) to %s (%s) is refused: %s",
				r.typ, r.source, types[r.source], r.target, types[r.target], refusal.Message)
		}
	}

	text := string(out)
	for _, want := range []string{
		"DERIVED, NOT AUTHORED",
		fmt.Sprintf("mapping version %d", MappingVersion),
		`<value xml:lang="en">rel-7</value>`,
		`<value xml:lang="en">app-mahnwesen</value>`,
		"Local decisions evaluated inside the engine (1) are not written",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("ArchiMate document lacks %q", want)
		}
	}
	if strings.Contains(text, ">rabatt<") {
		t.Error("a local decision is written as an element")
	}
}

func TestAnUnreadProcessIsReportedAsAGap(t *testing.T) {
	s := Snapshot{FormatVersion: FormatVersion, MappingVersion: MappingVersion,
		Processes: []Process{{ProcessID: "p", Version: 1, Unread: true}}}
	m := meta()
	if !strings.Contains(string(SVG(s, m)), "Incomplete: 1 process could not be read") {
		t.Error("SVG does not say a member could not be read")
	}
	if !strings.Contains(string(ArchiMate(s, m)), "Incomplete: 1 process could not be read") {
		t.Error("ArchiMate document does not say a member could not be read")
	}
	if !strings.Contains(string(SVG(s, m)), "No outbound dependencies.") {
		t.Error("an empty right column is not stated")
	}
}

func assertWellFormed(t *testing.T, doc []byte) {
	t.Helper()
	d := xml.NewDecoder(bytes.NewReader(doc))
	d.Strict = true
	for {
		if _, err := d.Token(); err == io.EOF {
			return
		} else if err != nil {
			t.Fatalf("not well-formed XML: %v\n%s", err, doc)
		}
	}
}

type rel struct{ typ, source, target string }

// readExchange returns each element's type by identifier, and the relationships.
func readExchange(t *testing.T, doc []byte) (map[string]string, []rel) {
	t.Helper()
	var model struct {
		Elements []struct {
			ID   string `xml:"identifier,attr"`
			Type string `xml:"http://www.w3.org/2001/XMLSchema-instance type,attr"`
		} `xml:"elements>element"`
		Relationships []struct {
			Type   string `xml:"http://www.w3.org/2001/XMLSchema-instance type,attr"`
			Source string `xml:"source,attr"`
			Target string `xml:"target,attr"`
		} `xml:"relationships>relationship"`
	}
	if err := xml.Unmarshal(doc, &model); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	types := map[string]string{}
	for _, e := range model.Elements {
		types[e.ID] = e.Type
	}
	var rels []rel
	for _, r := range model.Relationships {
		rels = append(rels, rel{r.Type, r.Source, r.Target})
	}
	return types, rels
}

// TestTheEdgesAreSaidRatherThanDropped: the cases a real model reaches less often — a
// name too long for its box, a worker reference with no type, a call with no target, a
// worker used from many elements, a kind this build does not draw — each end up in the
// picture as something a reader can act on, never as a silent gap.
func TestTheEdgesAreSaidRatherThanDropped(t *testing.T) {
	many := make([]Use, 0, 10)
	for i := range 10 {
		many = append(many, Use{ProcessID: "p", ElementID: string(rune('a' + i))})
	}
	s := Snapshot{
		FormatVersion: FormatVersion, MappingVersion: MappingVersion,
		Processes: []Process{{ProcessID: "p", Version: 1}},
		Dependencies: []Dependency{
			{Kind: DependencyWorker, Name: "ein-ausgesprochen-langer-worker-name-der-nicht-passt", UsedBy: many},
			{Kind: DependencyWorker, Name: "ein-ausgesprochen-langer-worker-name-der-nicht-passt", UsedBy: []Use{{ProcessID: "q"}}},
			{Kind: DependencyCall, Name: "nirgends", UsedBy: []Use{{ProcessID: "p", ElementID: "Call"}}},
			{Kind: "telepathy", Name: "kommt-spaeter", UsedBy: nil},
		},
	}
	s.Normalize()
	if len(s.Dependencies) != 3 || len(s.Dependencies[0].UsedBy) != 11 {
		t.Fatalf("dependencies = %+v, want the two references to one worker merged, 11 uses", s.Dependencies)
	}
	if s.Dependencies[2].Kind != "telepathy" {
		t.Errorf("an unknown kind sorts %+v, want it last", s.Dependencies)
	}

	svg := string(SVG(s, meta()))
	for _, want := range []string{
		"ein-ausgesprochen-langer-worker-nam…", // cut on the box, whole in the tooltip
		"Worker ein-ausgesprochen-langer-worker-name-der-nicht-passt (worker)",
		"and 3 more", // eight elements named, the rest counted
		"not deployed here when recorded",
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("SVG lacks %q", want)
		}
	}
	if strings.Contains(svg, "kommt-spaeter") {
		t.Error("a dependency kind this build does not draw reached the picture")
	}
	assertWellFormed(t, []byte(svg))

	doc := string(ArchiMate(s, meta()))
	for _, want := range []string{
		"A worker of type unknown",
		"Nothing on the recording installation held that process",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("ArchiMate document lacks %q", want)
		}
	}
	if strings.Contains(doc, "kommt-spaeter") {
		t.Error("a dependency kind this mapping does not know was written")
	}
	if got := usesText(nil); got != "no recorded element" {
		t.Errorf("usesText(nil) = %q", got)
	}
}
