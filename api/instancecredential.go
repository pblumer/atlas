package api

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"sync"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/logging"
	"github.com/pblumer/atlas/model"
)

// The credential an installed Worker Type's instance is launched with
// (ADR-draft-installed-worker-instances-hold-their-own-credential).
//
// Every other supervised child holds this server's internal token, whose worker scope
// reaches every job type. That is acceptable for the Worker Types built in this
// repository and not for an artifact atlas did not build (ADR-0535 §8): it could lease
// and settle the jobs of every other type, use the report routes of the built-in ones,
// stamp the MCP marker that is the same secret, and pass the broker's
// supervised-worker check. So an installed release gets a secret of its own instead,
// bound to its Worker Instance id and the job types its manifest declares.
//
// It lives in memory only, minted when the release is registered with the supervisor
// and revoked when it is removed. A restart of the server forgets every secret and
// mints new ones as it registers the releases again; the children of the old process
// ended with it (ADR-0439), and one that did not is refused like any unknown bearer.

// instanceTokenPrefix marks the secret, as apiTokenPrefix marks an API token, so a
// scanner finding one in a log can tell what it is.
const instanceTokenPrefix = "atlaswi_"

// instancePrincipalPrefix namespaces the principal the secret resolves to, so an
// audit line says an installed instance acted, and which.
const instancePrincipalPrefix = "system:instance:"

// instanceCredential is what one secret stands for.
type instanceCredential struct {
	worker   string
	jobTypes []string
}

// instanceCredentials indexes the live secrets by their hash, the way the API-token
// index does, so the secret itself is never a map key and a lookup compares no
// secret byte by byte. A nil index, on a Server built without New, holds nothing and
// mints nothing.
type instanceCredentials struct {
	mu       sync.RWMutex
	byHash   map[string]instanceCredential
	byWorker map[string]string // worker id -> secret
	random   func(int) (string, error)
}

func newInstanceCredentials() *instanceCredentials {
	return &instanceCredentials{
		byHash: map[string]instanceCredential{}, byWorker: map[string]string{}, random: randomHex,
	}
}

func hashInstanceToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// mint returns the secret of worker, minting one bound to jobTypes when it has none.
// It is stable while the worker stays registered: the supervisor reads the launch
// environment at every spawn and restarts a worker whose environment changed, so a
// secret per spawn would restart it for ever. It returns "" when no random secret
// could be drawn — the instance is then refused at every poll, never let in.
func (c *instanceCredentials) mint(worker string, jobTypes []string) string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if tok, ok := c.byWorker[worker]; ok {
		return tok
	}
	secret, err := c.random(32)
	if err != nil {
		logging.Warn(logging.WorkerSupervisorFailed, "no credential could be minted for an installed Worker Type's instance; it will be refused",
			slog.String("id", worker), slog.String("error", err.Error()))
		return ""
	}
	tok := instanceTokenPrefix + secret
	c.byWorker[worker] = tok
	c.byHash[hashInstanceToken(tok)] = instanceCredential{worker: worker, jobTypes: slices.Clone(jobTypes)}
	return tok
}

// revoke forgets worker's secret; from then on it is refused as unknown.
func (c *instanceCredentials) revoke(worker string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if tok, ok := c.byWorker[worker]; ok {
		delete(c.byHash, hashInstanceToken(tok))
		delete(c.byWorker, worker)
	}
}

// match resolves a bearer to the credential it is, if it is one.
func (c *instanceCredentials) match(tok string) (instanceCredential, bool) {
	if c == nil {
		return instanceCredential{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	cred, ok := c.byHash[hashInstanceToken(tok)]
	return cred, ok
}

// tokenOf reports worker's live secret.
func (c *instanceCredentials) tokenOf(worker string) (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	tok, ok := c.byWorker[worker]
	return tok, ok
}

// principal is what an instance's secret resolves to: the operator role the worker
// routes ask for, the confined scope, and the binding the handlers check.
func (cred instanceCredential) principal() *httpapi.Principal {
	return &httpapi.Principal{
		UserID:   instancePrincipalPrefix + cred.worker,
		Username: cred.worker,
		Roles:    []string{RoleOperator},
		Scope:    apiScopeInstalledWorker,
		Instance: &httpapi.InstanceBinding{Worker: cred.worker, JobTypes: slices.Clone(cred.jobTypes)},
	}
}

// instanceToken is the secret an installed release's instance is launched with, or ""
// when this server asks for no credential.
func (s *Server) instanceToken(worker string, jobTypes []string) string {
	if !s.authEnabled {
		return ""
	}
	return s.instanceCreds.mint(worker, jobTypes)
}

// instanceRefusesJob says why the request's principal may not report as worker on
// the job jv, or "" when it may. Only a principal bound to an installed instance is
// ever refused: it reports only as itself, on a job of one of its own types — so
// never through the operator door, which names no worker. Runs on the loop.
func (s *Server) instanceRefusesJob(p *httpapi.Principal, worker string, jv *model.JobValue) string {
	if p == nil || p.Instance == nil {
		return ""
	}
	name, ok := s.jobTypes.Name(jv.JobType)
	if !ok {
		// A type with no name cannot be one the binding lists; say so with its index
		// rather than skip the type check an empty name would.
		name = "#" + strconv.Itoa(int(jv.JobType))
	}
	return httpapi.InstanceRefusal(p, worker, name)
}

// refuseInstance writes the 403 for work outside an instance's binding, and the
// audit line every scope refusal leaves.
func refuseInstance(w http.ResponseWriter, r *http.Request, why string) {
	auditRefusal(r, logging.AuthDenied, "refused: outside this Worker Instance's binding", slog.String("reason", why),
		slog.String("method", r.Method), slog.String("path", r.URL.Path))
	httpapi.Error(w, http.StatusForbidden, why)
}
