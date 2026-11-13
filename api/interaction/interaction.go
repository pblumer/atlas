// Package interaction computes the interaction graph of ADR-0559 around one process:
// which participants exchange which message names with it, what the deployed
// definitions say is possible, and what the event log says happened.
//
// It is a pure function of its inputs. The caller copies the definitions and the
// message sources on the run loop, takes a state.ReadView there, and runs Build with
// the loop free (invariant I3, ADR-0080): nothing here reaches loop-owned state, and
// the only thing that can grow with the instance population, the event-log scan, is
// bounded by Input.ScanLimit and says when it was cut.
//
// The two layers are kept apart in the answer (ADR-0559 §1). An exchange that is
// possible carries Possible; one that was seen carries Observed; one that is both
// carries both. Neither is inferred from the other.
package interaction

import (
	"errors"
	"sort"
	"time"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// Participant kinds. A process is a white box; every other kind is a black box,
// known only by what crosses the boundary (ADR-0559 §2).
const (
	// KindProcess is a process deployed on this installation.
	KindProcess = "process"
	// KindInboundWatch is a Worker's inbound watch that publishes a name the process
	// waits for. Only the possible layer names one: a send row of an inbound watch
	// does not say which watch it was.
	KindInboundWatch = "inbound-watch"
	// KindProductAction is a product action whose message the process waits for.
	KindProductAction = "product-action"
	// KindInbound is an inbound watch as the event log knows it: some watch, not
	// which one.
	KindInbound = "inbound"
	// KindPublish is a message published through the API, or by atlas's own order and
	// fulfilment, which the event log does not tell apart. Never derived as possible:
	// the route can send any name (ADR-0446 §3).
	KindPublish = "publish"
	// KindDirected is a delivery addressed to one instance (ADR-0428).
	KindDirected = "directed"
	// KindOutsideView is whatever the reader may not see: a sender or receiver whose
	// row the event log does not show them (ADR-0472 §5). It has no name on purpose.
	KindOutsideView = "outside-view"
	// KindRemoved is a definition that is no longer deployed, named in an older row.
	KindRemoved = "removed"
)

// Participant ids that are not per-process.
const (
	idInbound     = "inbound"
	idPublish     = "publish"
	idDirected    = "directed"
	idOutsideView = "outside-view"
	idRemoved     = "removed"
)

// ProcessID is the participant id of a process.
func ProcessID(processID string) string { return "process:" + processID }

// Point is one element of a deployed definition that sends or receives a message,
// as compiler.MessagePoint reports it.
type Point struct {
	Element string
	Message string
	// Role is start, catch, boundary, event-subprocess or throw.
	Role string
}

// Sends reports whether the point sends its message.
func (p Point) Sends() bool { return p.Role == "throw" }

// Definition is a deployed definition as the graph needs it.
type Definition struct {
	Key       uint64
	ProcessID string
	// Name is the definition's display name: the pool it was deployed under, else
	// the process's own name.
	Name      string
	Version   int32
	ProjectID string
	System    bool
	Inactive  bool
	// Newest is whether this is the newest deployed version of its process.
	Newest bool
	Points []Point
}

// Source is a sender of a message that is not a process: an inbound watch or a
// product action, as message-sources lists it for the caller.
type Source struct {
	Kind    string // KindInboundWatch or KindProductAction
	Message string
	Enabled bool

	ConnectorID   string
	ConnectorName string
	WorkerKind    string

	ProductID   string
	ProductName string
	Action      string
}

// Input is everything Build reads.
type Input struct {
	// ProcessID is the process the graph is computed around.
	ProcessID string
	// Definitions is every deployed definition the caller may read.
	Definitions []Definition
	// Sources is every inbound watch and product action the caller may see. Empty for
	// a caller who may not read message-sources.
	Sources []Source

	// Scan walks the event log newest first, calling fn per row until fn returns an
	// error; nil when there is no log to read.
	Scan func(fn func(state.EventLogEntry) error) error
	// ReadsAll is whether the caller reads every row (an operator, an administrator,
	// or a server without authentication).
	ReadsAll bool
	// Visible reports, for a caller who does not read every row, whether they may read
	// the rows of a definition (ADR-0472 §5).
	Visible func(defKey uint64) bool
	// Now and From bound the window, in unix nanoseconds: rows recorded before From
	// are not read.
	Now, From int64
	// ScanLimit is how many rows the scan reads at most.
	ScanLimit int
}

// Graph is the answer.
type Graph struct {
	ProcessID    string        `json:"processId"`
	Participants []Participant `json:"participants"`
	Exchanges    []Exchange    `json:"exchanges"`
	Unreceived   []Unreceived  `json:"unreceived"`
	Ports        []Port        `json:"ports"`
	Observed     Window        `json:"observed"`
}

// Participant is one party of the graph.
type Participant struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Center marks the process the graph is computed around.
	Center bool `json:"center,omitempty"`
	// Name is a proposal: the pool name the process was deployed under, the Worker's
	// name, the product's. Empty for a participant the reader may not see.
	Name string `json:"name,omitempty"`

	ProcessID string `json:"processId,omitempty"`
	Version   int32  `json:"version,omitempty"`
	// DefinitionKey is the newest deployed version's, so a client can read its
	// diagram to expand the process (ADR-0559 §8).
	DefinitionKey uint64 `json:"definitionKey,omitempty"`
	ProjectID     string `json:"projectId,omitempty"`
	System        bool   `json:"system,omitempty"`
	Inactive      bool   `json:"inactive,omitempty"`

	ConnectorID string `json:"connectorId,omitempty"`
	WorkerKind  string `json:"workerKind,omitempty"`
	ProductID   string `json:"productId,omitempty"`
	Action      string `json:"action,omitempty"`
}

