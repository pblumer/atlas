package archsketch

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/panorama"
)

// ADR-0495 slice E: the release's coupling to other applications through message and
// signal names, and the event feed, drawn in the application layer and written as
// processes triggering one another.

// couplingSample is the physical sample with a coupling part, in an order Normalize
// settles.
func couplingSample() Snapshot {
	s := physicalSample()
	s.Coupling = &Coupling{
		Names: []NameUse{
			{Kind: NameMessage, Name: "rechnung-versandt", Direction: DirectionOut, UsedBy: []Use{{ProcessID: "rechnung", ElementID: "Versandt"}}},
			{Kind: NameSignal, Name: "periodenabschluss", Direction: DirectionIn, UsedBy: []Use{{ProcessID: "rechnung", ElementID: "Abschluss"}}},
			{Kind: NameSignal, Name: "storno", Direction: DirectionOut, UsedBy: []Use{{ProcessID: "eingang", ElementID: "Storno"}}},
			{Kind: NameMessage, Name: "rechnung-versandt", Direction: DirectionOut, UsedBy: []Use{{ProcessID: "rechnung", ElementID: "Erneut"}}},
		},
		Peers: []Peer{
			{Kind: NameMessage, Name: "auftrag", Direction: DirectionIn, ApplicationID: "app-shop", Application: "Shop", ProcessID: "bestellung", ElementID: "Senden"},
			{Kind: NameMessage, Name: "rechnung-versandt", Direction: DirectionOut, ApplicationID: "app-mahn", Application: "Mahnwesen", ProcessID: "mahnlauf", ElementID: "Warten"},
			{Kind: NameSignal, Name: "periodenabschluss", Direction: DirectionIn, ProcessID: "kalender", ElementID: "Monatsende"},
			{Kind: NameMessage, Name: "auftrag", Direction: DirectionIn, ApplicationID: "app-shop", Application: "Shop", ProcessID: "bestellung", ElementID: "Senden"},
			{Kind: NameMessage, Name: "rechnung-versandt", Direction: DirectionOut, ApplicationID: "system", Application: "Atlas", ProcessID: "atlas-audit", ElementID: "Log", System: true},
		},
		Feed: EventFeed{Subscriptions: 2, Targets: []FeedTarget{
			{Worker: "siem", ConfiguredID: "w-siem", Endpoint: &Endpoint{Scheme: "https", Host: "siem.example.com", Port: "443"}},
			{Worker: "datalake", ConfiguredID: "w-lake", Endpoint: &Endpoint{Scheme: "https", Host: "lake.example.com", Port: "443"}},
		}},
	}
	s.Normalize()
	return s
}

func TestTheCouplingIsNormalizedAndBounded(t *testing.T) {
	c := couplingSample().Coupling
	if len(c.Names) != 3 || c.Names[0].Name != "periodenabschluss" || c.Names[1].Name != "rechnung-versandt" || len(c.Names[1].UsedBy) != 2 {
		t.Errorf("names = %+v, want ordered by direction, kind and name, the repeated name merged", c.Names)
	}
	if len(c.Peers) != 4 {
		t.Errorf("peers = %+v, want the repeated peer dropped", c.Peers)
	}
	if c.Feed.Targets[0].Worker != "datalake" {
		t.Errorf("feed targets = %+v, want ordered by worker", c.Feed.Targets)
	}

	big := Coupling{}
	for i := range maxNameUses + 2 {
		big.Names = append(big.Names, NameUse{Kind: NameMessage, Name: fmt.Sprintf("n%04d", i), Direction: DirectionOut})
	}
	for i := range maxPeers + 3 {
		big.Peers = append(big.Peers, Peer{Kind: NameMessage, Name: "n", Direction: DirectionOut, ProcessID: fmt.Sprintf("p%04d", i)})
	}
	for i := range maxFeedTarget + 1 {
		big.Feed.Targets = append(big.Feed.Targets, FeedTarget{ConfiguredID: fmt.Sprintf("w%04d", i)})
	}
	big.normalize()
	if big.Omitted != (CouplingOmitted{Names: 2, Peers: 3, FeedTargets: 1}) {
		t.Errorf("omitted = %+v", big.Omitted)
	}
	gaps := strings.Join(Snapshot{Coupling: &big}.NotShown(), "\n")
	if !strings.Contains(gaps, "beyond the snapshot's bounds: 2 message and signal names, 3 processes of other applications, 1 feed endpoint") {
		t.Errorf("the gaps do not count what the bounds left out:\n%s", gaps)
	}
}

