package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// connectorKindTemis is the central DMN decision connector kind (ADR-0050);
// connectorKindClio is the clio event-store connector kind (ADR-0036). Both are
// wired into the runtime and configurable in the Console: a connector record of
// either kind resolves to a live client with its token from the vault. The
// http.rest kind is model-authored (its endpoint lives in the model, not a record),
// so it is not a managed kind here.
// connectorKindMail is the outbound mail connector kind (ADR-0079/0081): a managed
// record of this kind resolves to a live mail client whose credential is read from
// the vault. Like clio, its provider and secret are managed here, never in the model;
// only the message (recipients, subject, body) is model-authored. The provider is
// SMTP (the default), Gmail, or Microsoft Graph — see mail.Provider* and
// mail.NewProviderClient, which own provider dispatch.
// connectorKindSharePoint is the SharePoint connector kind (ADR-0105): a managed
// record of this kind resolves to a live Microsoft Graph client whose OAuth
// credential is read from the vault. Like mail, its Graph base and secret are managed
// here, never in the model; only the target (site, list, item fields) is
// model-authored. See sharepoint.NewProviderClient, which owns provider dispatch.
const (
	connectorKindTemis      = "temis"
	connectorKindClio       = "clio"
	connectorKindMail       = "mail"
	connectorKindSharePoint = "sharepoint"
)

// connector is a managed connector instance: an operator-configured, durable
// integration Atlas delegates to (ADR-0041). It holds a name a model refers to, an
// endpoint, and — per the secret model — only a *reference* to a credential
// (CredentialsRef), never the secret value; the token is resolved from the
// environment at runtime. Disabled instances are kept but not registered.
type connector struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	Endpoint       string `json:"endpoint"`
	CredentialsRef string `json:"credentialsRef,omitempty"`
	Enabled        bool   `json:"enabled"`
	CreatedAt      int64  `json:"createdAt"`
	// Provider and Sender apply to a mail connector (Kind == connectorKindMail,
	// ADR-0079/0081). Provider selects the transport ("smtp", "gmail", or
	// "microsoft"; empty defaults to SMTP). Sender is the default From address a mail
	// task falls back to when it authors no sender — and, for SMTP, the auth username;
	// for a native provider, the mailbox to send as. Both are empty for the other
	// kinds. As with every kind, only a credential *reference* is stored here, never
	// the secret: for a native provider CredentialsRef names a vault auth bundle
	// (client secret, refresh token, or service-account key), never a value (I6).
	Provider string `json:"provider,omitempty"`
	Sender   string `json:"sender,omitempty"`
}

// connectorStore is a durable store for managed connector instances, one JSON file
// per id under a single directory — the same on-disk sidecar approach as the
// deployment/draft/dmn-ref stores (ADR-0019/0034/0041). Like them it is owned
// solely by the server's run-loop goroutine, so it needs no locking; and it holds
// no secret material, only references (I6).
type connectorStore struct {
	dir string
}

// newConnectorStore opens (creating if needed) the connectors directory.
func newConnectorStore(dir string) (*connectorStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("connectorstore: create dir: %w", err)
	}
	return &connectorStore{dir: dir}, nil
}

func (s *connectorStore) fileFor(id string) string {
	return filepath.Join(s.dir, hex.EncodeToString([]byte(id))+".json")
}

// save writes a connector durably (atomic write + directory fsync), overwriting
// any existing record with the same id.
func (s *connectorStore) save(rec connector) error {
	return atomicWriteJSON(s.dir, s.fileFor(rec.ID), rec)
}

// get returns the connector for an id, or ok=false if none exists.
func (s *connectorStore) get(id string) (connector, bool, error) {
	data, err := os.ReadFile(s.fileFor(id))
	if err != nil {
		if os.IsNotExist(err) {
			return connector{}, false, nil
		}
		return connector{}, false, fmt.Errorf("connectorstore: read: %w", err)
	}
	var rec connector
	if err := json.Unmarshal(data, &rec); err != nil {
		return connector{}, false, fmt.Errorf("connectorstore: decode: %w", err)
	}
	return rec, true, nil
}

// delete removes a connector. A missing connector is not an error (idempotent).
func (s *connectorStore) delete(id string) error {
	if err := os.Remove(s.fileFor(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("connectorstore: remove: %w", err)
	}
	return fsyncDir(s.dir)
}

// loadAll reads every connector, oldest first (creation order), so the Console
// lists them stably. Non-record files are ignored.
func (s *connectorStore) loadAll() ([]connector, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("connectorstore: read dir: %w", err)
	}
	var out []connector
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if _, err := hex.DecodeString(strings.TrimSuffix(e.Name(), ".json")); err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("connectorstore: read %s: %w", e.Name(), err)
		}
		var rec connector
		if err := json.Unmarshal(data, &rec); err != nil {
			return nil, fmt.Errorf("connectorstore: decode %s: %w", e.Name(), err)
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}
