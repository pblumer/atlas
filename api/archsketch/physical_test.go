package archsketch

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/panorama"
)

// TestParseEndpointKeepsOnlySchemeHostAndPort: a configured endpoint is reduced to the
// three parts a sketch shows, so a credential, a path or a query in it never reaches a
// snapshot — and a value that cannot be an address is reported as such, never echoed.
func TestParseEndpointKeepsOnlySchemeHostAndPort(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want *Endpoint
		text string
	}{
		{"", nil, ""},
		{"https://svc:geheim@ERP.example.com:8443/api/v2?token=x#frag", &Endpoint{Scheme: "https", Host: "erp.example.com", Port: "8443"}, "https://erp.example.com:8443"},
		{"ldaps://dc1.corp.local", &Endpoint{Scheme: "ldaps", Host: "dc1.corp.local"}, "ldaps://dc1.corp.local"},
		{"mail.example.com:587", &Endpoint{Host: "mail.example.com", Port: "587"}, "mail.example.com:587"},
		{"relay.example.com", &Endpoint{Host: "relay.example.com"}, "relay.example.com"},
		{"svc@relay.example.com:25/ignored", &Endpoint{Host: "relay.example.com", Port: "25"}, "relay.example.com:25"},
		{"[::1]:9000", &Endpoint{Host: "::1", Port: "9000"}, "[::1]:9000"},
		{"http://[2001:db8::1]:8080/x", &Endpoint{Scheme: "http", Host: "2001:db8::1", Port: "8080"}, "http://[2001:db8::1]:8080"},
		{"not a host at all", &Endpoint{Unparseable: true}, "endpoint not readable as an address"},
		{"https://", &Endpoint{Unparseable: true}, "endpoint not readable as an address"},
		{"https://bad host/", &Endpoint{Unparseable: true}, "endpoint not readable as an address"},
	} {
		got := ParseEndpoint(tc.raw)
		if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
			t.Errorf("ParseEndpoint(%q) = %+v, want %+v", tc.raw, got, tc.want)
			continue
		}
		if got != nil && got.String() != tc.text {
			t.Errorf("ParseEndpoint(%q).String() = %q, want %q", tc.raw, got.String(), tc.text)
		}
		if got != nil && (strings.Contains(got.String(), "geheim") || strings.Contains(got.String(), "token")) {
			t.Errorf("ParseEndpoint(%q) kept a credential or query: %q", tc.raw, got.String())
		}
	}
}

// physicalSample is sample() as recorded on a named production installation: one
// worker run by Atlas itself, one by a worker process that reported it, one by a
// worker that keeps its own configuration, a job type nobody has served since the
// restart, and a process with public start links.
func physicalSample() Snapshot {
	s := sample()
	s.Dependencies = append(s.Dependencies,
		Dependency{Kind: DependencyJobType, Name: "adresse-pruefen", UsedBy: []Use{{ProcessID: "eingang", ElementID: "Task_Adresse"}}},
		Dependency{Kind: DependencyJobType, Name: "archivieren", UsedBy: []Use{{ProcessID: "rechnung", ElementID: "Task_Archiv"}}},
		Dependency{Kind: DependencyWorker, Name: "ad-forest", WorkerType: "ad", UsedBy: []Use{{ProcessID: "eingang", ElementID: "Join"}}},
	)
	s.Physical = &Physical{
		Node: "prod-1", Environment: "PROD",
		ExternalURL:      &Endpoint{Scheme: "https", Host: "atlas.example.com"},
		PublicStartLinks: []StartLinks{{ProcessID: "rechnung", Links: 1}, {ProcessID: "eingang", Links: 2}},
		Workers: []WorkerSite{
			{Name: "smtp-main", WorkerType: "mail", Placement: PlacementEngine, ConfiguredID: "w-mail",
				Endpoint: &Endpoint{Host: "relay.example.com", Port: "587"}},
			{Name: "erp", WorkerType: "rest", Placement: PlacementWorker, ConfiguredID: "w-erp",
				Endpoint:  &Endpoint{Scheme: "https", Host: "erp.example.com", Port: "8443"},
				Observers: Observers{ObservedBy: []string{"worker-b", "worker-a"}}},
			{Name: "ad-forest", WorkerType: "ad", Placement: PlacementWorker},
		},
		JobTypes: []JobTypeSite{{JobType: "archivieren", Observers: Observers{ObservedBy: []string{"archiver"}}}, {JobType: "adresse-pruefen"}},
	}
	s.Normalize()
	return s
}

