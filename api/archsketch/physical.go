package archsketch

import (
	"cmp"
	"slices"
	"strings"

	"github.com/pblumer/atlas/internal/endpoint"
)

// The physical part of a snapshot (ADR-0471 §1): what the installation that recorded
// the release had in place for it at that moment. Unlike the logical part it differs
// between installations — DEV and PROD record the same release with different
// workers, endpoints and addresses — and it is a moment, not a live view: an endpoint
// changed after the release was recorded is not in it.
//
// Three rules shape it:
//
//   - **An endpoint is scheme, host and port, and nothing else.** [ParseEndpoint]
//     rebuilds it from those parts, so user information, a path, a query or a fragment
//     in the configured value cannot reach a snapshot at all, rather than being
//     trimmed off it later.
//   - **Unknown is not none.** A worker that holds its own configuration (ADR-0168)
//     tells the server only the names it holds, and the server forgets even that on
//     a restart. So "no worker reported this" is recorded as exactly that, never as
//     "nothing serves this".
//   - **What a reader may see is decided when it is read.** The snapshot keeps what
//     the server knew; [Physical.Redacted] withholds an endpoint from a reader who may
//     not see that worker's configuration (ADR-0205) and the worker instances from a
//     reader who may not see the Workers view, and says it did.

// Endpoint is one address, reduced to the three parts a reader of an architecture
// sketch needs.
type Endpoint struct {
	Scheme string `json:"scheme,omitempty"`
	Host   string `json:"host,omitempty"`
	// Port is the port the configured value named. Empty when it named none, which
	// means the scheme's or the Worker Type's default — the snapshot does not guess
	// which.
	Port string `json:"port,omitempty"`
	// Unparseable is set when the configured value could not be read as an address.
	// The value itself is not kept: what cannot be parsed cannot be cleaned either.
	Unparseable bool `json:"unparseable,omitempty"`
	// Withheld is set on a read for a reader who may not see it. Never stored.
	Withheld bool `json:"withheld,omitempty"`
}

// ParseEndpoint reduces a configured endpoint to scheme, host and port
// (internal/endpoint), and returns nil for an empty value. A value that cannot be an
// address is recorded as unparseable and not kept.
func ParseEndpoint(raw string) *Endpoint {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	a, ok := endpoint.Reduce(raw)
	if !ok {
		return &Endpoint{Unparseable: true}
	}
	return &Endpoint{Scheme: a.Scheme, Host: a.Host, Port: a.Port}
}

// String renders the endpoint as a reader writes an address.
func (e Endpoint) String() string {
	switch {
	case e.Withheld:
		return "endpoint withheld from this reader"
	case e.Unparseable:
		return "endpoint not readable as an address"
	}
	return endpoint.Address{Scheme: e.Scheme, Host: e.Host, Port: e.Port}.String()
}

// Placements: who opens the connection to a worker's target system.
const (
	// PlacementEngine: the Atlas server runs the Worker Type itself and connects to the
	// target directly. Deprecated for side-effecting work (ADR-0164), and still how
	// some installations run.
	PlacementEngine = "engine"
	// PlacementWorker: a worker process leases the work from Atlas's API and connects
	// to the target — two connections, opened from the worker's side both times.
	PlacementWorker = "worker"
)

// Physical is the installation's half of a snapshot.
type Physical struct {
	// Node and Environment are the recording installation's operator-given name and
	// environment (ADR-0189 §6). Empty where nobody named them.
	Node        string `json:"node,omitempty"`
	Environment string `json:"environment,omitempty"`
	// ExternalURL is the origin the installation was configured to be reached under.
	// Nil when none was configured: the listen address is not the reachable address,
	// so it is not offered in its place.
	ExternalURL *Endpoint `json:"externalUrl,omitempty"`
	// PublicStartLinks counts the public start links each process had here. The links
	// themselves are their own authorization and are never recorded.
	PublicStartLinks []StartLinks  `json:"publicStartLinks"`
	Workers          []WorkerSite  `json:"workers"`
	JobTypes         []JobTypeSite `json:"jobTypes"`
	// Inbound are the inbound subscriptions that turned an external system's events
	// into a message the release receives (format version 4, ADR-0495 slice B). A
	// disabled subscription feeds nothing and is not recorded.
	Inbound []InboundSite `json:"inbound,omitempty"`
}