// Exchange is one message name passing from one participant to another.
type Exchange struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Message string `json:"message"`
	// Possible is whether the newest, active definitions (and the enabled sources)
	// make this exchange possible.
	Possible bool `json:"possible"`
	// Observed is what the event log shows of it in the window; nil when nothing.
	Observed *Observed `json:"observed,omitempty"`
}

// Observed counts what happened on one exchange.
type Observed struct {
	// Received is how many times the message arrived: deliveries to a waiting element
	// and instances it started.
	Received int `json:"received"`
	// Started is how many of those started an instance.
	Started int `json:"started"`
	// LastAt is when it last arrived.
	LastAt string `json:"lastAt"`
}

// Unreceived counts the sends of the center that reached nobody.
type Unreceived struct {
	From    string `json:"from"`
	Message string `json:"message"`
	Count   int    `json:"count"`
	LastAt  string `json:"lastAt"`
}

// Port is a message name at the center's edge (ADR-0559 §6).
type Port struct {
	Direction string   `json:"direction"` // "out" or "in"
	Message   string   `json:"message"`
	Elements  []string `json:"elements"`
	Roles     []string `json:"roles"`
}

// Window says what the observed layer read.
type Window struct {
	From string `json:"from"`
	To   string `json:"to"`
	// ReadFrom is how far back the read is complete. It equals From unless the scan
	// was cut, when it is the time of the oldest row read.
	ReadFrom string `json:"readFrom"`
	Rows     int    `json:"rows"`
	Partial  bool   `json:"partial"`
}

// ErrUnknownProcess is Build's answer for a process with no deployed definition the
// caller may read.
var ErrUnknownProcess = errors.New("no deployed definition of that process")

var errStop = errors.New("stop")

// Build computes the graph around in.ProcessID.
func Build(in Input) (Graph, error) {
	b, center, err := newBuilder(in)
	if err != nil {
		return Graph{}, err
	}
	b.possible(center)
	window, err := b.observe()
	if err != nil {
		return Graph{}, err
	}
	return b.graph(center, window), nil
}

// newBuilder indexes the definitions and places the center, the newest version of
// in.ProcessID, as the first participant.
func newBuilder(in Input) (*builder, Definition, error) {
	b := &builder{in: in, participants: map[string]*Participant{}, exchanges: map[exchangeKey]*Exchange{},
		unreceived: map[unreceivedKey]*Unreceived{}, centerKeys: map[uint64]bool{}, byKey: map[uint64]Definition{}}
	center, ok := b.newest(in.ProcessID)
	if !ok {
		return nil, Definition{}, ErrUnknownProcess
	}
	for _, d := range in.Definitions {
		b.byKey[d.Key] = d
		if d.ProcessID == in.ProcessID {
			b.centerKeys[d.Key] = true
		}
	}
	b.centerID = ProcessID(in.ProcessID)
	b.process(center).Center = true
	return b, center, nil
}

type exchangeKey struct{ from, to, message string }
type unreceivedKey struct{ from, message string }

type builder struct {
	in           Input
	centerID     string
	centerKeys   map[uint64]bool
	byKey        map[uint64]Definition
	participants map[string]*Participant
	order        []string
	exchanges    map[exchangeKey]*Exchange
	unreceived   map[unreceivedKey]*Unreceived
}

