package archsketch

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/panorama"
)

// ADR-0495 slice B: what else a Worker's configuration says, the systems it reaches
// beyond its target, and the inbound subscriptions that feed a way in.

func detailedSample() Snapshot {
	s := physicalSample()
	for i := range s.Physical.Workers {
		w := &s.Physical.Workers[i]
		switch w.Name {
		case "smtp-main":
			w.Provider = "smtp"
			w.Credential = true
			w.MailboxEndpoint = &Endpoint{Scheme: "imaps", Host: "mail.example.com", Port: "993"}
			w.ScannerEndpoint = &Endpoint{Scheme: "clamd", Host: "av.example.com", Port: "3310"}
		case "erp":
			w.Identity = &WorkerIdentity{Mode: "delegated-per-job", Enforcement: "shadow"}
			w.Credential = true
			w.Model = "gpt-4o-mini"
		}
	}
	s.Physical.Inbound = []InboundSite{
		{MessageName: "auftrag", Worker: "events", WorkerType: "clio", ConfiguredID: "w-events",
			Subject: "orders/new", Recursive: true, Endpoint: &Endpoint{Scheme: "https", Host: "clio.example.com", Port: "8443"}},
	}
	s.Normalize()
	return s
}

func TestAWorkersConfigurationIsDrawnOnAFourthLine(t *testing.T) {
	svg := string(SVG(detailedSample(), meta()))
	for _, want := range []string{
		`height="88"`,
		"delegated per instance (policy recorded, not enforced) · credential configured · model gpt-4o-mini",
		"credential configured · via smtp · mailbox imaps://mail.example.com:993 · scanner clamd://av.example.com:3310",
		"← events · orders/new/…",
		"mapping version 4",
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("the sketch lacks %q", want)
		}
	}
	// A picture without any of it keeps the three-line boxes it always had.
	if plain := string(SVG(physicalSample(), meta())); strings.Contains(plain, `height="88"`) {
		t.Error("a snapshot with nothing more to say grew a fourth line")
	}
}

func TestAnOlderSnapshotDrawsNoDetailItDidNotHave(t *testing.T) {
	s := detailedSample()
	s.FormatVersion, s.MappingVersion = 3, 3
	s.Physical.Inbound = nil
	for _, name := range []string{"smtp-main", "erp"} {
		w := s.Physical.worker(name, map[string]string{"smtp-main": "mail", "erp": "rest"}[name])
		w.Provider, w.ScannerEndpoint, w.Model, w.Identity, w.Credential = "", nil, "", nil, false
	}
	for name, sel := range map[string]Snapshot{"both layers": s, "technology": s.Select(SelectionOf(LayerTechnology))} {
		svg, doc := string(SVG(sel, meta())), string(ArchiMate(sel, meta()))
		if strings.Contains(svg, "mailbox imaps://") || strings.Contains(svg, `height="88"`) {
			t.Errorf("%s: a version-3 snapshot draws its mailbox as version 4 does", name)
		}
		if strings.Contains(doc, "id-mailbox-") {
			t.Errorf("%s: a version-3 snapshot writes its mailbox as a target system", name)
		}
	}
}

func TestAReaderWhoMayNotSeeTheWorkerSeesThatItHasMoreNotWhat(t *testing.T) {
	s := detailedSample()
	red := s.Physical.Redacted(func(string) bool { return false }, false)
	s.Physical = &red
	for _, w := range red.Workers {
		if w.Model != "" || w.Identity != nil || w.Credential || w.Provider != "" || w.ScannerEndpoint != nil {
			t.Errorf("worker %s keeps configuration for a reader who may not see it: %+v", w.Name, w)
		}
	}
	if in := red.Inbound[0]; in.Subject != "" || in.Endpoint != nil || !in.Withheld || in.Worker != "events" {
		t.Errorf("inbound subscription = %+v, want its worker named and its subject and endpoint withheld", in)
	}
	svg, doc := string(SVG(s, meta())), string(ArchiMate(s, meta()))
	for _, leak := range []string{"gpt-4o-mini", "av.example.com", "orders/new", "clio.example.com", "delegated"} {
		if strings.Contains(svg, leak) || strings.Contains(doc, leak) {
			t.Errorf("a reader who may not see the configuration is shown %q", leak)
		}
	}
	for _, want := range []string{"configuration withheld from this reader", "← events (subject withheld from this reader)"} {
		if !strings.Contains(svg, want) {
			t.Errorf("the sketch does not say %q", want)
		}
	}
	// Nobody may see a Worker that has nothing more to say: no line is added for it.
	if w := red.Workers[0]; w.Name == "ad-forest" && w.DetailWithheld {
		t.Error("a worker with no configuration is marked as withholding one")
	}
}

