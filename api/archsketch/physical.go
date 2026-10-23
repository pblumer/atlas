package archsketch

import (
	"cmp"
	"net"
	"net/url"
	"slices"
	"strings"
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

// ParseEndpoint reduces a configured endpoint to scheme, host and port. It accepts
// the shapes the Worker Types store — a URL, "host:port", or a bare host — and returns
// nil for an empty value.
func ParseEndpoint(raw string) *Endpoint {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" || !validHost(u.Hostname()) {
			return &Endpoint{Unparseable: true}
		}
		return &Endpoint{Scheme: strings.ToLower(u.Scheme), Host: strings.ToLower(u.Hostname()), Port: u.Port()}
	}
	if i := strings.IndexAny(raw, "/?#"); i >= 0 {
		raw = raw[:i]
	}
	if i := strings.LastIndex(raw, "@"); i >= 0 {
		raw = raw[i+1:]
	}
	host, port := raw, ""
	if h, p, err := net.SplitHostPort(raw); err == nil {
		host, port = h, p
	}
	if !validHost(host) {
		return &Endpoint{Unparseable: true}
	}
	return &Endpoint{Host: strings.ToLower(host), Port: port}
}

// validHost refuses what cannot be a host name or an address, so a value that only
// looks parsed — a sentence, a credential pasted into the wrong field — is reported as
// unparseable instead of being drawn as a machine.
func validHost(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == ':', r == '_':
		default:
			return false
		}
	}
	return true
}

// String renders the endpoint as a reader writes an address.
func (e Endpoint) String() string {
	switch {
	case e.Withheld:
		return "endpoint withheld from this reader"
	case e.Unparseable:
		return "endpoint not readable as an address"
	}
	host := e.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if e.Port != "" {
		host += ":" + e.Port
	}
	if e.Scheme != "" {
		return e.Scheme + "://" + host
	}
	return host
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
	Observers
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
		if !mayViewWorker(w.ConfiguredID) {
			w.Endpoint = withheld(w.Endpoint)
			w.MailboxEndpoint = withheld(w.MailboxEndpoint)
		}
		w.Observers = w.Observers.redacted(mayViewInstances)
		out.Workers[i] = w
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
	case w.Endpoint != nil:
		return "→ " + w.Endpoint.String()
	case w.ConfiguredID == "" && w.Placement == PlacementWorker:
		return "endpoint held by the worker itself"
	default:
		return "no endpoint in its configuration here"
	}
}