// newest is the newest deployed version of a process.
func (b *builder) newest(processID string) (Definition, bool) {
	for _, d := range b.in.Definitions {
		if d.ProcessID == processID && d.Newest {
			return d, true
		}
	}
	return Definition{}, false
}

func (b *builder) add(p Participant) *Participant {
	if cur, ok := b.participants[p.ID]; ok {
		return cur
	}
	b.participants[p.ID] = &p
	b.order = append(b.order, p.ID)
	return &p
}

// process is the participant of a process, described by its newest version when it
// has one deployed, else by the definition at hand.
func (b *builder) process(d Definition) *Participant {
	if n, ok := b.newest(d.ProcessID); ok {
		d = n
	}
	return b.add(Participant{
		ID: ProcessID(d.ProcessID), Kind: KindProcess, Name: d.Name, ProcessID: d.ProcessID,
		Version: d.Version, DefinitionKey: d.Key, ProjectID: d.ProjectID, System: d.System, Inactive: d.Inactive,
	})
}

func (b *builder) exchange(from, to, message string) *Exchange {
	k := exchangeKey{from, to, message}
	if e, ok := b.exchanges[k]; ok {
		return e
	}
	e := &Exchange{From: from, To: to, Message: message}
	b.exchanges[k] = e
	return e
}

// possible adds what the newest, active definitions and the enabled sources make
// possible around center: whoever waits for a name center sends, and whoever sends a
// name center waits for (ADR-0559 §3). Center itself counts, so a process that sends
// a name it also waits for is drawn with an exchange to itself.
func (b *builder) possible(center Definition) {
	sends, receives := map[string]bool{}, map[string]bool{}
	for _, p := range center.Points {
		if p.Sends() {
			sends[p.Message] = true
		} else {
			receives[p.Message] = true
		}
	}
	if center.Inactive {
		// A deactivated process starts nothing; it still has running instances, but
		// "possible" is read from the newest, active definitions, as ADR-0446's
		// findings read it. Its ports still say what it would exchange.
		return
	}
	for _, d := range b.in.Definitions {
		if !d.Newest || d.Inactive {
			continue
		}
		for _, p := range d.Points {
			switch {
			case !p.Sends() && sends[p.Message]:
				b.exchange(b.centerID, b.process(d).ID, p.Message).Possible = true
			case p.Sends() && receives[p.Message]:
				b.exchange(b.process(d).ID, b.centerID, p.Message).Possible = true
			}
		}
	}
	for _, s := range b.in.Sources {
		if !s.Enabled || !receives[s.Message] {
			continue
		}
		var p *Participant
		switch s.Kind {
		case KindInboundWatch:
			p = b.add(Participant{ID: "inbound-watch:" + s.ConnectorID, Kind: KindInboundWatch, Name: s.ConnectorName,
				ConnectorID: s.ConnectorID, WorkerKind: s.WorkerKind})
		case KindProductAction:
			p = b.add(Participant{ID: "product-action:" + s.ProductID + ":" + s.Action, Kind: KindProductAction,
				Name: s.ProductName, ProductID: s.ProductID, Action: s.Action})
		default:
			continue
		}
		b.exchange(p.ID, b.centerID, s.Message).Possible = true
	}
}

// readable reports whether the caller may read a row about the definition defKey.
func (b *builder) readable(defKey uint64) bool {
	if b.in.ReadsAll {
		return true
	}
	return defKey != 0 && b.in.Visible != nil && b.in.Visible(defKey)
}

// party is the participant a definition key names in a row the caller may read.
func (b *builder) party(defKey uint64) *Participant {
	d, ok := b.byKey[defKey]
	if !ok {
		return b.add(Participant{ID: idRemoved, Kind: KindRemoved})
	}
	return b.process(d)
}

func (b *builder) outside() *Participant {
	return b.add(Participant{ID: idOutsideView, Kind: KindOutsideView})
}