func TestThePhysicalPartIsDrawnFromTheSideThatOpensTheConnection(t *testing.T) {
	s := physicalSample()
	svg := string(SVG(s, meta()))
	assertWellFormed(t, []byte(svg))
	for _, want := range []string{
		"Logical and physical view of prod-1 (PROD)",
		"at https://atlas.example.com",
		"Public start form eingang", "2 links",
		"→ relay.example.com:587", "run by Atlas itself",
		"→ https://erp.example.com:8443", "seen: worker-a, worker-b",
		"endpoint held by the worker itself",
		"Job type adresse-pruefen", "no worker seen since the server started",
		"from the side that opens it",
		"the endpoint of a worker that keeps its own configuration",
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("SVG lacks %q", want)
		}
	}
	// Atlas opens the connection to the mail relay (it runs the mail Worker Type
	// itself): that line starts at the application and points at the box. The REST
	// worker's process opens its lease: that line starts at the box.
	appRight, boxLeft := nodeX+appInset+(nodeWidth-2*appInset), colRightX
	if !strings.Contains(svg, `x1="`+itoa(appRight)+`"`) || !strings.Contains(svg, `x1="`+itoa(boxLeft)+`"`) {
		t.Error("no line runs from the application outwards, or none from a box inwards")
	}
}

func TestARedactedReadWithholdsEndpointsAndInstances(t *testing.T) {
	s := physicalSample()
	before := *s.Physical
	red := s.Physical.Redacted(func(id string) bool { return id == "w-mail" }, false)
	if s.Physical.Workers[1].Endpoint.Withheld || len(before.Workers[1].ObservedBy) != 2 {
		t.Fatal("Redacted changed the snapshot it was called on")
	}
	erp := red.Workers[1]
	if erp.Name != "erp" || erp.Endpoint == nil || !erp.Endpoint.Withheld || erp.Endpoint.Host != "" {
		t.Errorf("erp = %+v, want its endpoint withheld and its host gone", erp.Endpoint)
	}
	if erp.ObservedBy != nil || !erp.ObserversWithheld || erp.Observed != 2 {
		t.Errorf("erp observers = %+v, want the ids withheld and the count kept", erp.Observers)
	}
	if mail := red.Workers[2]; mail.Name != "smtp-main" || mail.Endpoint.Withheld {
		t.Errorf("smtp-main = %+v, want its endpoint shown to a reader who may see it", mail)
	}

	s.Physical = &red
	svg := string(SVG(s, meta()))
	if strings.Contains(svg, "erp.example.com") || strings.Contains(svg, "worker-a") {
		t.Error("a withheld endpoint or instance reached the SVG")
	}
	for _, want := range []string{"endpoint withheld from this reader", "seen: 2 workers"} {
		if !strings.Contains(svg, want) {
			t.Errorf("redacted SVG lacks %q", want)
		}
	}
	doc := string(ArchiMate(s, meta()))
	if strings.Contains(doc, "erp.example.com") || strings.Contains(doc, "worker-a") {
		t.Error("a withheld endpoint or instance reached the ArchiMate document")
	}
	if !strings.Contains(doc, "Its target endpoint is not in this document: endpoint withheld from this reader.") {
		t.Error("the document does not say an endpoint was withheld")
	}
}

func TestThePhysicalArchiMateIsValid(t *testing.T) {
	s := physicalSample()
	doc := ArchiMate(s, meta())
	if res := panorama.Validate(doc); len(res.Problems) != 0 {
		t.Fatalf("panorama.Validate reports %+v", res.Problems)
	}
	types, rels := readExchange(t, doc)
	paths := 0
	for _, r := range rels {
		ok, refusal := panorama.MayConnect(r.typ, types[r.source], types[r.target])
		if ok {
			continue
		}
		// Path is ArchiMate 3.2 and outside Panorama's authoring subset (ADR-0471 §3):
		// that refusal, and only that one, is expected.
		if types[r.source] == "Path" && refusal.Reason == panorama.RefusedOutOfSubset {
			paths++
			continue
		}
		t.Errorf("%s from %s (%s) to %s (%s) is refused: %s",
			r.typ, r.source, types[r.source], r.target, types[r.target], refusal.Message)
	}
	// Every Path has two ends: the leases of the two worker processes (ERP and AD), and
	// the connections to the two target systems whose endpoints are known (mail, ERP).
	if paths != 8 {
		t.Errorf("%d path associations, want 8", paths)
	}
	text := string(doc)
	for _, want := range []string{
		"Atlas — prod-1 (PROD)",
		`<value xml:lang="en">relay.example.com</value>`,
		`<value xml:lang="en">8443</value>`,
		`<value xml:lang="en">atlas</value>`, `<value xml:lang="en">worker</value>`,
		`<value xml:lang="en">adresse-pruefen</value>`,
		"Public start form eingang",
		"the physical one of prod-1 (PROD)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("ArchiMate document lacks %q", want)
		}
	}
}