// InboundSite is one inbound subscription (ADR-0205): a configured Worker watches a
// subject on the system it reaches and republishes each new event there as a message
// this release receives. Atlas opens that connection itself.
type InboundSite struct {
	MessageName string `json:"messageName"`
	// Worker and WorkerType name the configured Worker the subscription reads
	// through; empty when the Worker had been deleted since, which leaves the
	// subscription with nowhere to read from.
	Worker     string `json:"worker,omitempty"`
	WorkerType string `json:"workerType,omitempty"`
	// ConfiguredID is that Worker's id, kept so a read can ask who may see the
	// subscription's subject and endpoint: both are the Worker's configuration.
	ConfiguredID string `json:"configuredId,omitempty"`
	// Subject is what the subscription watches, in the terms of the Worker's kind: a
	// clio subject (Recursive when it takes the subtree too), a Jira query, a
	// spreadsheet, a folder, a channel or a mail folder.
	Subject   string    `json:"subject,omitempty"`
	Recursive bool      `json:"recursive,omitempty"`
	Endpoint  *Endpoint `json:"endpoint,omitempty"`
	// Withheld is set on a read by somebody who may not see the Worker's
	// configuration: the subscription is still there, its subject and endpoint are not.
	Withheld bool `json:"withheld,omitempty"`
}

// WorkerIdentity is which identity a Worker's actions run as (ADR-0475): the service's
// own, a fixed delegated connection, or the person who started each instance; and
// whether the policy decision beside it is enforced or only recorded.
type WorkerIdentity struct {
	Mode        string `json:"mode"`
	Enforcement string `json:"enforcement,omitempty"`
}

// text names the identity the way a reader says it.
func (i WorkerIdentity) text() string {
	mode := map[string]string{
		"service":           "service identity",
		"delegated-fixed":   "delegated, fixed connection",
		"delegated-per-job": "delegated per instance",
	}[i.Mode]
	if mode == "" {
		mode = "identity " + i.Mode
	}
	if i.Enforcement == "shadow" {
		mode += " (policy recorded, not enforced)"
	}
	return mode
}

// StartLinks is how many public start links one process had.
type StartLinks struct {
	ProcessID string `json:"processId"`
	Links     int    `json:"links"`
}

// WorkerSite is what the installation had in place for one worker name.
type WorkerSite struct {
	Name       string `json:"name"`
	WorkerType string `json:"workerType,omitempty"`
	Placement  string `json:"placement"`
	// ConfiguredID is the configured worker the installation held under this name, and
	// empty when it held none — the case of a worker that keeps its own configuration
	// (ADR-0168). Kept so a read can ask who may see its endpoint.
	ConfiguredID    string    `json:"configuredId,omitempty"`
	Endpoint        *Endpoint `json:"endpoint,omitempty"`
	MailboxEndpoint *Endpoint `json:"mailboxEndpoint,omitempty"`
	// EndpointReported is set when Endpoint is not the installation's configuration
	// but what the worker instances serving the name reported (format version 3).
	// It is then information about those instances, and a read treats it as such.
	EndpointReported bool `json:"endpointReported,omitempty"`
	// EndpointsDiffer is set when the instances serving the name reported different
	// endpoints. None of them is recorded then: picking one would draw a single
	// system where the installation reaches several.
	EndpointsDiffer bool `json:"endpointsDiffer,omitempty"`
	// The rest of the configured Worker, from format version 4 (ADR-0495 slice B),
	// none of it a secret: the mail transport, the malware scanner a mail Worker hands
	// messages to, the model an agent Worker asks (ADR-0255), the identity its actions
	// run as, and whether a credential is configured — never which one.
	Provider        string          `json:"provider,omitempty"`
	ScannerEndpoint *Endpoint       `json:"scannerEndpoint,omitempty"`
	Model           string          `json:"model,omitempty"`
	Identity        *WorkerIdentity `json:"identity,omitempty"`
	Credential      bool            `json:"credential,omitempty"`
	// DetailWithheld is set on a read by somebody who may not see the Worker's
	// configuration and the snapshot held some of it.
	DetailWithheld bool `json:"detailWithheld,omitempty"`
	Observers
}

