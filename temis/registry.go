// Package temis integrates a central temis decision service as a server-registered
// Atlas connector: a business rule task marked <atlas:temisConnector> delegates its
// decision to a configured temis instance through the job path (ADR-0050), instead
// of the embedded temis library that evaluates a local decision (ADR-0014). It
// mirrors the clio connector (ADR-0036) and reuses the shared decision I/O core
// (dmn.DecisionHandler, ADR-0039), so a central and a local decision differ only in
// where they are evaluated:
//
//   - A central business rule task creates a job carrying the reserved
//     [compiler.TemisDecisionJobType]. The processor never calls temis itself, so
//     it stays allocation-free (invariant I1) and free of any HTTP dependency.
//   - The in-process [Handler] — a job worker — pulls those jobs, evaluates the
//     decision on the remote temis service off the processor goroutine and after
//     fsync (invariant I2, never inside applyToState / I4), and completes the job
//     with the result written back as the resultVariable process variable.
//   - The temis endpoint and credentials live in a server-side [Registry] keyed by
//     connector name, so a model refers to a connector by name only and never
//     carries a URL or secret (ADR-0036/0041).
//
// Evaluation is at-least-once (a crash between "temis answered" and "job completed"
// re-evaluates); DMN evaluation is pure, so a re-evaluation is harmless and the
// result is frozen into the completion event on replay (invariant I6).
package temis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Client evaluates a decision on one temis instance. It is an interface so the
// worker is testable without a live temis and so a connector name binds to exactly
// one endpoint. Outputs are the decision's named results (decision/output name →
// value), the same shape the local registry returns.
type Client interface {
	Evaluate(ctx context.Context, decisionId string, inputs map[string]any) (map[string]any, error)
}

// Registry resolves a connector name to the [Client] for its temis instance.
// Connectors are registered at the server from configuration (endpoint plus
// credentials), so a model refers to a connector by name only (ADR-0036/0041). A
// Registry is read-only once populated and safe for concurrent use by workers.
type Registry struct {
	clients map[string]Client
}

// NewRegistry creates an empty connector registry.
func NewRegistry() *Registry { return &Registry{clients: map[string]Client{}} }

// Register binds a connector name to its client. Registering the same name again
// replaces the earlier binding (last write wins), so reconfiguration is simple.
// Populate the registry before the processes that use it start running.
func (r *Registry) Register(name string, c Client) { r.clients[name] = c }

// Client returns the client bound to name, or nil and false if none is registered.
func (r *Registry) Client(name string) (Client, bool) {
	c, ok := r.clients[name]
	return c, ok
}

// Replace swaps the whole set of registered connectors at once, so a server can
// rebuild the registry from managed configuration after a change (ADR-0041). The
// caller must serialize Replace with the workers that read the registry — the
// Atlas server does both on its run-loop goroutine — so no lock is needed. A nil
// map clears the registry.
func (r *Registry) Replace(clients map[string]Client) {
	if clients == nil {
		clients = map[string]Client{}
	}
	r.clients = clients
}

// Connector is the server-side configuration of one temis connector: the base
// endpoint of the temis instance and an optional bearer token for it. Per
// ADR-0041 the token is the output of a secret resolver, not a value typed into a
// model.
type Connector struct {
	Endpoint string
	Token    string
}

// HTTPClient talks to a real temis instance over HTTP.
//
// The wire format is provisional pending the temis service API contract: it POSTs
// {"decisionId":…, "inputs":{…}} as JSON to {Endpoint}/evaluate and expects
// {"outputs":{…}} back. Swap the path/shape here when the contract is fixed;
// nothing outside this method depends on it.
type HTTPClient struct {
	conn Connector
	http *http.Client
}

// NewHTTPClient builds a temis HTTP client for a configured connector.
func NewHTTPClient(conn Connector) *HTTPClient {
	return &HTTPClient{conn: conn, http: http.DefaultClient}
}

// Evaluate posts the decision id and input context to the temis service and
// returns its outputs. A non-2xx response or a transport failure is an error,
// leaving the job pending for a later retry.
func (c *HTTPClient) Evaluate(ctx context.Context, decisionId string, inputs map[string]any) (map[string]any, error) {
	body, err := json.Marshal(map[string]any{"decisionId": decisionId, "inputs": inputs})
	if err != nil {
		return nil, fmt.Errorf("temis: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.conn.Endpoint+"/evaluate", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("temis: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.conn.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.conn.Token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("temis: evaluate %q: %w", decisionId, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("temis: evaluate %q returned HTTP %d", decisionId, resp.StatusCode)
	}
	// Decode with UseNumber so decision outputs keep their exact decimal text
	// (dmn.outputVariable canonicalizes json.Number losslessly).
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	var out struct {
		Outputs map[string]any `json:"outputs"`
	}
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("temis: decode response for %q: %w", decisionId, err)
	}
	return out.Outputs, nil
}
