package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/model"
)

// What every provider adapter shares, so the two of them cannot drift on the parts that
// are not about a wire format at all: how a credential is presented, how a refusal
// becomes an error an operator can act on, and what an answer with nothing in it means.
//
// The wire formats themselves stay apart — messages.go and chatcompletions.go — because
// that is the only real difference between providers, and a single adapter with a
// dialect switch inside it would hide it.

// Authentication schemes a model endpoint takes. Both are just a header; which one an
// endpoint wants is deployment configuration, not a code path.
const (
	// AuthAPIKey sends the key as Anthropic's own x-api-key header.
	AuthAPIKey = "x-api-key"
	// AuthBearer sends it as Authorization: Bearer, which is what OpenAI, OpenRouter
	// and most gateways in front of either expect — including a gateway that speaks
	// the Messages format but authenticates its own way.
	AuthBearer = "bearer"
)

// applyAuth presents the credential. An empty key sets no header at all: a self-hosted
// endpoint may legitimately need none, and sending an empty credential would turn that
// into a 401 nobody can read.
func applyAuth(h http.Header, scheme, key string) error {
	if key == "" {
		return nil
	}
	switch scheme {
	case "", AuthAPIKey:
		h.Set("x-api-key", key)
	case AuthBearer:
		h.Set("Authorization", "Bearer "+key)
	default:
		return fmt.Errorf("agent: unknown auth scheme %q; use %q or %q", scheme, AuthAPIKey, AuthBearer)
	}
	return nil
}

// postJSON is one round trip to a model endpoint. It is shared because the failure
// half is the part an operator reads: the status is carried into the message because it
// decides what they do about the incident — 401 is a credential, 429 is capacity, 400
// is us — and a provider that reported it its own way would make that unreadable.
func postJSON(ctx context.Context, client *http.Client, timeout time.Duration,
	endpoint string, header func(http.Header) error, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if header != nil {
		if err := header(req.Header); err != nil {
			return nil, err
		}
	}
	if client == nil {
		if timeout == 0 {
			timeout = 2 * time.Minute
		}
		client = &http.Client{Timeout: timeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("model endpoint returned %d: %s", resp.StatusCode, snippet(raw))
	}
	return raw, nil
}

// answerDecision is the end of a round in which the model called nothing: the text it
// wrote becomes the answer variable, and no text at all is a failure rather than an
// ending.
//
// The distinction is the whole reason this is shared. A model that answers with neither
// a tool call nor words would otherwise end the run silently and leave the process with
// nothing to show for it — indistinguishable from an agent that decided it was done.
// Failing the round says so, and costs one more call.
func answerDecision(variable, text, stopReason string) (Decision, error) {
	if variable == "" {
		variable = DefaultAnswerVariable
	}
	answer := strings.TrimSpace(text)
	if answer == "" {
		return Decision{}, fmt.Errorf("the model answered with neither a tool call nor text (stop reason %q)", stopReason)
	}
	return Decision{Outputs: []model.VariableValue{{Name: variable, Kind: model.VarString, Text: answer}}}, nil
}

// snippet keeps an error message readable when an endpoint answers with a page.
func snippet(b []byte) string {
	const max = 400
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
