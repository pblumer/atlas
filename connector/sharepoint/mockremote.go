package sharepoint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/pblumer/atlas/connector/nettimeout"
	"github.com/pblumer/atlas/limits"
)

// MockAnswer is what the server's mockup route answers a forwarded operation with:
// what the mockup answered, or why it refused. A refusal is a 422 with Error set; every
// other failure is the route's, and says so in its status.
type MockAnswer struct {
	Answer any    `json:"answer,omitempty"`
	Error  string `json:"error,omitempty"`
}

// RemoteMock is the [Client] a worker in mockup mode holds for a Worker: it forwards each
// operation to the server's mockup of that Worker and returns what the mockup answered.
// The worker keeps no site of its own, so what one run wrote, the next run — on this
// worker after a restart, on another, or in the engine — reads.
type RemoteMock struct {
	url    string
	token  string
	client *http.Client
}

// NewRemoteMock creates the forwarder for one Worker. base is the server's mockup
// route, which the server hands a supervised worker as ATLAS_SHAREPOINT_MOCK_URL; token
// is the worker's own API token.
func NewRemoteMock(base, worker, token string) *RemoteMock {
	return &RemoteMock{
		url:    strings.TrimRight(strings.TrimSpace(base), "/") + "/" + url.PathEscape(worker),
		token:  strings.TrimSpace(token),
		client: nettimeout.HTTPClient(),
	}
}

// Do forwards one operation. A mockup that is unreachable fails the job, which then
// retries as a Graph outage would: the operation did not happen.
func (r *RemoteMock) Do(ctx context.Context, req Request) (any, error) {
	op := req.Operation
	if op == "" {
		op = OpCreateItem
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("sharepoint: %s: encode the operation for the mockup: %w", op, err)
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("sharepoint: %s: %w", op, err)
	}
	hreq.Header.Set("Content-Type", "application/json")
	if r.token != "" {
		hreq.Header.Set("Authorization", "Bearer "+r.token)
	}
	resp, err := r.client.Do(hreq)
	if err != nil {
		return nil, fmt.Errorf("sharepoint: %s: reach the mockup at %s: %w", op, r.url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	// The Payload budget is above anything a mockup answers with — a download is held
	// to a variable's size before it is encoded — so a longer answer is not one.
	budget := limits.Default().Payload
	raw, err := io.ReadAll(io.LimitReader(resp.Body, budget+1))
	if err != nil {
		return nil, fmt.Errorf("sharepoint: %s: read the mockup's answer: %w", op, err)
	}
	if int64(len(raw)) > budget {
		return nil, fmt.Errorf("sharepoint: %s: the mockup answered with more than %d bytes", op, budget)
	}
	var answer MockAnswer
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	decodeErr := dec.Decode(&answer)
	switch {
	case resp.StatusCode == http.StatusOK && decodeErr == nil:
		return answer.Answer, nil
	case resp.StatusCode == http.StatusUnprocessableEntity && decodeErr == nil && answer.Error != "":
		return nil, errors.New(answer.Error)
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("sharepoint: %s: the server at %s holds no mockup for this Worker — is it still configured with the mockup provider?", op, r.url)
	}
	quote := strings.TrimSpace(string(raw))
	if int64(len(quote)) > limits.Default().ErrorBody {
		quote = quote[:limits.Default().ErrorBody]
	}
	return nil, fmt.Errorf("sharepoint: %s: the mockup at %s answered %s: %s", op, r.url, resp.Status, quote)
}