func TestThePhysicalPartIsDeterministic(t *testing.T) {
	a := physicalSample()
	b := physicalSample()
	p := b.Physical
	p.Workers[0], p.Workers[2] = p.Workers[2], p.Workers[0]
	p.Workers[1].ObservedBy = []string{"worker-a", "worker-b"}
	b.Normalize()
	if !bytes.Equal(SVG(a, meta()), SVG(b, meta())) || !bytes.Equal(ArchiMate(a, meta()), ArchiMate(b, meta())) {
		t.Error("the same physical part read in another order renders differently")
	}
}

func TestAnInstallationWithoutNamesSaysSo(t *testing.T) {
	for _, tc := range []struct {
		p    Physical
		want string
	}{
		{Physical{}, "an unnamed installation"},
		{Physical{Environment: "INT"}, "an unnamed installation (INT)"},
		{Physical{Node: "atlas-3"}, "atlas-3"},
	} {
		if got := installationName(tc.p); got != tc.want {
			t.Errorf("installationName(%+v) = %q, want %q", tc.p, got, tc.want)
		}
	}
	s := sample()
	s.Physical = &Physical{}
	if svg := string(SVG(s, meta())); !strings.Contains(svg, "external address not configured") {
		t.Error("a physical part with no external URL does not say so")
	}
	if got := (WorkerSite{Placement: PlacementEngine, ConfiguredID: "x"}).siteText(); got != "no endpoint in its configuration here" {
		t.Errorf("siteText = %q", got)
	}
	// A job type on a snapshot with no physical part is drawn from the logical part
	// alone; a host name longer than DNS allows is not an address.
	logical := sample()
	logical.Dependencies = append(logical.Dependencies, Dependency{Kind: DependencyJobType, Name: "nur-logisch", UsedBy: []Use{{ProcessID: "p"}}})
	if svg := string(SVG(logical, meta())); !strings.Contains(svg, "Job type nur-logisch") || strings.Contains(svg, "seen:") {
		t.Error("a job type without a physical part is not drawn from the logical part alone")
	}
	if e := ParseEndpoint(strings.Repeat("a", 254)); e == nil || !e.Unparseable {
		t.Errorf("an over-long host = %+v, want unparseable", e)
	}
}

func itoa(n int) string {
	var b [20]byte
	i := len(b)
	for {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
		if n == 0 {
			return string(b[i:])
		}
	}
}

// TestAReportedEndpointIsDrawnAsReportedAndReadByOperators: an endpoint a worker
// reported is drawn as reported, goes to whoever may see the instances rather than
// whoever may see a configuration, and instances that disagree are drawn as
// disagreeing.
func TestAReportedEndpointIsDrawnAsReportedAndReadByOperators(t *testing.T) {
	s := physicalSample()
	for i := range s.Physical.Workers {
		switch s.Physical.Workers[i].Name {
		case "ad-forest":
			s.Physical.Workers[i].Endpoint = &Endpoint{Scheme: "ldaps", Host: "dc.corp.local"}
			s.Physical.Workers[i].EndpointReported = true
		case "erp":
			s.Physical.Workers[i].Endpoint, s.Physical.Workers[i].ConfiguredID = nil, ""
			s.Physical.Workers[i].EndpointsDiffer = true
		}
	}
	svg := string(SVG(s, meta()))
	for _, want := range []string{"→ ldaps://dc.corp.local (reported)", "workers report different endpoints",
		"had not polled since the server started"} {
		if !strings.Contains(svg, want) {
			t.Errorf("SVG lacks %q", want)
		}
	}
	if doc := string(ArchiMate(s, meta())); !strings.Contains(doc, "as the worker instances serving it reported it") {
		t.Error("the ArchiMate document does not say the endpoint was reported")
	}

	everyWorker := func(string) bool { return true }
	if red := s.Physical.Redacted(everyWorker, false); !red.Workers[0].Endpoint.Withheld {
		t.Errorf("a non-operator reads %+v, want the reported endpoint withheld even with the worker's configuration in view", red.Workers[0].Endpoint)
	}
	if red := s.Physical.Redacted(func(string) bool { return false }, true); red.Workers[0].Endpoint.Withheld {
		t.Error("an operator is refused a reported endpoint because a configuration is out of view")
	}

	// A version-2 snapshot keeps its own wording.
	s.FormatVersion = 2
	if !strings.Contains(string(SVG(s, meta())), "which such a worker does not report yet") {
		t.Error("a version-2 snapshot is not drawn as version 2 drew it")
	}
}