func TestTheFurtherSystemsAndTheInboundSubscriptionAreValidArchiMate(t *testing.T) {
	for name, s := range map[string]Snapshot{
		"both layers": detailedSample(),
		"technology":  detailedSample().Select(SelectionOf(LayerTechnology)),
	} {
		doc := ArchiMate(s, meta())
		if res := panorama.Validate(doc); len(res.Problems) != 0 {
			t.Fatalf("%s: panorama.Validate reports %+v", name, res.Problems)
		}
		types, rels := readExchange(t, doc)
		for _, r := range rels {
			ok, refusal := panorama.MayConnect(r.typ, types[r.source], types[r.target])
			if !ok && !(types[r.source] == "Path" && refusal.Reason == panorama.RefusedOutOfSubset) {
				t.Errorf("%s: %s from %s (%s) to %s (%s) is refused: %s", name,
					r.typ, r.source, types[r.source], r.target, types[r.target], refusal.Message)
			}
		}
		text := string(doc)
		for _, want := range []string{
			`<value xml:lang="en">delegated-per-job</value>`,
			`<value xml:lang="en">gpt-4o-mini</value>`,
			`<value xml:lang="en">av.example.com</value>`,
			`<value xml:lang="en">mail.example.com</value>`,
			`<value xml:lang="en">orders/new</value>`,
			"Atlas watches events · orders/new/… and publishes each new event as message auftrag.",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: the document lacks %q", name, want)
			}
		}
	}
	// With the application layer the subscription is joined to the way in it feeds.
	types, rels := readExchange(t, ArchiMate(detailedSample(), meta()))
	joined := false
	for _, r := range rels {
		if strings.HasPrefix(r.source, "id-inbound-") && types[r.target] == "ApplicationInterface" {
			joined = true
		}
	}
	if !joined {
		t.Error("the inbound subscription is not joined to the message it publishes")
	}
}

func TestTheInstallationAloneShowsTheInboundSubscription(t *testing.T) {
	svg := string(SVG(detailedSample().Select(SelectionOf(LayerTechnology)), meta()))
	for _, want := range []string{"Inbound auftrag", "watches events · orders/new/…", "→ https://clio.example.com:8443",
		"model gpt-4o-mini"} {
		if !strings.Contains(svg, want) {
			t.Errorf("the installation alone lacks %q", want)
		}
	}
}

func TestSeveralInboundSubscriptionsForOneMessageAreCounted(t *testing.T) {
	s := detailedSample()
	s.Physical.Inbound = append(s.Physical.Inbound, InboundSite{MessageName: "auftrag", Worker: "", ConfiguredID: "w-gone", Subject: "legacy"})
	s.Normalize()
	svg := string(SVG(s, meta()))
	if !strings.Contains(svg, "← 2 inbound subscriptions") || !strings.Contains(svg, "a deleted worker · legacy") {
		t.Error("two subscriptions for one message are not counted, or the one without a worker is not named as such")
	}
	if got := (InboundSite{Worker: "w"}).sourceText(); got != "w" {
		t.Errorf("sourceText without a subject = %q", got)
	}
	if got := (WorkerIdentity{Mode: "custom"}).text(); got != "identity custom" {
		t.Errorf("an unknown identity mode reads %q", got)
	}
	if (&Physical{}).inboundFor("x") != nil || (*Physical)(nil).inboundFor("x") != nil || (*Physical)(nil).inboundSubscriptions() != nil {
		t.Error("no inbound subscription is found where none is recorded")
	}
}