// observe reads the event log of the window and folds what concerns the center.
func (b *builder) observe() (Window, error) {
	w := Window{From: stamp(b.in.From), To: stamp(b.in.Now), ReadFrom: stamp(b.in.From)}
	if b.in.Scan == nil {
		return w, nil
	}
	var (
		group     []state.EventLogEntry
		cause     uint64
		oldestRow int64
	)
	flush := func() {
		if len(group) > 0 {
			b.fold(group)
		}
		group = group[:0]
	}
	err := b.in.Scan(func(e state.EventLogEntry) error {
		if e.At < b.in.From {
			return errStop
		}
		if w.Rows >= b.in.ScanLimit {
			w.Partial = true
			return errStop
		}
		w.Rows++
		oldestRow = e.At
		// The rows of one command share its cause and lie next to each other on the
		// log, so a group ends where the cause changes.
		if len(group) > 0 && e.Cause != cause {
			flush()
		}
		cause = e.Cause
		group = append(group, e)
		return nil
	})
	if err != nil && !errors.Is(err, errStop) {
		return Window{}, err
	}
	flush()
	if w.Partial {
		// The group at the cut may be missing rows older than the cut; it was folded
		// with what was read, and ReadFrom says where reading stopped.
		w.ReadFrom = stamp(oldestRow)
	}
	return w, nil
}

// arrival is one fact a command's rows establish about the center, attributed to the
// participants the caller may know: a message that arrived (n times, started of them
// starting an instance) or, with unreceived, a send of the center that reached nobody.
// The graph counts arrivals; the replay lists them (replay.go).
type arrival struct {
	at         int64
	position   uint64
	from, to   string
	message    string
	n, started int
	unreceived bool
	// outside marks the count of a send's arrivals outside the reader's view.
	outside bool
	key     string
}

// fold adds one command's message rows to the graph.
func (b *builder) fold(group []state.EventLogEntry) {
	for _, a := range b.attribute(group) {
		if a.unreceived {
			k := unreceivedKey{a.from, a.message}
			u, ok := b.unreceived[k]
			if !ok {
				u = &Unreceived{From: a.from, Message: a.message, LastAt: stamp(a.at)}
				b.unreceived[k] = u
			}
			u.Count++
			continue
		}
		e := b.exchange(a.from, a.to, a.message)
		if e.Observed == nil {
			// The graph reads the log newest first, so the first arrival counted is the
			// last one.
			e.Observed = &Observed{LastAt: stamp(a.at)}
		}
		e.Observed.Received += a.n
		e.Observed.Started += a.started
	}
}

// attribute reads one command's message rows — its send, if any, and what it reached
// — and answers what they establish about the center, in the order the rows hold.
//
// Two rules decide what the caller learns, both ADR-0472 §5's:
//   - A reception is attributed to a sender only when the caller may read the send row;
//     otherwise it comes from outside their view.
//   - What a readable send of the center reached beyond the receptions the caller may
//     read is counted as received outside their view, from the send row's own counts.
func (b *builder) attribute(group []state.EventLogEntry) []arrival {
	var send *state.EventLogEntry
	var receptions []state.EventLogEntry
	for i := range group {
		e := group[i]
		switch {
		case e.Kind == state.EventLogDispatched && e.Dispatch != nil && e.Dispatch.Channel == model.DispatchMessage:
			send = &group[i]
		case e.Kind == state.EventLogMessageDelivered && e.Flow != nil:
			receptions = append(receptions, e)
		}
	}
	if send == nil && len(receptions) == 0 {
		return nil
	}

	// Who sent it, as far as the caller may know.
	var from *Participant
	fromCenter := false
	if send != nil {
		d := send.Dispatch
		switch d.Source {
		case model.DispatchFromProcess:
			if b.readable(d.SenderProcessDefKey) {
				from = b.lazyParty(d.SenderProcessDefKey)
				fromCenter = b.centerKeys[d.SenderProcessDefKey]
			}
		case model.DispatchFromPublish:
			if b.in.ReadsAll {
				from = b.lazy(Participant{ID: idPublish, Kind: KindPublish})
			}
		case model.DispatchFromInbound:
			if b.in.ReadsAll {
				from = b.lazy(Participant{ID: idInbound, Kind: KindInbound})
			}
		case model.DispatchDirected:
			if b.readable(d.TargetProcessDefKey) {
				from = b.lazy(Participant{ID: idDirected, Kind: KindDirected})
			}
		}
	} else if receptions[0].Flow.SenderProcessInstanceKey == 0 {
		// A reception with no send in its command is a kept message handed to a
		// subscription that opened later (ADR-0472 §1), and only a publish with a time
		// to live is kept.
		if b.in.ReadsAll {
			from = b.lazy(Participant{ID: idPublish, Kind: KindPublish})
		}
	}

	var out []arrival
	readReceived, readStarted := 0, 0
	for _, r := range receptions {
		fl := r.Flow
		if !b.readable(fl.ReceiverProcessDefKey) {
			continue
		}
		toCenter := b.centerKeys[fl.ReceiverProcessDefKey]
		started := 0
		if fl.ReceiverProcessInstanceKey == 0 {
			started = 1
		}
		readReceived++
		readStarted += started
		if !fromCenter && !toCenter {
			continue // depth one: an exchange that does not touch the center
		}
		src := from
		if src == nil {
			src = b.outside()
		} else {
			src = b.materialize(src)
		}
		to := b.party(fl.ReceiverProcessDefKey)
		out = append(out, arrival{at: r.At, position: r.Position, from: src.ID, to: to.ID, message: fl.MessageName,
			n: 1, started: started, key: fl.CorrelationKey})
	}

	if send == nil || !fromCenter {
		return out
	}
	d := send.Dispatch
	// What the send reached beyond what the caller may read is received outside their
	// view: the send row says how many, not where.
	if hidden := int(d.Reached) + int(d.Started) - readReceived; hidden > 0 {
		out = append(out, arrival{at: send.At, position: send.Position, from: b.centerID, to: b.outside().ID,
			message: d.Name, n: hidden, started: max(int(d.Started)-readStarted, 0), outside: true, key: d.CorrelationKey})
	}
	if d.Reached == 0 && d.Started == 0 {
		out = append(out, arrival{at: send.At, position: send.Position, from: b.centerID, message: d.Name,
			unreceived: true, key: d.CorrelationKey})
	}
	return out
}

