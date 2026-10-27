package archsketch

import (
	"cmp"
	"slices"
	"strings"
)

// Cross-application coupling (ADR-0495 slice E): how a release meets the other
// applications on the recording installation through names. A message is correlated by
// its name across the whole installation and a signal is broadcast to every process
// that catches its name, so a release that receives a message is coupled to every
// process elsewhere that throws it, and one that throws a message or a signal to every
// process elsewhere that receives it. The event feed (ADR-0433) is the other way out:
// Atlas pushes its incidents, entitlements and action outcomes to the endpoints the
// installation subscribed.
//
// Everything here is read off the deployed definitions and the feed subscriptions at
// the moment the snapshot is taken. Another application's name and processes are shown
// only to a reader who may see that application, the starmap's rule (ADR-0211,
// ADR-0495 §5); everyone else learns that another application is there. A feed
// endpoint is shown only to a reader who may see its Worker's configuration.

// The bounds on the coupling's lists (ADR-0495 §6), guards well above what a real
// installation holds; what they leave out is counted in [Coupling.Omitted].
const (
	maxNameUses   = 100
	maxPeers      = 200
	maxFeedTarget = 20
)

// Name kinds.
const (
	NameMessage = "message"
	NameSignal  = "signal"
)

// Directions, from the release's side.
const (
	// DirectionIn is a name the release receives: a peer throws it.
	DirectionIn = "in"
	// DirectionOut is a name the release throws: a peer receives it.
	DirectionOut = "out"
)

// Coupling is the release's cross-application coupling, absent on a snapshot taken
// before format version 7.
type Coupling struct {
	// Names are the messages and signals the release throws, and the signals it
	// catches; the messages it receives are its entry points.
	Names []NameUse `json:"names"`
	// Peers are the processes of other applications that throw a name the release
	// receives, or receive a name it throws.
	Peers []Peer `json:"peers"`
	// Feed is the event feed the installation pushes.
	Feed EventFeed `json:"feed"`
	// PeersWithheld counts the peers a read withheld from its reader; it is set by
	// [Coupling.Redacted], never stored.
	PeersWithheld int `json:"peersWithheld,omitempty"`
	// Omitted counts, per kind, what the bounds left out.
	Omitted CouplingOmitted `json:"omitted,omitzero"`
}

// CouplingOmitted counts what the bounds left out of each list.
type CouplingOmitted struct {
	Names       int `json:"names,omitempty"`
	Peers       int `json:"peers,omitempty"`
	FeedTargets int `json:"feedTargets,omitempty"`
}

// NameUse is one message or signal name the release throws or catches, with the
// elements that do.
type NameUse struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Direction string `json:"direction"`
	UsedBy    []Use  `json:"usedBy"`
}

// Peer is one element of another application's process that throws a name the release
// receives, or receives one it throws.
type Peer struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Direction string `json:"direction"`
	// ApplicationID and Application name the peer's application; both are empty for a
	// process filed in no application.
	ApplicationID string `json:"applicationId,omitempty"`
	Application   string `json:"application,omitempty"`
	ProcessID     string `json:"processId,omitempty"`
	ElementID     string `json:"elementId,omitempty"`
	// System is set for one of Atlas's own processes (ADR-0435).
	System bool `json:"system,omitempty"`
	// Withheld is set by a read for a reader who may not see the peer's application:
	// the name and direction stay, the application and process do not.
	Withheld bool `json:"withheld,omitempty"`
}

// EventFeed is the event feed the installation pushes (ADR-0433): how many
// subscriptions are enabled and the endpoints they push to. The feed carries every
// application's events, narrowed only by catalogue reach, so a release's incidents go
// to it, and its products' action outcomes when it carries out products.
type EventFeed struct {
	Subscriptions int          `json:"subscriptions"`
	Targets       []FeedTarget `json:"targets"`
}

// FeedTarget is one endpoint the feed is pushed to, through a cloudevents Worker.
type FeedTarget struct {
	Worker       string    `json:"worker,omitempty"`
	ConfiguredID string    `json:"configuredId,omitempty"`
	Endpoint     *Endpoint `json:"endpoint,omitempty"`
	// Withheld is set by a read for a reader who may not see the Worker.
	Withheld bool `json:"withheld,omitempty"`
}

