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
// it a connector's configuration through that environment is the operator setting the
// worker up, done by the program instead of by hand — the secret goes neither over
// the wire nor into a payload, and an `atlas worker` run by hand is configured
// exactly the same way, from the same variables.
//
// That distinction is why this file provisions only *supervised* children. An
// external worker gets nothing from here; its operator sets its environment, which is
// the arrangement ADR-0168 is actually about.
//
// It also makes mail the first *managed* kind Atlas can offload by default: mail's
// endpoint and password live in the connector store rather than the environment, and
// until the engine could hand them over, offloading mail meant handing every mail
// task to a worker with no mailbox.

// Environment variables a supervised worker reads its mail configuration from. They
// are the same names an operator sets by hand for an external worker — there is no
// private channel between a supervised worker and its parent, because a private
// channel is how the supervised path would quietly become the only tested one
// (ADR-0157).
const (
	// mailConnectorsEnv lists the connector names this worker can send through.
	mailConnectorsEnv = "ATLAS_MAIL_CONNECTORS"
	// mailOutboxURLEnv is where a preview connector delivers: this server's own
	// outbox, over the API. A worker frames the message and posts it back, so the
	// operator reads it in Operations › Outbox as if it had never left (ADR-0150).
	mailOutboxURLEnv = "ATLAS_MAIL_OUTBOX_URL"
)

