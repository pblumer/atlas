package api

import (
	"encoding/json"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/mail"
	"github.com/pblumer/atlas/logging"
)

// What a supervised worker is told at spawn, and why the engine is allowed to tell it.
//
// ADR-0168 draws the line at the *job payload*: the engine decides what to send, the
// worker knows where to send it and with what credential, and a secret never travels
// with a leased job. That line is what lets a worker sit in a network the engine
// cannot reach, and nothing here moves it — a [mail.Job] still has nowhere to put a
// password.
//
// A supervised worker is the one case where the engine also happens to *be* the
// operator. It is this process's own child: same host, same user, started by the same
// command line, and it already inherits this process's whole environment. So handing
// it a worker's configuration through that environment is the operator setting the
// worker up, done by the program instead of by hand — the secret goes neither over
// the wire nor into a payload, and an `atlas worker` run by hand is configured
// exactly the same way, from the same variables.
//
// That distinction is why this file provisions only *supervised* children. An
// external worker gets nothing from here; its operator sets its environment, which is
// the arrangement ADR-0168 is actually about.
//
// It also makes mail the first *managed* kind Atlas can offload by default: mail's
// endpoint and password live in the worker store rather than the environment, and
// until the engine could hand them over, offloading mail meant handing every mail
// task to a worker with no mailbox.

// Environment variables a supervised worker reads its mail configuration from. They
// are the same names an operator sets by hand for an external worker — there is no
// private channel between a supervised worker and its parent, because a private
// channel is how the supervised path would quietly become the only tested one
// (ADR-0157).
const (
	// mailConnectorsEnv lists the worker names this worker can send through.
	mailConnectorsEnv = "ATLAS_MAIL_CONNECTORS"
	// mailOutboxURLEnv is where a preview worker delivers: this server's own
	// outbox, over the API. A worker frames the message and posts it back, so the
	// operator reads it in Operations › Outbox as if it had never left (ADR-0150).
	mailOutboxURLEnv = "ATLAS_MAIL_OUTBOX_URL"
)

// mailWorkerEnv renders this server's mail workers as the environment a
// supervised worker builds the identical clients from. It is re-read on every spawn,
// so an operator who adds a worker in the Console and presses Restart in the
// Workers view has a worker that can send through it — without restarting Atlas.
//
// It reads the worker store and the vault, so it runs on the run-loop goroutine
// (their owner), like buildMailClients does.
func (s *Server) mailWorkerEnv() []string {
	var (
		names []string
		env   []string
	)
	s.do(func() {
		recs, err := s.connectors.LoadAll()
		if err != nil {
			logging.Warn(logging.WorkerSupervisorFailed, "could not read the worker store for a supervised worker",
				slog.String("error", err.Error()))
			return
		}
		sort.Slice(recs, func(i, j int) bool { return recs[i].Name < recs[j].Name })
		taken := map[string]string{}
		for _, c := range recs {
			if c.Kind != connectorKindMail || !c.Enabled {
				continue
			}
			key := connectorEnvKey(c.Name)
			if key == "" {
				continue
			}
			// Two names that fold to the same variable would silently give one of them
			// the other's credential, so the second is left out and said out loud. The
			// worker then reports it as a worker it does not hold, and the Workers
			// view shows it among the names served nowhere (ADR-0168).
			if first, dup := taken[key]; dup {
				logging.Warn(logging.WorkerSupervisorFailed,
					"two mail workers share one environment name; the second is not handed to the supervised worker",
					slog.String("connector", c.Name), slog.String("collidesWith", first))
				continue
			}
			taken[key] = c.Name
			names = append(names, c.Name)
			env = append(env, mailConnectorEnv(key, c, s.resolveConnectorSecret(c.CredentialsRef))...)
		}
	})
	if len(names) == 0 {
		return nil
	}
	env = append(env, mailConnectorsEnv+"="+strings.Join(names, ","))
	if s.superviseURL != "" {
		env = append(env, mailOutboxURLEnv+"="+strings.TrimRight(s.superviseURL, "/")+"/api/v1/mail/outbox")
	}
	return env
}

// mailConnectorEnv is one worker's configuration as environment variables: exactly
// the fields [mail.ProviderConfig] is built from, so the worker's client and the
// engine's are the same client built from the same values.
//
// The secret is written last and only when there is one, so a worker with no
// credential does not hand the child an empty variable that looks like a configured
// blank password.
func mailConnectorEnv(key string, c connector, secret string) []string {
	p := mailEnvPrefix + key + "_"
	env := []string{
		p + "PROVIDER=" + mailProviderOf(c),
		p + "ENDPOINT=" + strings.TrimSpace(c.Endpoint),
		p + "SENDER=" + strings.TrimSpace(c.Sender),
	}
	if secret != "" {
		env = append(env, p+"SECRET="+secret)
	}
	return env
}

// mailEnvPrefix is where a mail worker's configuration lives. It matches the worker's
// own constant of the same name; TestSupervisedMailEnvUsesTheWorkersOwnNames holds
// the two together.
const mailEnvPrefix = "ATLAS_MAIL_"

// mailProviderOf is a record's provider, defaulted the way buildMailClients defaults
// it, so a record written before the field existed is handed over as what it actually
// is rather than as an empty provider the worker would have to guess at.
func mailProviderOf(c connector) string {
	if p := strings.TrimSpace(c.Provider); p != "" {
		return p
	}
	return mail.ProviderSMTP
}