// normalize puts every list in a fixed order, merges names thrown by several elements,
// drops repeated peers, and applies the bounds.
func (c *Coupling) normalize() {
	names := map[[3]string]*NameUse{}
	for _, n := range c.Names {
		key := [3]string{n.Kind, n.Direction, n.Name}
		if have, ok := names[key]; ok {
			have.UsedBy = append(have.UsedBy, n.UsedBy...)
			continue
		}
		cp := n
		cp.UsedBy = append([]Use(nil), n.UsedBy...)
		names[key] = &cp
	}
	c.Names = c.Names[:0]
	for _, n := range names {
		n.UsedBy = normalizeUses(n.UsedBy)
		if n.UsedBy == nil {
			n.UsedBy = []Use{}
		}
		c.Names = append(c.Names, *n)
	}
	slices.SortFunc(c.Names, func(a, b NameUse) int {
		return cmp.Or(cmp.Compare(a.Direction, b.Direction), cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Name, b.Name))
	})

	slices.SortFunc(c.Peers, func(a, b Peer) int {
		return cmp.Or(cmp.Compare(a.Direction, b.Direction), cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Name, b.Name),
			cmp.Compare(a.Application, b.Application), cmp.Compare(a.ApplicationID, b.ApplicationID),
			cmp.Compare(a.ProcessID, b.ProcessID), cmp.Compare(a.ElementID, b.ElementID))
	})
	c.Peers = slices.Compact(c.Peers)

	slices.SortFunc(c.Feed.Targets, func(a, b FeedTarget) int {
		return cmp.Or(cmp.Compare(a.Worker, b.Worker), cmp.Compare(a.ConfiguredID, b.ConfiguredID))
	})
	c.Feed.Targets = slices.CompactFunc(c.Feed.Targets, func(a, b FeedTarget) bool { return a.ConfiguredID == b.ConfiguredID && a.Worker == b.Worker })

	c.Names, c.Omitted.Names = bound(c.Names, maxNameUses, c.Omitted.Names)
	c.Peers, c.Omitted.Peers = bound(c.Peers, maxPeers, c.Omitted.Peers)
	c.Feed.Targets, c.Omitted.FeedTargets = bound(c.Feed.Targets, maxFeedTarget, c.Omitted.FeedTargets)
	if c.Names == nil {
		c.Names = []NameUse{}
	}
	if c.Peers == nil {
		c.Peers = []Peer{}
	}
	if c.Feed.Targets == nil {
		c.Feed.Targets = []FeedTarget{}
	}
}

// Redacted is the coupling as one reader sees it: a peer mayViewPeer refuses keeps
// its name and direction and loses its application and process, and is counted; a
// feed target whose Worker mayViewWorker refuses loses its name and endpoint. The
// coupling it is called on is not changed.
func (c Coupling) Redacted(mayViewPeer func(Peer) bool, mayViewWorker func(id string) bool) Coupling {
	out := c
	out.Peers = make([]Peer, 0, len(c.Peers))
	for _, p := range c.Peers {
		if !mayViewPeer(p) {
			p = Peer{Kind: p.Kind, Name: p.Name, Direction: p.Direction, Withheld: true}
			out.PeersWithheld++
		}
		out.Peers = append(out.Peers, p)
	}
	out.Feed.Targets = make([]FeedTarget, 0, len(c.Feed.Targets))
	for _, t := range c.Feed.Targets {
		if !mayViewWorker(t.ConfiguredID) {
			t = FeedTarget{Withheld: true}
		}
		out.Feed.Targets = append(out.Feed.Targets, t)
	}
	return out
}

// text lists what the bounds left out.
func (o CouplingOmitted) text() string {
	var parts []string
	for _, c := range []struct {
		n         int
		one, many string
	}{
		{o.Names, "message or signal name", "message and signal names"},
		{o.Peers, "process of another application", "processes of other applications"},
		{o.FeedTargets, "feed endpoint", "feed endpoints"},
	} {
		if c.n > 0 {
			parts = append(parts, plural(c.n, c.one, c.many))
		}
	}
	return strings.Join(parts, ", ")
}

// peersOf is the peers of one name in one direction.
func (c *Coupling) peersOf(kind, name, direction string) []Peer {
	if c == nil {
		return nil
	}
	var out []Peer
	for _, p := range c.Peers {
		if p.Kind == kind && p.Name == name && p.Direction == direction {
			out = append(out, p)
		}
	}
	return out
}

// label names a peer's application as this reader may know it.
func (p Peer) label() string {
	switch {
	case p.Withheld:
		return "another application"
	case p.System:
		return "Atlas itself"
	case p.Application != "":
		return p.Application
	case p.ApplicationID != "":
		return "application " + p.ApplicationID
	}
	return "a process in no application"
}

// peersText says who is at the other end of a name: the applications, each once.
func peersText(peers []Peer) string {
	var names []string
	for _, p := range peers {
		if l := p.label(); !slices.Contains(names, l) {
			names = append(names, l)
		}
	}
	switch len(names) {
	case 0:
		return ""
	case 1, 2:
		return strings.Join(names, ", ")
	}
	return names[0] + ", " + names[1] + " and " + plural(len(names)-2, "more", "more")
}

// peersDetail names each peer's process, for a tooltip.
func peersDetail(peers []Peer) string {
	parts := make([]string, 0, len(peers))
	for _, p := range peers {
		if p.Withheld || p.ProcessID == "" {
			parts = append(parts, p.label())
			continue
		}
		parts = append(parts, p.label()+" ("+p.ProcessID+"/"+p.ElementID+")")
	}
	return strings.Join(parts, ", ")
}

// targetsText says where the feed goes, as far as this reader may know it.
func (f EventFeed) targetsText() string {
	var visible []string
	withheld := 0
	for _, t := range f.Targets {
		switch {
		case t.Withheld:
			withheld++
		case t.Endpoint != nil && !t.Endpoint.Withheld:
			visible = append(visible, t.Worker+" → "+t.Endpoint.String())
		default:
			visible = append(visible, t.Worker)
		}
	}
	if withheld > 0 {
		visible = append(visible, plural(withheld, "endpoint withheld from this reader", "endpoints withheld from this reader"))
	}
	return strings.Join(visible, ", ")
}