// lazy and lazyParty describe a participant without adding it yet: a sender is only
// part of the graph when one of its receptions is, so an unrelated publish read in
// passing does not appear as a participant.
func (b *builder) lazy(p Participant) *Participant { return &p }

func (b *builder) lazyParty(defKey uint64) *Participant {
	d, ok := b.byKey[defKey]
	if !ok {
		return &Participant{ID: idRemoved, Kind: KindRemoved}
	}
	if n, ok := b.newest(d.ProcessID); ok {
		d = n
	}
	return &Participant{ID: ProcessID(d.ProcessID), Kind: KindProcess, Name: d.Name, ProcessID: d.ProcessID,
		Version: d.Version, DefinitionKey: d.Key, ProjectID: d.ProjectID, System: d.System, Inactive: d.Inactive}
}

func (b *builder) materialize(p *Participant) *Participant { return b.add(*p) }

// graph is the answer in a stable order: the center first, then participants as they
// were met; exchanges by sender, receiver and name.
func (b *builder) graph(center Definition, w Window) Graph {
	g := Graph{ProcessID: center.ProcessID, Participants: []Participant{}, Exchanges: []Exchange{},
		Unreceived: []Unreceived{}, Ports: ports(center), Observed: w}
	g.Participants = b.participantList()
	for _, e := range b.exchanges {
		g.Exchanges = append(g.Exchanges, *e)
	}
	sort.Slice(g.Exchanges, func(i, j int) bool {
		a, c := g.Exchanges[i], g.Exchanges[j]
		if a.From != c.From {
			return a.From < c.From
		}
		if a.To != c.To {
			return a.To < c.To
		}
		return a.Message < c.Message
	})
	for _, u := range b.unreceived {
		g.Unreceived = append(g.Unreceived, *u)
	}
	sort.Slice(g.Unreceived, func(i, j int) bool { return g.Unreceived[i].Message < g.Unreceived[j].Message })
	return g
}

// participantList is the participants met, the center first and the others in the
// order they were met.
func (b *builder) participantList() []Participant {
	out := make([]Participant, 0, len(b.order))
	for _, id := range b.order {
		out = append(out, *b.participants[id])
	}
	return out
}

// ports are the center's message names, outgoing first, each with the elements
// behind it.
func ports(d Definition) []Port {
	type key struct{ dir, msg string }
	by := map[key]*Port{}
	var keys []key
	for _, p := range d.Points {
		k := key{"in", p.Message}
		if p.Sends() {
			k.dir = "out"
		}
		pt, ok := by[k]
		if !ok {
			pt = &Port{Direction: k.dir, Message: p.Message, Elements: []string{}, Roles: []string{}}
			by[k] = pt
			keys = append(keys, k)
		}
		pt.Elements = append(pt.Elements, p.Element)
		pt.Roles = append(pt.Roles, p.Role)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].dir != keys[j].dir {
			return keys[i].dir == "out"
		}
		return keys[i].msg < keys[j].msg
	})
	out := make([]Port, 0, len(keys))
	for _, k := range keys {
		out = append(out, *by[k])
	}
	return out
}

func stamp(ns int64) string { return time.Unix(0, ns).UTC().Format(time.RFC3339Nano) }