// provisionedConnectorKinds maps each kind whose configuration the engine hands to a
// supervised worker to the function that renders it. It is the one list: a managed
// kind may only be in [DefaultOffloadedKinds] if it appears here, because otherwise
// the default would move its tasks to a worker holding nothing to serve them with —
// TestEveryDefaultOffloadedKindCanBeServedByItsWorker checks exactly that.
func (s *Server) provisionedConnectorKinds() map[string]func() []string {
	return map[string]func() []string{
		connectorKindMail: s.mailWorkerEnv,
		// AD is not a managed kind — no worker record, no store entry — but its
		// bind-password *reference* can resolve out of the vault, which a supervised
		// worker cannot read either. So it is provisioned for the same reason mail
		// is, and defaulting it without this would have moved every vault-backed AD
		// task to a worker holding nothing to bind with.
		"ad": s.adWorkerEnv,
		// REST is neither managed nor credential-free: the endpoint is in the model but
		// the auth secret is a vault reference, which is AD's shape exactly. Provisioned
		// for AD's reason, and defaulted onto a worker for the plainest one there is —
		// an HTTP call to somebody else's host is the original argument for ADR-0164.
		"rest": s.restWorkerEnv,
		// LDAP is AD's shape without Microsoft's dialect: the directory is model data
		// and travels, the bind password and client certificate are vault references
		// and cannot. Provisioned for AD's reason, and defaulted with it.
		"ldap": s.ldapWorkerEnv,
		// SOAP is REST's shape in an envelope: the call is model data and travels, the
		// credential behind authSecret is a vault reference and cannot. Provisioned
		// for REST's reason, and defaulted with it.
		"soap": s.soapWorkerEnv,
		// SCIM is REST's shape with a provisioning vocabulary. Same reason again.
		"scim": s.scimWorkerEnv,
		// temis is clio's shape with a decision service: endpoint in the connector
		// store, token in the vault, neither readable by a supervised worker.
		connectorKindTemis: s.temisWorkerEnv,
		// SharePoint is a managed kind like Jira: the site's address and its OAuth
		// bundle are a record and a vault secret, so a supervised worker holding
		// neither could serve no SharePoint task at all.
		connectorKindSharePoint: s.sharepointWorkerEnv,
		// Entra is worker-only like AD (the engine holds no tenant credential, ADR-0172),
		// and provisioned for the same reason: a supervised worker has no vault, so the
		// engine renders its client secret out of the vault. Only the secret — tenant and
		// client id are not secret and are inherited from this process's own environment.
		"entra": s.entraWorkerEnv,
		// The three SQL products. Unlike every kind above them the *whole* configuration
		// is the secret — a DSN has no public half — so what is rendered here is one
		// connection string per configured database and nothing else
		// (ADR-0188).
		// Remedy is provisioned for exactly mail's reason: its base URL and service
		// account live in the worker store and the vault (ADR-0106), so a supervised
		// worker holding neither could serve no Remedy task at all.
		connectorKindRemedy: s.remedyWorkerEnv,
		// clio is Remedy's shape with an event store in place of an ITSM instance
		// (ADR-0036): the base endpoint is in the connector store and the token in the
		// vault, so a supervised worker holding neither could serve no clio task.
		connectorKindClio: s.clioWorkerEnv,
		// Jira is provisioned for exactly Remedy's reason: its site URL and credential
		// live in the worker store and the vault (ADR-0201), so a supervised worker
		// holding neither could serve no Jira task at all.
		connectorKindJira:     s.jiraWorkerEnv,
		connectorKindPostgres: func() []string { return s.sqlWorkerEnvByName(connectorKindPostgres) },
		connectorKindMariaDB:  func() []string { return s.sqlWorkerEnvByName(connectorKindMariaDB) },
		connectorKindMSSQL:    func() []string { return s.sqlWorkerEnvByName(connectorKindMSSQL) },
	}
}

// Environment a supervised Entra worker reads its tenants from — the same names an
// operator sets by hand for an external worker (there is no private channel, ADR-0157).
const (
	entraEnvPrefix     = "ATLAS_ENTRA_"
	entraConnectorsEnv = entraEnvPrefix + "CONNECTORS"
)

