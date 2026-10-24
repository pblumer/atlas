package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/pblumer/atlas/connector/paperless"
)

// paperlessJobType is deliberately dynamic, not a reserved compiler job type:
// this initial external-only slice does not introduce a new engine job-type index.
// A future first-class <atlas:paperlessConnector> can reserve a *different* type.
const paperlessJobType = "paperless.v1"
const paperlessEnvPrefix = "ATLAS_PAPERLESS_"

// paperlessRegistryFromEnv configures named Paperless destinations without placing
// their API tokens into process variables, compiled models or WAL records.
func paperlessRegistryFromEnv(env func(string) string) (map[string]*paperless.Client, []string, error) {
	names := splitAndTrim(env(paperlessEnvPrefix + "CONNECTORS"))
	if len(names) == 0 {
		return nil, nil, nil
	}
	clients := make(map[string]*paperless.Client, len(names))
	for _, name := range names {
		key := paperlessEnvPrefix + envFold(name) + "_"
		allowHTTP := strings.EqualFold(env(key+"ALLOW_HTTP"), "true")
		client, err := paperless.NewClient(env(key+"URL"), env(key+"TOKEN"), paperless.Options{AllowHTTP: allowHTTP})
		if err != nil {
			return nil, nil, fmt.Errorf("worker: paperless worker %q is not usable: %w", name, err)
		}
		if _, exists := clients[name]; exists {
			return nil, nil, fmt.Errorf("worker: paperless worker %q is configured twice", name)
		}
		clients[name] = client
	}
	return clients, names, nil
}

// RunPaperlessJob runs one plain BPMN service-task job whose variable "paperless"
// is a metadata-only request (worker, operation and operation-specific attributes).
// The worker returns only the chosen result variable; absence of a result variable
// means that no API response is persisted into Atlas's variable events.
func RunPaperlessJob(ctx context.Context, j Job, clients map[string]*paperless.Client) (map[string]any, error) {
	raw, ok := j.Variables["paperless"]
	if !ok || raw == nil {
		return nil, errors.New("paperless: service task needs an input variable named paperless")
	}
	b, err := json.Marshal(raw)
	if err != nil || len(b) > 16*1024 {
		return nil, errors.New("paperless: invalid or oversized task request")
	}
	var request struct {
		Worker         string `json:"worker"`
		ResultVariable string `json:"resultVariable"`
		paperless.Request
	}
	if err := json.Unmarshal(b, &request); err != nil {
		return nil, errors.New("paperless: task request must be a JSON object")
	}
	if !validPaperlessResultVariable(request.ResultVariable) {
		return nil, errors.New("paperless: invalid resultVariable")
	}
	client, found := clients[request.Worker]
	if !found {
		return nil, errors.New("paperless: requested Worker is not configured on this Worker Instance")
	}
	result, err := client.Do(ctx, request.Request)
	if err != nil {
		return nil, err
	}
	if request.ResultVariable == "" {
		return nil, nil
	}
	return map[string]any{request.ResultVariable: result}, nil
}

func validPaperlessResultVariable(v string) bool {
	if v == "" {
		return true
	}
	if len(v) > 128 || !((v[0] >= 'A' && v[0] <= 'Z') || (v[0] >= 'a' && v[0] <= 'z') || v[0] == '_') {
		return false
	}
	for i := 1; i < len(v); i++ {
		c := v[i]
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}