// mailWorkerEnv renders this server's mail connectors as the environment a
// supervised worker builds the identical clients from. It is re-read on every spawn,
// so an operator who adds a connector in the Console and presses Restart in the
// Workers view has a worker that can send through it — without restarting Atlas.
//
// It reads the connector store and the vault, so it runs on the run-loop goroutine
// (their owner), like buildMailClients does.
func (s *Server) mailWorkerEnv() []string {
	var (
		names []string
		env   []string
	)
	s.do(func() {
		recs, err := s.connectors.LoadAll()
		if err != nil {
			logging.Warn(logging.WorkerSupervisorFailed, "could not read the connector store for a supervised worker",
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
			// worker then reports it as a connector it does not hold, and the Workers
			// view shows it among the names served nowhere (ADR-0168).
			if first, dup := taken[key]; dup {
				logging.Warn(logging.WorkerSupervisorFailed,
					"two mail connectors share one environment name; the second is not handed to the supervised worker",
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

// mailConnectorEnv is one connector's configuration as environment variables: exactly
// the fields [mail.ProviderConfig] is built from, so the worker's client and the
// engine's are the same client built from the same values.
//
// The secret is written last and only when there is one, so a connector with no
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
		// AD is not a managed kind — no connector record, no store entry — but its
		// bind-password *reference* can resolve out of the vault, which a supervised
		// worker cannot read either. So it is provisioned for the same reason mail
		// is, and defaulting it without this would have moved every vault-backed AD
		// task to a worker holding nothing to bind with.
		"ad": s.adWorkerEnv,
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
		// account live in the connector store and the vault (ADR-0106), so a supervised
		// worker holding neither could serve no Remedy task at all.
		connectorKindRemedy:   s.remedyWorkerEnv,
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
// It is the AD story with a connector name in place of a bind-secret reference. The
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
			logging.Warn(logging.WorkerSupervisorFailed, "could not read the connector store for a supervised entra worker",
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
					"two entra connectors share one environment name; the second is not handed to the supervised worker",
					slog.String("connector", c.Name), slog.String("collidesWith", first))
				continue
			}
			// A bundle that does not resolve (no secret set yet, or malformed) is left out
			// rather than handed over half-filled: the worker then simply does not build
			// that tenant, and the Console shows the connector as configured-not-working
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
// connector's credentialsRef: the tenant id, client id and client secret together, so
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
const (
	remedyEnvPrefix     = "ATLAS_REMEDY_"
	remedyConnectorsEnv = remedyEnvPrefix + "CONNECTORS"
)

// remedyWorkerEnv renders this server's Remedy connectors as the environment a
// supervised worker builds the identical clients from: the AR System base URL and the
// {username,password} bundle behind each connector's credentialsRef.
//
// It is mail's story with an ITSM instance in place of a mailbox (ADR-0106/0168). The
// base URL and the service account live in the connector store and the vault, which a
// supervised worker can read no more than it can read the engine's memory — so
// offloading Remedy without this would hand every Remedy task to a worker with no
// instance to file against.
//
// A connector an operator configured on the host is left untouched and kept in the
// rendered list: the child inherits ATLAS_REMEDY_<NAME>_* already, and dropping its
// name would let a store connector silently take the whole list away from it.
//
// It reads the connector store and the vault, so it runs on the run-loop goroutine
// (their owner, invariant I3), like buildRemedyClients does.
func (s *Server) remedyWorkerEnv() []string {
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
	// Instances an operator set directly on the host: inherited by the child as they
	// are, so nothing is rendered for them — they are only kept in the list below.
	for _, name := range splitConnectorList(os.Getenv(remedyConnectorsEnv)) {
		addName(name)
	}
	s.do(func() {
		recs, err := s.connectors.LoadAll()
		if err != nil {
			logging.Warn(logging.WorkerSupervisorFailed, "could not read the connector store for a supervised remedy worker",
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
					"two remedy connectors share one environment name; the second is not handed to the supervised worker",
					slog.String("connector", c.Name), slog.String("collidesWith", first))
				continue
			}
			endpoint := strings.TrimSpace(c.Endpoint)
			creds, ok := remedyBundleParse(s.resolveConnectorSecret(c.CredentialsRef))
			// A connector with no endpoint, or whose bundle does not resolve (no secret
			// set yet, or malformed), is left out rather than handed over half-filled:
			// the worker then refuses at startup on a *named* instance missing a field,
			// which would take down every other kind it serves. Left out, it simply is
			// not served, and the Console shows the connector as configured-not-working.
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
	// Only a store connector needs CONNECTORS rendered: an operator who set it on the
	// host has it inherited by the child already. When the store does contribute,
	// render the union so a host-named instance is not lost to the override.
	if !fromStore {
		return nil
	}
	return append(env, remedyConnectorsEnv+"="+strings.Join(names, ","))
}

// remedyBundleParse parses the vault bundle a remedy connector's credentialsRef names
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

// adDirectoryEnvLocked renders the Console-configured AD directories a supervised
// worker builds its registry from: ATLAS_AD_CONNECTORS naming them, and per name an
// ATLAS_AD_<NAME>_URL, _BIND_DN and _PASSWORD out of the record and the vault bundle.
//
// "Locked" because it reads the connector store and the vault and must run on the run
// loop; its caller is already inside s.do, so it does not open a second one.
func (s *Server) adDirectoryEnvLocked() []string {
	recs, err := s.connectors.LoadAll()
	if err != nil {
		logging.Warn(logging.WorkerSupervisorFailed, "could not read the connector store for a supervised ad worker",
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
				"two active directory connectors share one environment name; the second is not handed to the supervised worker",
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

// adCredentials is the vault bundle an AD connector's credentialsRef names: the
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
// up whatever it changed about them. Every path that can edit a connector or the
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
// a *connector record* an operator created, so the engine knows what to hand over by
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
			// (ADR-draft-atlas-manages-the-ad-mock-seed). The path carries a digest of
			// the seed's content, which is what makes replacing a seed actually reach
			// the worker — refresh() restarts a child when its rendered environment
			// differs, and a fixed name would render the same string for new content.
			if seed := s.settings.adSeedPath(a); seed != "" && a.Enabled {
				env = append(env, adMockSeedEnv+"="+seed)
			}
		}
		// Directories an operator configured in the Console
		// (ADR-draft-ad-as-a-console-connector), rendered under the names the worker
		// reads — the Remedy shape exactly, because the problem is the same: the URL is
		// in a store and the bind account is in the vault, neither of which a supervised
		// worker can read.
		env = append(env, s.adDirectoryEnvLocked()...)
		keys := make([]uint64, 0, len(s.deployments))
		for key := range s.deployments {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

		taken := map[string]string{}
		for _, key := range keys {
			d := s.deployments[key]
			if d == nil || d.cp == nil {
				continue
			}
			for _, ref := range adBindSecretRefs(d.cp) {
				envKey := connectorEnvKey(ref)
				if envKey == "" {
					continue
				}
				// Two references that fold to one variable would silently give one of
				// them the other's password — the same collision two mail connectors
				// can have, and left out for the same reason. The worker then fails
				// that job naming the reference, which is the honest outcome.
				if first, dup := taken[envKey]; dup {
					if first != ref {
						logging.Warn(logging.WorkerSupervisorFailed,
							"two AD bind-secret references share one environment name; the second is not handed to the supervised worker",
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
	})
	return env
}

// adSecretEnv is one bind password under the name the worker reads it by. It is the
// same ATLAS_CONNECTOR_<REF>_TOKEN an operator sets by hand for an external worker —
// there is no private channel between a supervised worker and its parent.
func adSecretEnv(envKey, secret string) string {
	return "ATLAS_CONNECTOR_" + envKey + "_TOKEN=" + secret
}

// adBindSecretRefs returns the bind-password references a compiled process's AD
// connector tasks name, in node order. A task that binds anonymously names none.
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
	// adDirEnvPrefix and adConnectorsEnv are where a supervised AD worker reads the
	// directories an operator configured (ADR-draft-ad-as-a-console-connector) — the
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