func TestAReaderSeesOnlyTheApplicationsTheyMaySee(t *testing.T) {
	s := couplingSample()
	red := s.Coupling.Redacted(func(p Peer) bool { return p.ApplicationID == "app-shop" }, func(id string) bool { return id == "w-siem" })
	if red.PeersWithheld != 3 {
		t.Errorf("withheld = %d, want the three peers outside app-shop", red.PeersWithheld)
	}
	for _, p := range red.Peers {
		if p.Withheld && (p.Application != "" || p.ProcessID != "" || p.ApplicationID != "") {
			t.Errorf("a withheld peer keeps %+v", p)
		}
	}
	if red.Feed.Targets[0].Withheld != true || red.Feed.Targets[0].Endpoint != nil || red.Feed.Targets[1].Worker != "siem" {
		t.Errorf("feed targets = %+v, want datalake withheld and siem kept", red.Feed.Targets)
	}
	if s.Coupling.PeersWithheld != 0 || s.Coupling.Peers[0].Withheld {
		t.Error("the stored coupling was changed by a read")
	}
	s.Coupling = &red
	for i, d := range s.Dependencies {
		if d.Kind == DependencyCall {
			s.Dependencies[i].TargetWithheld, s.Dependencies[i].TargetApplication, s.Dependencies[i].TargetApplicationID = true, "", ""
		}
	}
	svg, doc := string(SVG(s, meta())), string(ArchiMate(s, meta()))
	for _, leak := range []string{"Mahnwesen", "mahnlauf", "kalender", "lake.example.com", "datalake", "app-mahn"} {
		if strings.Contains(svg, leak) || strings.Contains(doc, leak) {
			t.Errorf("a reader who may not see it is shown %q", leak)
		}
	}
	for _, want := range []string{"→ received by another application", "in another application · 1 element",
		"3 processes of applications this reader may not see"} {
		if !strings.Contains(svg, want) {
			t.Errorf("the sketch does not say %q", want)
		}
	}
	if !strings.Contains(doc, "Another application") || !strings.Contains(doc, "this reader may not see it") {
		t.Error("the document does not name the called application as withheld")
	}
}

func TestTheCouplingIsDrawnInTheApplicationLayer(t *testing.T) {
	svg := string(SVG(couplingSample(), meta()))
	for _, want := range []string{
		"← thrown by Shop",
		"Signal periodenabschluss", "caught at 1 element", "← thrown by a process in no application",
		"Sends message rechnung-versandt", "thrown at 2 elements", "→ received by Atlas itself, Mahnwesen",
		"Broadcasts signal storno", "→ received by no other application here",
		"Event feed", "pushed by Atlas · 2 subscriptions", "carries its incidents",
		"datalake → https://lake.example.com:443",
		"Dashed line at a message or signal box: a name Atlas correlates itself; no connection crosses it.",
		"messages published through the API",
		fmt.Sprintf("mapping version %d", MappingVersion),
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("the sketch lacks %q", want)
		}
	}
	if !strings.Contains(svg, `stroke-dasharray="6 4" marker-end="url(#arrow)"`) {
		t.Error("the caught signal is not drawn with a dashed line")
	}
	assertWellFormed(t, []byte(svg))

	// The logical view alone gives the coupling's boxes their third line too.
	logical := string(SVG(couplingSample().Select(SelectionOf(LayerApplication)), meta()))
	if !strings.Contains(logical, "← thrown by Shop") || !strings.Contains(logical, `height="72"`) {
		t.Error("the logical view drops the coupling's third line")
	}
	// The coupling belongs to the application layer: the installation alone has none.
	if tech := string(SVG(couplingSample().Select(SelectionOf(LayerTechnology)), meta())); strings.Contains(tech, "Sends message") {
		t.Error("the installation alone draws the coupling")
	}

	if got := peersText([]Peer{{Application: "A"}, {Application: "B"}, {Application: "C"}, {ApplicationID: "d"}}); got != "A, B and 2 more" {
		t.Errorf("four peers read %q", got)
	}
	if got := (EventFeed{Targets: []FeedTarget{{Worker: "w"}, {Withheld: true}}}).targetsText(); got != "w, 1 endpoint withheld from this reader" {
		t.Errorf("feed targets read %q", got)
	}

	// With the business layer shown, the feed says it carries the products' outcomes.
	s := couplingSample()
	s.Business = &Business{Products: []Product{{ID: "p", Name: "P"}}}
	if !strings.Contains(string(SVG(s.Select(SelectionOf(LayerApplication, LayerBusiness)), meta())), "carries its incidents and product outcomes") {
		t.Error("the feed does not say it carries the products' outcomes")
	}
}

func TestTheCouplingIsValidArchiMate(t *testing.T) {
	doc := ArchiMate(couplingSample(), meta())
	if res := panorama.Validate(doc); len(res.Problems) != 0 {
		t.Fatalf("panorama.Validate reports %+v", res.Problems)
	}
	types, rels := readExchange(t, doc)
	count := map[string]int{}
	feedServes := 0
	for _, r := range rels {
		if r.source == "id-event-feed" && r.typ == "Serving" && r.target == "id-application" {
			feedServes++
		}
		ok, refusal := panorama.MayConnect(r.typ, types[r.source], types[r.target])
		if !ok && !(types[r.source] == "Path" && refusal.Reason == panorama.RefusedOutOfSubset) {
			t.Errorf("%s from %s (%s) to %s (%s) is refused: %s", r.typ, r.source, types[r.source], r.target, types[r.target], refusal.Message)
		}
		count[r.typ+" "+types[r.source]+" "+types[r.target]]++
	}
	for rel, want := range map[string]int{
		// Shop's bestellung triggers eingang; rechnung triggers mahnung and the audit
		// process; kalender triggers rechnung.
		"Triggering ApplicationProcess ApplicationProcess":   4,
		"Assignment ApplicationComponent ApplicationProcess": 6,
	} {
		if count[rel] != want {
			t.Errorf("%s: %d, want %d", rel, count[rel], want)
		}
	}
	if feedServes != 1 {
		t.Errorf("the event feed serves the application %d times, want once", feedServes)
	}
	for _, want := range []string{"Event feed", `<value xml:lang="en">2</value>`, "Process bestellung", "Mahnwesen"} {
		if !strings.Contains(string(doc), want) {
			t.Errorf("the document lacks %q", want)
		}
	}
}