// entraWorkerEnv renders the client secrets a supervised Entra worker needs out of the
// vault, one variable per tenant this process's environment names in ATLAS_ENTRA_CONNECTORS.
//
// It is the AD story with a worker name in place of a bind-secret reference. The
// Entra worker is worker-only (ADR-0172): it reads ATLAS_ENTRA_<NAME>_TENANT_ID,
// _CLIENT_ID and _CLIENT_SECRET, and the engine never builds an Entra client. Tenant
// and client id are not secret and reach a supervised child by inheriting this
// process's environment; the secret must not sit there in the clear, so an operator who
// wants it in the vault sets ATLAS_ENTRA_<NAME>_CLIENT_SECRET_REF to a vault key instead
// and types the secret into the Console. This resolves that reference — vault first,
// environment on a miss (ADR-0069/0041) — and hands the child the value under the exact
// name the worker reads it by.
//
// A tenant whose secret the operator set directly is left untouched: the child already
// inherits ATLAS_ENTRA_<NAME>_CLIENT_SECRET, and overriding it would let a stale vault
// entry silently win over an explicit choice.
//
// Re-rendered on every spawn and refresh, like the mail and AD configuration, so a
// secret an operator adds or rotates in the Console reaches the worker without a restart.
// It reads the vault, so it runs on the run-loop goroutine (invariant I3), its owner.
func (s *Server) entraWorkerEnv() []string {
	var env []string
	var names []string
	var fromStore bool // a store tenant contributed a name; only then must CONNECTORS be rendered
	seen := map[string]bool{}
	addName := func(n string) {
		if n = strings.TrimSpace(n); n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	s.do(func() {
		// Tenants an operator set directly on the host (env): inherited by the child, so
		// only the #565 vault-reference bridge is done here — but they still belong in the
		// rendered CONNECTORS list so a store-based render below does not drop them.
		for _, name := range splitConnectorList(os.Getenv(entraConnectorsEnv)) {
			addName(name)
			envKey := connectorEnvKey(name)
			if envKey == "" {
				continue
			}
			key := entraEnvPrefix + envKey + "_"
			if strings.TrimSpace(os.Getenv(key+"CLIENT_SECRET")) != "" {
				continue // the operator set the secret directly; nothing to resolve
			}
			if ref := strings.TrimSpace(os.Getenv(key + "CLIENT_SECRET_REF")); ref != "" {
				if secret := s.resolveConnectorSecret(ref); secret != "" {
					env = append(env, key+"CLIENT_SECRET="+secret)
				}
			}
		}
		// Tenants an operator added in the Console (ADR-0172, amended): the tenant id,
		// client id and client secret live together in the vault bundle credentialsRef
		// names. Render the three variables the worker reads — the engine holds none of
		// them, it only hands them to its own supervised child at spawn.
		recs, err := s.connectors.LoadAll()
		if err != nil {
			logging.Warn(logging.WorkerSupervisorFailed, "could not read the worker store for a supervised entra worker",
				slog.String("error", err.Error()))
			return
		}
		sort.Slice(recs, func(i, j int) bool { return recs[i].Name < recs[j].Name })
		taken := map[string]string{}
		for _, c := range recs {
			if c.Kind != connectorKindEntra || !c.Enabled {
				continue
			}
			envKey := connectorEnvKey(c.Name)
			if envKey == "" {
				continue
			}
			// Two names that fold to one variable would silently give one the other's
			// credential — the mail/AD collision, left out for the same reason.
			if first, dup := taken[envKey]; dup {
				logging.Warn(logging.WorkerSupervisorFailed,
					"two entra workers share one environment name; the second is not handed to the supervised worker",
					slog.String("connector", c.Name), slog.String("collidesWith", first))
				continue
			}
			// A bundle that does not resolve (no secret set yet, or malformed) is left out
			// rather than handed over half-filled: the worker then simply does not build
			// that tenant, and the Console shows the worker as configured-not-working
			// instead of a token failing mid-run.
			b, ok := entraBundleParse(s.resolveConnectorSecret(c.CredentialsRef))
			if !ok {
				continue
			}
			taken[envKey] = c.Name
			key := entraEnvPrefix + envKey + "_"
			env = append(env,
				key+"TENANT_ID="+b.TenantID,
				key+"CLIENT_ID="+b.ClientID,
				key+"CLIENT_SECRET="+b.ClientSecret)
			if base := strings.TrimSpace(c.Endpoint); base != "" {
				env = append(env, key+"BASE_URL="+base) // a national cloud overrides the Graph base
			}
			addName(c.Name)
			fromStore = true
		}
	})
	// Only a store tenant needs CONNECTORS rendered: an operator who set it on the host
	// has it inherited by the child already, so rendering it there would be redundant.
	// When the store does contribute, render the union so an env-named tenant is not lost
	// to the override.
	if !fromStore {
		return env
	}
	return append(env, entraConnectorsEnv+"="+strings.Join(names, ","))
}

// entraBundle is the OAuth bundle an operator stores in the vault under an entra
// worker's credentialsRef: the tenant id, client id and client secret together, so
// the record itself holds no credential (ADR-0172). ok is false when the bundle is
// absent or missing a field — the tenant is then left unconfigured rather than handed
// over half-filled.
type entraBundle struct {
	TenantID     string `json:"tenantId"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

// entraBundle parses a vault bundle value. An empty value, invalid JSON, or a missing
// field yields ok=false.
func entraBundleParse(raw string) (entraBundle, bool) {
	if strings.TrimSpace(raw) == "" {
		return entraBundle{}, false
	}
	var b entraBundle
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		return entraBundle{}, false
	}
	if strings.TrimSpace(b.TenantID) == "" || strings.TrimSpace(b.ClientID) == "" || strings.TrimSpace(b.ClientSecret) == "" {
		return entraBundle{}, false
	}
	return b, true
}

// Environment a supervised Remedy worker reads its AR System instances from — the
// same names an operator sets by hand for an external worker (there is no private
// channel, ADR-0157). remedyEnvPrefix matches the worker's own constant of the same
// name; TestSupervisedRemedyEnvUsesTheWorkersOwnNames holds the two together.
// Environment a supervised clio worker reads its event stores from — the same names
// an operator sets by hand for an external worker (there is no private channel,
// ADR-0157). clioEnvPrefix matches the worker's own constant of the same name;
// TestSupervisedClioEnvUsesTheWorkersOwnNames holds the two together.
const (
	clioEnvPrefix     = "ATLAS_CLIO_"
	clioConnectorsEnv = clioEnvPrefix + "CONNECTORS"
)

// clioWorkerEnv renders this server's clio connectors as the environment a supervised
// worker builds the identical clients from: each instance's base endpoint and, where
// one is configured, the bearer token behind its credentialsRef.
//
// It is Remedy's story with an event store in place of an ITSM instance
// (ADR-0036/0168). One difference is deliberate: a connector with no token is still
// rendered. clio may be reached without one — a store an operator runs beside Atlas —
// and dropping such a connector would leave a working instance unserved, where Remedy
// without a password is simply not configured.
//
// A connector an operator configured on the host is left untouched and kept in the
// rendered list: the child inherits ATLAS_CLIO_<NAME>_* already, and dropping its name
// would let a store connector silently take the whole list away from it.
//
// It reads the connector store and the vault, so it runs on the run-loop goroutine
// (their owner, invariant I3), like buildClioClients does.
func (s *Server) clioWorkerEnv() []string {
	var (
		env       []string
		names     []string
		fromStore bool // a store connector contributed a name; only then must CONNECTORS be rendered
	)
	seen := map[string]bool{}
	addName := func(n string) {
		if n = strings.TrimSpace(n); n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	// Stores an operator set directly on the host: inherited by the child as they are,
	// so nothing is rendered for them — they are only kept in the list below.
	for _, name := range splitConnectorList(os.Getenv(clioConnectorsEnv)) {
		addName(name)
	}
	s.do(func() {
		recs, err := s.connectors.LoadAll()
		if err != nil {
			logging.Warn(logging.WorkerSupervisorFailed, "could not read the connector store for a supervised clio worker",
				slog.String("error", err.Error()))
			return
		}
		sort.Slice(recs, func(i, j int) bool { return recs[i].Name < recs[j].Name })
		taken := map[string]string{}
		for _, c := range recs {
			if c.Kind != connectorKindClio || !c.Enabled {
				continue
			}
			envKey := connectorEnvKey(c.Name)
			if envKey == "" {
				continue
			}
			// Two names that fold to one variable would silently give one the other's
			// token — the mail/remedy collision, left out for the same reason.
			if first, dup := taken[envKey]; dup {
				logging.Warn(logging.WorkerSupervisorFailed,
					"two clio connectors share one environment name; the second is not handed to the supervised worker",
					slog.String("connector", c.Name), slog.String("collidesWith", first))
				continue
			}
			endpoint := strings.TrimSpace(c.Endpoint)
			if endpoint == "" {
				// Nothing to reach: left out rather than handed over half-filled, so the
				// worker starts and the Console shows the connector as configured-not-working.
				continue
			}
			taken[envKey] = c.Name
			key := clioEnvPrefix + envKey + "_"
			env = append(env, key+"ENDPOINT="+endpoint)
			if token := strings.TrimSpace(s.resolveConnectorSecret(c.CredentialsRef)); token != "" {
				env = append(env, key+"TOKEN="+token)
			}
			addName(c.Name)
			fromStore = true
		}
	})
	// Only a store connector needs CONNECTORS rendered: an operator who set it on the
	// host has it inherited by the child already. When the store does contribute,
	// render the union so a host-named store is not lost to the override.
	if !fromStore {
		return nil
	}
	return append(env, clioConnectorsEnv+"="+strings.Join(names, ","))
}

const (
	temisEnvPrefix     = "ATLAS_TEMIS_"
	temisConnectorsEnv = temisEnvPrefix + "CONNECTORS"
)

// temisWorkerEnv renders the decision services a supervised temis worker needs
// (ADR-0233, slice 7). It is clioWorkerEnv's shape with a decision service in place
// of an event store: the endpoint is a connector record and the token a vault
// reference behind it, neither of which a supervised worker can read.
//
// A service with no token is still handed over, for clio's reason rather than
// Remedy's: a decision service run beside Atlas may be reachable without one, and
// dropping it would leave a working installation unserved.
//
// It reads the connector store and the vault, so it runs on the run-loop goroutine
// (invariant I3), like buildTemisClients does.
func (s *Server) temisWorkerEnv() []string {
	var (
		env       []string
		names     []string
		fromStore bool
	)
	seen := map[string]bool{}
	addName := func(n string) {
		if n = strings.TrimSpace(n); n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	// Services an operator set directly on the host: inherited by the child as they
	// are, so nothing is rendered for them — they are only kept in the list below.
	for _, name := range splitConnectorList(os.Getenv(temisConnectorsEnv)) {
		addName(name)
	}
	s.do(func() {
		recs, err := s.connectors.LoadAll()
		if err != nil {
			logging.Warn(logging.WorkerSupervisorFailed, "could not read the connector store for a supervised temis worker",
				slog.String("error", err.Error()))
			return
		}
		sort.Slice(recs, func(i, j int) bool { return recs[i].Name < recs[j].Name })
		taken := map[string]string{}
		for _, c := range recs {
			if c.Kind != connectorKindTemis || !c.Enabled {
				continue
			}
			envKey := connectorEnvKey(c.Name)
			if envKey == "" {
				continue
			}
			if first, dup := taken[envKey]; dup {
				logging.Warn(logging.WorkerSupervisorFailed,
					"two temis services share one environment name; the second is not handed to the supervised worker",
					slog.String("connector", c.Name), slog.String("collidesWith", first))
				continue
			}
			endpoint := strings.TrimSpace(c.Endpoint)
			if endpoint == "" {
				// Nothing to reach: left out rather than handed over half-filled, so
				// the worker starts and the Console shows it as configured-not-working.
				continue
			}
			taken[envKey] = c.Name
			key := temisEnvPrefix + envKey + "_"
			env = append(env, key+"URL="+endpoint)
			if token := strings.TrimSpace(s.resolveConnectorSecret(c.CredentialsRef)); token != "" {
				env = append(env, key+"TOKEN="+token)
			}
			addName(c.Name)
			fromStore = true
		}
	})
	if !fromStore {
		return nil
	}
	return append(env, temisConnectorsEnv+"="+strings.Join(names, ","))
}

const (
	remedyEnvPrefix     = "ATLAS_REMEDY_"
	remedyConnectorsEnv = remedyEnvPrefix + "CONNECTORS"
)

// remedyWorkerEnv renders this server's Remedy workers as the environment a
// supervised worker builds the identical clients from: the AR System base URL and the
// {username,password} bundle behind each worker's credentialsRef.
//
// It is mail's story with an ITSM instance in place of a mailbox (ADR-0106/0168). The
// base URL and the service account live in the worker store and the vault, which a
// supervised worker can read no more than it can read the engine's memory — so
// offloading Remedy without this would hand every Remedy task to a worker with no
// instance to file against.
//
// A worker an operator configured on the host is left untouched and kept in the
// rendered list: the child inherits ATLAS_REMEDY_<NAME>_* already, and dropping its
// name would let a store worker silently take the whole list away from it.
//
// It reads the worker store and the vault, so it runs on the run-loop goroutine
// (their owner, invariant I3), like buildRemedyClients does.
func (s *Server) remedyWorkerEnv() []string {
	var (
		env       []string
		names     []string
		fromStore bool // a store worker contributed a name; only then must CONNECTORS be rendered
	)
	seen := map[string]bool{}
	addName := func(n string) {
		if n = strings.TrimSpace(n); n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	// Instances an operator set directly on the host: inherited by the child as they
	// are, so nothing is rendered for them — they are only kept in the list below.
	for _, name := range splitConnectorList(os.Getenv(remedyConnectorsEnv)) {
		addName(name)
	}
	s.do(func() {
		recs, err := s.connectors.LoadAll()
		if err != nil {
			logging.Warn(logging.WorkerSupervisorFailed, "could not read the worker store for a supervised remedy worker",
				slog.String("error", err.Error()))
			return
		}
		sort.Slice(recs, func(i, j int) bool { return recs[i].Name < recs[j].Name })
		taken := map[string]string{}
		for _, c := range recs {
			if c.Kind != connectorKindRemedy || !c.Enabled {
				continue
			}
			envKey := connectorEnvKey(c.Name)
			if envKey == "" {
				continue
			}
			// Two names that fold to one variable would silently give one the other's
			// credential — the mail/entra collision, left out for the same reason.
			if first, dup := taken[envKey]; dup {
				logging.Warn(logging.WorkerSupervisorFailed,
					"two remedy workers share one environment name; the second is not handed to the supervised worker",
					slog.String("connector", c.Name), slog.String("collidesWith", first))
				continue
			}
			endpoint := strings.TrimSpace(c.Endpoint)
			creds, ok := remedyBundleParse(s.resolveConnectorSecret(c.CredentialsRef))
			// A worker with no endpoint, or whose bundle does not resolve (no secret
			// set yet, or malformed), is left out rather than handed over half-filled:
			// the worker then refuses at startup on a *named* instance missing a field,
			// which would take down every other kind it serves. Left out, it simply is
			// not served, and the Console shows the worker as configured-not-working.
			if endpoint == "" || !ok {
				continue
			}
			taken[envKey] = c.Name
			key := remedyEnvPrefix + envKey + "_"
			env = append(env,
				key+"ENDPOINT="+endpoint,
				key+"USERNAME="+creds.Username,
				key+"PASSWORD="+creds.Password)
			addName(c.Name)
			fromStore = true
		}
	})
	// Only a store worker needs CONNECTORS rendered: an operator who set it on the
	// host has it inherited by the child already. When the store does contribute,
	// render the union so a host-named instance is not lost to the override.
	if !fromStore {
		return nil
	}
	return append(env, remedyConnectorsEnv+"="+strings.Join(names, ","))
}

// remedyBundleParse parses the vault bundle a remedy worker's credentialsRef names
// (ADR-0106): the AR System username and password together, so the record itself holds
// no credential. ok is false when the bundle is absent, invalid JSON, or missing a
// field — the instance is then left unconfigured rather than handed over half-filled,
// because an AR System login needs both halves.
func remedyBundleParse(raw string) (remedyCredentials, bool) {
	if strings.TrimSpace(raw) == "" {
		return remedyCredentials{}, false
	}
	var c remedyCredentials
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return remedyCredentials{}, false
	}
	if strings.TrimSpace(c.Username) == "" || strings.TrimSpace(c.Password) == "" {
		return remedyCredentials{}, false
	}
	return c, true
}

// Environment a supervised SharePoint worker reads its instances from — the same
// names an operator sets by hand for an external worker (there is no private channel,
// ADR-0157). sharepointEnvPrefix matches the worker's own constant of the same name;
// TestSupervisedSharePointEnvUsesTheWorkersOwnNames holds the two together.
const (
	sharepointEnvPrefix     = "ATLAS_SHAREPOINT_"
	sharepointConnectorsEnv = sharepointEnvPrefix + "CONNECTORS"
)

// sharepointWorkerEnv renders this server's SharePoint instances as the environment a
// supervised worker builds the identical clients from (ADR-0233, slice 5).
//
// It is jiraWorkerEnv's story with a document library in place of an issue tracker
// (ADR-0141/0168): the Graph endpoint and the OAuth bundle live in the worker store
// and the vault, which a supervised worker can read no more than it can read the
// engine's memory — so offloading SharePoint without this would hand every task to a
// worker with no site to create items in.
//
// One difference from Jira is deliberate: the credential is handed over as the *whole
// bundle*, one opaque value, rather than field by field. The bundle has no public half
// worth splitting — tenant and client ids sit in the same vault secret as the client
// secret and the refresh token — and splitting it would mean this function deciding
// the grant's shape a second time, where getting it wrong yields a worker that fails
// every job. sharepoint.NewProviderClient parses and validates it on the far side, as
// it already does here. It is the SQL kinds' arrangement (ADR-0188) for the SQL kinds'
// reason.
//
// A worker an operator configured on the host is left untouched and kept in the
// rendered list: the child inherits ATLAS_SHAREPOINT_<NAME>_* already, and dropping
// its name would let a store instance silently take the whole list away from it.
//
// It reads the worker store and the vault, so it runs on the run-loop goroutine
// (their owner, invariant I3), like buildSharePointClients does.
func (s *Server) sharepointWorkerEnv() []string {
	var (
		env       []string
		names     []string
		fromStore bool // a store instance contributed a name; only then must CONNECTORS be rendered
	)
	seen := map[string]bool{}
	addName := func(n string) {
		if n = strings.TrimSpace(n); n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	// Instances an operator set directly on the host: inherited by the child as they
	// are, so nothing is rendered for them — they are only kept in the list below.
	for _, name := range splitConnectorList(os.Getenv(sharepointConnectorsEnv)) {
		addName(name)
	}
	s.do(func() {
		recs, err := s.connectors.LoadAll()
		if err != nil {
			logging.Warn(logging.WorkerSupervisorFailed, "could not read the worker store for a supervised sharepoint worker",
				slog.String("error", err.Error()))
			return
		}
		sort.Slice(recs, func(i, j int) bool { return recs[i].Name < recs[j].Name })
		taken := map[string]string{}
		for _, c := range recs {
			if c.Kind != connectorKindSharePoint || !c.Enabled {
				continue
			}
			envKey := connectorEnvKey(c.Name)
			if envKey == "" {
				continue
			}
			// Two names that fold to one variable would silently give one the other's
			// credential — the mail/jira collision, left out for the same reason.
			if first, dup := taken[envKey]; dup {
				logging.Warn(logging.WorkerSupervisorFailed,
					"two sharepoint workers share one environment name; the second is not handed to the supervised worker",
					slog.String("connector", c.Name), slog.String("collidesWith", first))
				continue
			}
			bundle := strings.TrimSpace(s.resolveConnectorSecret(c.CredentialsRef))
			// An instance whose bundle does not resolve — no secret set yet — is left
			// out rather than handed over empty: the worker refuses at startup on a
			// *named* instance it cannot build, which would take down every other kind
			// it serves. Left out, it is simply not served, and the Console shows it as
			// configured-not-working.
			if bundle == "" {
				continue
			}
			taken[envKey] = c.Name
			key := sharepointEnvPrefix + envKey + "_"
			// The endpoint is optional: blank means the Graph default, which is what
			// buildSharePointClients passes too, so a record without one builds the
			// same client on both sides.
			if endpoint := strings.TrimSpace(c.Endpoint); endpoint != "" {
				env = append(env, key+"ENDPOINT="+endpoint)
			}
			env = append(env, key+"CREDENTIALS="+bundle)
			addName(c.Name)
			fromStore = true
		}
	})
	// Only a store instance needs CONNECTORS rendered: an operator who set it on the
	// host has it inherited by the child already. When the store does contribute,
	// render the union so a host-named instance is not lost to the override.
	if !fromStore {
		return nil
	}
	return append(env, sharepointConnectorsEnv+"="+strings.Join(names, ","))
}

// Environment a supervised Jira worker reads its sites from — the same names an
// operator sets by hand for an external worker (there is no private channel, ADR-0157).
// jiraEnvPrefix matches the worker's own constant of the same name;
// TestSupervisedJiraEnvUsesTheWorkersOwnNames holds the two together.
const (
	jiraEnvPrefix     = "ATLAS_JIRA_"
	jiraConnectorsEnv = jiraEnvPrefix + "CONNECTORS"
)

// jiraWorkerEnv renders this server's Jira workers as the environment a supervised
// worker builds the identical clients from: the site URL and, out of the vault bundle
// behind each worker's credentialsRef, either the Cloud {email, apiToken} pair or a
// Data Center personal access token.
//
// It is Remedy's story with an issue tracker in place of an AR System (ADR-0201/0168).
// The site URL and the credential live in the worker store and the vault, which a
// supervised worker can read no more than it can read the engine's memory — so
// offloading Jira without this would hand every Jira task to a worker with no site to
// file against.
//
// Exactly one credential shape is rendered per worker, chosen the way
// jira.NewProviderClient chooses it, so the supervised worker cannot end up talking to
// a product the engine thinks it is not: the shape decides the authentication scheme,
// how an assignee is addressed, and which search endpoint is used.
//
// A worker an operator configured on the host is left untouched and kept in the
// rendered list: the child inherits ATLAS_JIRA_<NAME>_* already, and dropping its name
// would let a store worker silently take the whole list away from it.
//
// It reads the worker store and the vault, so it runs on the run-loop goroutine
// (their owner, invariant I3), like buildJiraClients does.
func (s *Server) jiraWorkerEnv() []string {
	var (
		env       []string
		names     []string
		fromStore bool // a store worker contributed a name; only then must CONNECTORS be rendered
	)
	seen := map[string]bool{}
	addName := func(n string) {
		if n = strings.TrimSpace(n); n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	// Instances an operator set directly on the host: inherited by the child as they
	// are, so nothing is rendered for them — they are only kept in the list below.
	for _, name := range splitConnectorList(os.Getenv(jiraConnectorsEnv)) {
		addName(name)
	}
	s.do(func() {
		recs, err := s.connectors.LoadAll()
		if err != nil {
			logging.Warn(logging.WorkerSupervisorFailed, "could not read the worker store for a supervised jira worker",
				slog.String("error", err.Error()))
			return
		}
		sort.Slice(recs, func(i, j int) bool { return recs[i].Name < recs[j].Name })
		taken := map[string]string{}
		for _, c := range recs {
			if c.Kind != connectorKindJira || !c.Enabled {
				continue
			}
			envKey := connectorEnvKey(c.Name)
			if envKey == "" {
				continue
			}
			// Two names that fold to one variable would silently give one the other's
			// credential — the mail/entra collision, left out for the same reason.
			if first, dup := taken[envKey]; dup {
				logging.Warn(logging.WorkerSupervisorFailed,
					"two jira workers share one environment name; the second is not handed to the supervised worker",
					slog.String("connector", c.Name), slog.String("collidesWith", first))
				continue
			}
			endpoint := strings.TrimSpace(c.Endpoint)
			creds, ok := jiraBundleParse(s.resolveConnectorSecret(c.CredentialsRef))
			// A worker with no site, or whose bundle does not resolve (no secret set
			// yet, or neither shape), is left out rather than handed over half-filled:
			// the worker then refuses at startup on a *named* instance missing a field,
			// which would take down every other kind it serves. Left out, it simply is
			// not served, and the Console shows the worker as configured-not-working.
			if endpoint == "" || !ok {
				continue
			}
			taken[envKey] = c.Name
			key := jiraEnvPrefix + envKey + "_"
			env = append(env, key+"URL="+endpoint)
			if creds.Token != "" {
				env = append(env, key+"TOKEN="+creds.Token)
			} else {
				env = append(env, key+"EMAIL="+creds.Email, key+"API_TOKEN="+creds.APIToken)
			}
			addName(c.Name)
			fromStore = true
		}
	})
	// Only a store worker needs CONNECTORS rendered: an operator who set it on the
	// host has it inherited by the child already. When the store does contribute,
	// render the union so a host-named instance is not lost to the override.
	if !fromStore {
		return nil
	}
	return append(env, jiraConnectorsEnv+"="+strings.Join(names, ","))
}

// jiraBundleParse parses the vault bundle a jira worker's credentialsRef names
// (ADR-0201) and returns it reduced to the one shape that will be used. The precedence
// is jira.NewProviderClient's — a Data Center token wins over a Cloud pair — so a
// supervised worker is handed the same product the engine would have talked to rather
// than a second guess at it. ok is false when the bundle is absent, invalid JSON, or
// neither shape.
func jiraBundleParse(raw string) (jiraCredentials, bool) {
	if strings.TrimSpace(raw) == "" {
		return jiraCredentials{}, false
	}
	var c jiraCredentials
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return jiraCredentials{}, false
	}
	switch {
	case strings.TrimSpace(c.Token) != "":
		return jiraCredentials{Token: strings.TrimSpace(c.Token)}, true
	case strings.TrimSpace(c.Email) != "" && strings.TrimSpace(c.APIToken) != "":
		return jiraCredentials{Email: strings.TrimSpace(c.Email), APIToken: strings.TrimSpace(c.APIToken)}, true
	default:
		return jiraCredentials{}, false
	}
}

// adDirectoryEnvLocked renders the Console-configured AD directories a supervised
// worker builds its registry from: ATLAS_AD_CONNECTORS naming them, and per name an
// ATLAS_AD_<NAME>_URL, _BIND_DN and _PASSWORD out of the record and the vault bundle.
//
// "Locked" because it reads the worker store and the vault and must run on the run
// loop; its caller is already inside s.do, so it does not open a second one.
func (s *Server) adDirectoryEnvLocked() []string {
	recs, err := s.connectors.LoadAll()
	if err != nil {
		logging.Warn(logging.WorkerSupervisorFailed, "could not read the worker store for a supervised ad worker",
			slog.String("error", err.Error()))
		return nil
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].Name < recs[j].Name })
	var (
		env   []string
		names []string
	)
	taken := map[string]string{}
	for _, c := range recs {
		if c.Kind != connectorKindAD || !c.Enabled {
			continue
		}
		envKey := connectorEnvKey(c.Name)
		if envKey == "" {
			continue
		}
		// Two names folding to one variable would give one directory the other's
		// service account — the mail/entra/remedy collision, left out for the reason
		// they are, and worse here: the credential opens a forest.
		if first, dup := taken[envKey]; dup {
			logging.Warn(logging.WorkerSupervisorFailed,
				"two active directory workers share one environment name; the second is not handed to the supervised worker",
				slog.String("connector", c.Name), slog.String("collidesWith", first))
			continue
		}
		url := strings.TrimSpace(c.Endpoint)
		creds, ok := adBundleParse(s.resolveConnectorSecret(c.CredentialsRef))
		// A directory with no URL, or whose bundle does not resolve yet, is left out
		// whole rather than handed over half-filled: the worker refuses to start on a
		// *named* directory missing a field, which would take down every other kind it
		// serves. Left out, it is simply not served, and the Console shows the record as
		// configured-not-working.
		if url == "" || !ok {
			continue
		}
		taken[envKey] = c.Name
		key := adDirEnvPrefix + envKey + "_"
		env = append(env,
			key+"URL="+url,
			key+"BIND_DN="+creds.BindDN,
			key+"PASSWORD="+creds.Password)
		names = append(names, c.Name)
	}
	if len(names) == 0 {
		return nil
	}
	return append(env, adConnectorsEnv+"="+strings.Join(names, ","))
}

// adCredentials is the vault bundle an AD worker's credentialsRef names: the
// service account's DN and its password together, so the record holds neither
// (ADR-0041 / I6). Both halves are required — a bind DN with no password is an
// *anonymous* bind to Active Directory, which succeeds and then fails on permissions
// somewhere far from the cause.
type adCredentials struct {
	BindDN   string `json:"bindDN"`
	Password string `json:"password"`
}

// adBundleParse parses that bundle. ok is false when it is absent, invalid JSON, or
// missing a field.
func adBundleParse(raw string) (adCredentials, bool) {
	if strings.TrimSpace(raw) == "" {
		return adCredentials{}, false
	}
	var c adCredentials
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return adCredentials{}, false
	}
	if strings.TrimSpace(c.BindDN) == "" || strings.TrimSpace(c.Password) == "" {
		return adCredentials{}, false
	}
	return c, true
}

// splitConnectorList splits a comma-separated connector-names value, trimming spaces
// and dropping empties — the same shape the worker's own splitAndTrim produces.
func splitConnectorList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// workerTokenEnv is the credential a supervised worker authenticates to this server
// with, when this server requires one.
//
// Without it a supervised worker under --auth is refused at every poll — it holds no
// token, and the job pull is authenticated like every other endpoint. That was a
// hole nobody fell into while offloading was opt-in; making it the default turned it
// into "Atlas starts a worker that cannot do anything" on every authenticated
// server, which is the worst kind of default.
//
// It is this server's own internal token (ADR-0049). The MCP adapter used to share
// it and no longer does — it forwards its caller's credential instead — so a
// supervised worker is now the only holder. That is the right credential for the
// same reason the mail
// configuration is: a supervised worker is this process's own child on this host,
// not a third party, and the token reaches it through its environment rather than
// argv. The principal it resolves to is deliberately not an admin, and a job is
// attributed to the worker's --id rather than to the principal, so the Workers view
// still says which worker did what.
//
// An operator who set ATLAS_TOKEN themselves keeps it: they have chosen an identity
// for their workers, and silently replacing it would undo that choice. **That value
// must be one this server accepts** — an API token, ideally scoped `worker`
// (ADR-0194). It could not be, until API tokens existed: the supervisor
// honoured the variable while principalFor compared a bearer only against the
// internal token, so setting it handed every supervised worker a credential that was
// refused at every poll. checkWorkerTokenEnv says so at startup now, rather than
// leaving it to be discovered one failing job at a time.
// checkWorkerTokenEnv warns when an operator has set ATLAS_TOKEN to something this
// server will not accept. It runs at startup, after the token index is loaded, and
// changes nothing: the operator's choice is still honoured, because overriding it
// silently is what the comment above rejects. What it removes is the silence.
//
// Only the API-token index is consulted, not the internal token: the internal one
// is never served over any endpoint, so an operator cannot have obtained it and a
// match would mean something has gone wrong elsewhere.
func (s *Server) checkWorkerTokenEnv() {
	if !s.authEnabled {
		return
	}
	tok := strings.TrimSpace(os.Getenv("ATLAS_TOKEN"))
	if tok == "" {
		return
	}
	if _, ok := s.apiTokens.match(tok, time.Now().Unix()); ok {
		return
	}
	logging.Warn(logging.AuthWorkerTokenUnknown,
		"ATLAS_TOKEN is set to a value this server does not accept, and supervised workers "+
			"are given it instead of this server's own token — they will be refused at every "+
			"poll. Mint an API token with scope \"worker\" and set ATLAS_TOKEN to that, or "+
			"unset it and let the server hand its workers their credential")
}

func (s *Server) workerTokenEnv() []string {
	if !s.authEnabled || s.internalToken == "" {
		return nil
	}
	if strings.TrimSpace(os.Getenv("ATLAS_TOKEN")) != "" {
		return nil
	}
	return []string{"ATLAS_TOKEN=" + s.internalToken}
}

// superviseEnv is the environment a supervised worker is spawned with: the token it
// authenticates with, and the configuration for the kinds it serves. It returns nil
// when neither applies — an unauthenticated server's --supervise worker inherits
// this process's environment unchanged, exactly as before.
func (s *Server) superviseEnv(spec SuperviseSpec) func() []string {
	render := []func() []string{s.workerTokenEnv}
	provisioned := s.provisionedConnectorKinds()
	for _, k := range spec.Connectors {
		if fn, ok := provisioned[strings.TrimSpace(k)]; ok {
			render = append(render, fn)
		}
	}
	return func() []string {
		var env []string
		for _, fn := range render {
			env = append(env, fn()...)
		}
		return env
	}
}

// refreshSupervisedWorkers restarts any supervised worker whose configuration a
// change has just altered. It must run OFF the run loop — reading that configuration
// goes onto it — so it is a step after the write rather than part of it.
func (s *Server) refreshSupervisedWorkers() {
	if s.supervisor != nil {
		s.supervisor.refresh()
	}
}

// doAndRefresh runs a write on the run loop and then lets the supervised workers pick
// up whatever it changed about them. Every path that can edit a worker or the
// secret behind one goes through it, so there is no route by which a change reaches
// the engine's own registries and not the worker's.
func (s *Server) doAndRefresh(fn func()) {
	s.do(fn)
	s.refreshSupervisedWorkers()
}

// adWorkerEnv renders the Active Directory bind passwords a supervised AD worker
// needs: one variable per secret reference the deployed models actually name,
// resolved through this server's vault or environment.
//
// AD is the kind that made this necessary. Every other provisioned kind is named by
// a *worker record* an operator created, so the engine knows what to hand over by
// reading its own store. An AD task names its own server and its own bind-password
// reference (ADR-0166), so what has to travel is not a configuration but whichever
// references the deployed models happen to make — which the engine knows because it
// compiled them, and a worker cannot know because it has neither the models nor the
// vault.
//
// Only what is deployed is handed over. That is the narrowest set that still works:
// a worker gets the passwords for the directories the running models actually use,
// and not the rest of the vault.
//
// Re-rendered on every spawn and on every refresh, like the mail configuration, so a
// secret an operator adds — or a model that starts naming a new one — reaches the
// worker without Atlas being restarted.
//
// It reads the deployment map and the vault, so it runs on the run-loop goroutine
// (invariant I3), their owner.
func (s *Server) adWorkerEnv() []string {
	var env []string
	s.do(func() {
		// The Console's mockup switch, when an operator has decided there
		// (ADR-0193). No stored record renders nothing, so a
		// server started with ATLAS_AD_MOCK by hand keeps deciding for itself; a
		// stored one decides either way, because a switch that says "off" while the
		// worker still simulates would be lying to the person who flipped it.
		if a, stored, err := s.settings.getADMock(); err != nil {
			logging.Warn(logging.WorkerSupervisorFailed, "could not read the AD mockup switch for a supervised worker",
				slog.String("error", err.Error()))
		} else if stored {
			env = append(env, adMockEnv+"="+boolEnv(a.Enabled))
			// The seed is a file Atlas wrote and Atlas names, not one an operator
			// pointed at: the Console is org-wide, and a path typed there belongs to
			// whichever host happens to run the worker
			// (ADR-0202). The path carries a digest of
			// the seed's content, which is what makes replacing a seed actually reach
			// the worker — refresh() restarts a child when its rendered environment
			// differs, and a fixed name would render the same string for new content.
			if seed := s.settings.adSeedPath(a); seed != "" && a.Enabled {
				env = append(env, adMockSeedEnv+"="+seed)
			}
			// And where to report the forest it ends up holding, so an operator can
			// see it in Operations rather than reconstruct it from the worker's log
			// (ADR-0213). Only when the mockup is
			// on: a worker writing to a real directory has nothing to show here, and
			// a "mock directory" view fed by a live one would be the worst possible
			// thing for this screen to be.
			if a.Enabled && s.superviseURL != "" {
				env = append(env, adMockViewURLEnv+"="+strings.TrimRight(s.superviseURL, "/")+"/api/v1/ad/mock-directory")
			}
		}
		// Directories an operator configured in the Console
		// (ADR-0206), rendered under the names the worker
		// reads — the Remedy shape exactly, because the problem is the same: the URL is
		// in a store and the bind account is in the vault, neither of which a supervised
		// worker can read.
		env = append(env, s.adDirectoryEnvLocked()...)
		env = append(env, s.deployedSecretRefEnvLocked("AD bind-secret", adBindSecretRefs)...)
	})
	return env
}

// deployedSecretRefEnvLocked renders one ATLAS_CONNECTOR_<REF>_TOKEN per secret
// reference the deployed models name, resolved through this server's vault or
// environment. refsOf says which references a compiled process makes; what names them
// in the collision warning.
//
// Two kinds need this and they need it identically. A worker *record* tells the
// engine what to hand over by being in its own store; a task that names its own
// endpoint and its own secret reference (an AD bind, a REST call's auth) tells it
// nothing — what has to travel is whichever references the deployed models happen to
// make, which the engine knows because it compiled them and a worker cannot know
// because it has neither the models nor the vault.
//
// Only what is deployed is handed over: a worker gets the secrets the running models
// actually use, and not the rest of the vault.
//
// It reads the deployment map and the vault, so it runs on the run-loop goroutine
// (invariant I3), their owner — the caller holds it.
func (s *Server) deployedSecretRefEnvLocked(what string, refsOf func(*compiler.CompiledProcess) []string) []string {
	keys := make([]uint64, 0, len(s.deployments))
	for key := range s.deployments {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	var env []string
	taken := map[string]string{}
	for _, key := range keys {
		d := s.deployments[key]
		if d == nil || d.cp == nil {
			continue
		}
		for _, ref := range refsOf(d.cp) {
			envKey := connectorEnvKey(ref)
			if envKey == "" {
				continue
			}
			// Two references that fold to one variable would silently give one of
			// them the other's password — the same collision two mail workers
			// can have, and left out for the same reason. The worker then fails
			// that job naming the reference, which is the honest outcome.
			if first, dup := taken[envKey]; dup {
				if first != ref {
					logging.Warn(logging.WorkerSupervisorFailed,
						"two "+what+" references share one environment name; the second is not handed to the supervised worker",
						slog.String("reference", ref), slog.String("collidesWith", first))
				}
				continue
			}
			taken[envKey] = ref
			// A reference nothing answers to is left out rather than handed over
			// empty: an empty variable reads as a configured blank password, and
			// the worker's own error names the variable an operator must set.
			if secret := s.resolveConnectorSecret(ref); secret != "" {
				env = append(env, adSecretEnv(envKey, secret))
			}
		}
	}
	return env
}

// restWorkerEnv renders the auth secrets a supervised REST worker needs: one variable
// per secret reference the deployed models name (ADR-0233).
//
// A REST task carries its endpoint, method, headers and body in the model, and all of
// that already travels with the job (ADR-0168). What does not travel is the secret
// behind its authSecret reference — the basic password, bearer token, api-key value or
// OAuth2 client secret — because a reference is resolved where it is used. On a
// supervised worker "where it is used" is a child process with no vault, so the engine
// resolves the references its own deployed models make and hands over exactly those.
//
// Without this, defaulting rest onto a worker would have moved every authenticated
// REST task to a process that fails it with "auth secret is not configured".
func (s *Server) restWorkerEnv() []string {
	var env []string
	s.do(func() {
		env = s.deployedSecretRefEnvLocked("REST auth-secret", restAuthSecretRefs)
	})
	return env
}

// scimWorkerEnv renders the auth secrets a supervised SCIM worker needs: one variable
// per secret reference the deployed models name (ADR-0233, slice 6).
//
// The third caller of the same collector, for the third kind that poses REST's
// problem: the provider's base URL and the resource are model data and travel with
// the job, while the credential behind its authSecret is a vault reference, and a
// reference is resolved where it is used.
func (s *Server) scimWorkerEnv() []string {
	var env []string
	s.do(func() {
		env = s.deployedSecretRefEnvLocked("SCIM auth-secret", scimAuthSecretRefs)
	})
	return env
}

// soapWorkerEnv renders the auth secrets a supervised SOAP worker needs: one variable
// per secret reference the deployed models name (ADR-0233, slice 4).
//
// It is restWorkerEnv with a different job type, because a SOAP task poses REST's
// problem exactly: the endpoint, the SOAPAction and the envelope body are model data
// and travel with the job, while the credential behind its authSecret is a vault
// reference, and a reference is resolved where it is used.
func (s *Server) soapWorkerEnv() []string {
	var env []string
	s.do(func() {
		env = s.deployedSecretRefEnvLocked("SOAP auth-secret", soapAuthSecretRefs)
	})
	return env
}

// restAuthSecretRefs returns the auth-secret references a compiled process's REST
// tasks name.
func restAuthSecretRefs(cp *compiler.CompiledProcess) []string {
	return authSecretRefs(cp, compiler.RestJobTypeIndex)
}

// scimAuthSecretRefs returns the same for its SCIM tasks.
func scimAuthSecretRefs(cp *compiler.CompiledProcess) []string {
	return authSecretRefs(cp, compiler.ScimJobTypeIndex)
}

// soapAuthSecretRefs returns the same for its SOAP tasks. REST and SOAP author their
// credentials identically — one [compiler.RestAuth] blob naming a vault reference —
// so they are one function called twice rather than two that drift.
func soapAuthSecretRefs(cp *compiler.CompiledProcess) []string {
	return authSecretRefs(cp, compiler.SoapJobTypeIndex)
}

// authSecretRefs returns the auth-secret references the compiled process's tasks of
// one job type name, in node order. A task calling an open endpoint names none, and so
// does one whose auth carries only model data (a username without a password reference
// is not a secret to hand over).
func authSecretRefs(cp *compiler.CompiledProcess, jobType int32) []string {
	var out []string
	for id := int32(0); int(id) < cp.NodeCount(); id++ {
		n := cp.Node(id)
		if n.Type != compiler.TypeConnectorTask {
			continue
		}
		d := cp.ConnectorTask(n.Detail)
		if d == nil || d.JobType != jobType {
			continue
		}
		raw := strings.TrimSpace(cp.Intern(d.Auth))
		if raw == "" {
			continue
		}
		var auth compiler.RestAuth
		if err := json.Unmarshal([]byte(raw), &auth); err != nil {
			// The engine wrote this JSON at deploy time, so a parse failure is a bug
			// rather than an operator's mistake — skipping it hands over one secret
			// less, and the worker's own error names what is missing.
			continue
		}
		if ref := strings.TrimSpace(auth.SecretRef); ref != "" {
			out = append(out, ref)
		}
	}
	return out
}

// ldapWorkerEnv renders the directory credentials a supervised LDAP worker needs: one
// variable per secret reference the deployed models name (ADR-0233, slice 3).
//
// It is AD's handover, and it is one function call rather than a second copy of it
// because the two kinds pose the identical problem: an LDAP task authors its own
// server and bind DN (ADR-0154) and both travel with the job, while its bind password
// and its client certificate are *vault references*, and a reference is resolved where
// it is used. On a supervised worker that is a child process with no vault, so the
// engine resolves the references its own deployed models make and hands over exactly
// those.
//
// Both flavours are covered, because both fail the same way if they are not: a
// certificate reference nothing answers to is a bind that cannot present an identity,
// which is not a better outcome than a password reference nothing answers to.
func (s *Server) ldapWorkerEnv() []string {
	var env []string
	s.do(func() {
		env = s.deployedSecretRefEnvLocked("LDAP secret", ldapSecretRefs)
	})
	return env
}

// ldapSecretRefs returns the bind-password and client-certificate references a
// compiled process's LDAP tasks name, in node order. An anonymous bind over plain
// LDAP names neither; a task may name either or both.
func ldapSecretRefs(cp *compiler.CompiledProcess) []string {
	var out []string
	for id := int32(0); int(id) < cp.NodeCount(); id++ {
		n := cp.Node(id)
		if n.Type != compiler.TypeConnectorTask {
			continue
		}
		d := cp.ConnectorTask(n.Detail)
		if d == nil || d.JobType != compiler.LdapJobTypeIndex {
			continue
		}
		for _, ref := range []string{cp.Intern(d.LdapBindSecret), cp.Intern(d.LdapClientCertSecret)} {
			if ref = strings.TrimSpace(ref); ref != "" {
				out = append(out, ref)
			}
		}
	}
	return out
}

// adSecretEnv is one bind password under the name the worker reads it by. It is the
// same ATLAS_CONNECTOR_<REF>_TOKEN an operator sets by hand for an external worker —
// there is no private channel between a supervised worker and its parent.
func adSecretEnv(envKey, secret string) string {
	return "ATLAS_CONNECTOR_" + envKey + "_TOKEN=" + secret
}

// adBindSecretRefs returns the bind-password references a compiled process's AD
// tasks name, in node order. A task that binds anonymously names none.
func adBindSecretRefs(cp *compiler.CompiledProcess) []string {
	var out []string
	for id := int32(0); int(id) < cp.NodeCount(); id++ {
		n := cp.Node(id)
		if n.Type != compiler.TypeConnectorTask {
			continue
		}
		d := cp.ConnectorTask(n.Detail)
		if d == nil || d.JobType != compiler.AdJobTypeIndex {
			continue
		}
		if ref := strings.TrimSpace(cp.Intern(d.AdBindSecret)); ref != "" {
			out = append(out, ref)
		}
	}
	return out
}

// Environment variables the AD worker reads its mockup configuration from. They are
// the same names an operator sets by hand for an external worker (ADR-0181) — there
// is no private channel between a supervised worker and its parent, so what the
// Console writes and what a hand-run worker reads are one contract.
const (
	adMockEnv     = "ATLAS_AD_MOCK"
	adMockSeedEnv = "ATLAS_AD_MOCK_SEED"
	// adMockViewURLEnv is where a mock worker posts the forest it holds: this
	// server's own API, so the Console shows a directory that exists only in the
	// worker's memory (ADR-0213). It is the mail
	// outbox's shape — the worker sends, because a server cannot dial into every
	// network a worker sits in.
	adMockViewURLEnv = "ATLAS_AD_MOCK_VIEW_URL"
	// sqlMockViewURLEnv is where a mock SQL worker posts the journal it answered. It
	// is spelled out here rather than imported from the worker package for the reason
	// the drivers are kept out of this one (ADR-0173/ADR-0220): importing `worker` to
	// reach one string would link three database drivers into a package that never
	// opens a database. A test holds the two spellings together instead.
	sqlMockViewURLEnv = "ATLAS_SQL_MOCK_VIEW_URL"
	// adDirEnvPrefix and adConnectorsEnv are where a supervised AD worker reads the
	// directories an operator configured (ADR-0206) — the
	// same names an operator sets by hand for an external worker, because there is no
	// private channel between engine and child (ADR-0157).
	adDirEnvPrefix  = "ATLAS_AD_"
	adConnectorsEnv = adDirEnvPrefix + "CONNECTORS"
)

// boolEnv renders a switch as the yes/no the worker parses.
func boolEnv(on bool) string {
	if on {
		return "1"
	}
	return "0"
}