// detailText is the fourth line of a worker's box: what else its configuration says.
// Empty when it says nothing more, so a box only grows for a Worker that has more to
// say.
func (w WorkerSite) detailText() string {
	if w.DetailWithheld {
		return "configuration withheld from this reader"
	}
	var parts []string
	if w.Identity != nil {
		parts = append(parts, w.Identity.text())
	}
	if w.Credential {
		parts = append(parts, "credential configured")
	}
	if w.Model != "" {
		parts = append(parts, "model "+w.Model)
	}
	if w.Provider != "" {
		parts = append(parts, "via "+w.Provider)
	}
	if w.MailboxEndpoint != nil {
		parts = append(parts, "mailbox "+w.MailboxEndpoint.String())
	}
	if w.ScannerEndpoint != nil {
		parts = append(parts, "scanner "+w.ScannerEndpoint.String())
	}
	return strings.Join(parts, " · ")
}

// hasDetail reports whether the site carries any of the format-version-4 detail. The
// mailbox endpoint is older (version 2) and keeps the endpoint's own rule.
func (w WorkerSite) hasDetail() bool {
	return w.Provider != "" || w.ScannerEndpoint != nil || w.Model != "" || w.Identity != nil || w.Credential
}

// inboundSubscriptions is the recorded inbound subscriptions, none for a snapshot
// without a physical part.
func (p *Physical) inboundSubscriptions() []InboundSite {
	if p == nil {
		return nil
	}
	return p.Inbound
}

// inboundFor is the inbound subscriptions that publish one message name.
func (p *Physical) inboundFor(message string) []InboundSite {
	if p == nil {
		return nil
	}
	var out []InboundSite
	for _, in := range p.Inbound {
		if in.MessageName == message {
			out = append(out, in)
		}
	}
	return out
}

// sourceText says where an inbound subscription reads from, as far as this reader may
// know it.
func (in InboundSite) sourceText() string {
	worker := in.Worker
	if worker == "" {
		worker = "a deleted worker"
	}
	switch {
	case in.Withheld:
		return worker + " (subject withheld from this reader)"
	case in.Subject == "":
		return worker
	}
	subject := in.Subject
	if in.Recursive {
		subject += "/…"
	}
	return worker + " · " + subject
}

// JobTypeSite is what the installation had seen serve one job type.
type JobTypeSite struct {
	JobType string `json:"jobType"`
	Observers
}

// Observers are the worker instances that reported serving something since the
// installation started. Observed is their number, kept beside the ids so a read that
// withholds the ids still says how many there were.
type Observers struct {
	ObservedBy        []string `json:"observedBy,omitempty"`
	Observed          int      `json:"observed"`
	ObserversWithheld bool     `json:"observersWithheld,omitempty"`
}

func (o Observers) text() string {
	switch {
	case o.Observed == 0:
		return "no worker seen since the server started"
	case o.ObserversWithheld:
		return "seen: " + plural(o.Observed, "worker", "workers")
	}
	return "seen: " + strings.Join(o.ObservedBy, ", ")
}

func (o Observers) redacted(mayView bool) Observers {
	o.Observed = max(o.Observed, len(o.ObservedBy))
	if !mayView && len(o.ObservedBy) > 0 {
		o.ObservedBy, o.ObserversWithheld = nil, true
	}
	return o
}

func (p *Physical) normalize() {
	slices.SortFunc(p.PublicStartLinks, func(a, b StartLinks) int { return cmp.Compare(a.ProcessID, b.ProcessID) })
	slices.SortFunc(p.Workers, func(a, b WorkerSite) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.WorkerType, b.WorkerType))
	})
	slices.SortFunc(p.JobTypes, func(a, b JobTypeSite) int { return cmp.Compare(a.JobType, b.JobType) })
	slices.SortFunc(p.Inbound, func(a, b InboundSite) int {
		return cmp.Or(cmp.Compare(a.MessageName, b.MessageName), cmp.Compare(a.Worker, b.Worker), cmp.Compare(a.Subject, b.Subject))
	})
	for i := range p.Workers {
		slices.Sort(p.Workers[i].ObservedBy)
		p.Workers[i].Observed = len(p.Workers[i].ObservedBy)
	}
	for i := range p.JobTypes {
		slices.Sort(p.JobTypes[i].ObservedBy)
		p.JobTypes[i].Observed = len(p.JobTypes[i].ObservedBy)
	}
}

// Redacted is the physical part as one reader may see it. mayViewWorker answers for a
// configured worker's endpoint by the worker's id; mayViewInstances answers for the
// worker instances, which are what the Workers view shows. The receiver is not
// changed.
func (p Physical) Redacted(mayViewWorker func(configuredID string) bool, mayViewInstances bool) Physical {
	out := p
	out.Workers = make([]WorkerSite, len(p.Workers))
	for i, w := range p.Workers {
		// A reported endpoint is what worker instances said, and goes to whoever may
		// see the instances; a configured one is the worker's configuration, and goes
		// to whoever may see that.
		mayView := mayViewWorker(w.ConfiguredID)
		if w.EndpointReported {
			mayView = mayViewInstances
		}
		if !mayView {
			w.Endpoint = withheld(w.Endpoint)
			w.MailboxEndpoint = withheld(w.MailboxEndpoint)
		}
		if !mayViewWorker(w.ConfiguredID) && w.hasDetail() {
			w.Provider, w.ScannerEndpoint, w.Model, w.Identity, w.Credential = "", nil, "", nil, false
			w.DetailWithheld = true
		}
		w.Observers = w.Observers.redacted(mayViewInstances)
		out.Workers[i] = w
	}
	if p.Inbound != nil {
		out.Inbound = make([]InboundSite, len(p.Inbound))
		for i, in := range p.Inbound {
			if !mayViewWorker(in.ConfiguredID) {
				in.Subject, in.Recursive, in.Endpoint, in.Withheld = "", false, nil, true
			}
			out.Inbound[i] = in
		}
	}
	out.JobTypes = make([]JobTypeSite, len(p.JobTypes))
	for i, j := range p.JobTypes {
		j.Observers = j.Observers.redacted(mayViewInstances)
		out.JobTypes[i] = j
	}
	return out
}

func withheld(e *Endpoint) *Endpoint {
	if e == nil {
		return nil
	}
	return &Endpoint{Withheld: true}
}

// worker returns the site recorded for a worker name, or nil.
func (p *Physical) worker(name, workerType string) *WorkerSite {
	if p == nil {
		return nil
	}
	for i := range p.Workers {
		if p.Workers[i].Name == name && p.Workers[i].WorkerType == workerType {
			return &p.Workers[i]
		}
	}
	return nil
}

// jobType returns the site recorded for a job type, or nil.
func (p *Physical) jobType(jobType string) *JobTypeSite {
	if p == nil {
		return nil
	}
	for i := range p.JobTypes {
		if p.JobTypes[i].JobType == jobType {
			return &p.JobTypes[i]
		}
	}
	return nil
}

// siteText is the third line of a worker's box: where its work goes.
func (w WorkerSite) siteText() string {
	switch {
	case w.Endpoint != nil && w.EndpointReported && !w.Endpoint.Withheld:
		return "→ " + w.Endpoint.String() + " (reported)"
	case w.Endpoint != nil:
		return "→ " + w.Endpoint.String()
	case w.EndpointsDiffer:
		return "workers report different endpoints"
	case w.ConfiguredID == "" && w.Placement == PlacementWorker:
		return "endpoint held by the worker itself"
	default:
		return "no endpoint in its configuration here"
	}
}
