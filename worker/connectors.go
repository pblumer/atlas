package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/ad"
	"github.com/pblumer/atlas/connector/csvimport"
	"github.com/pblumer/atlas/connector/ldif"
	"github.com/pblumer/atlas/connector/mail"
	"github.com/pblumer/atlas/connector/nettimeout"
	"github.com/pblumer/atlas/connector/rest"
	"github.com/pblumer/atlas/connector/script"
	"github.com/pblumer/atlas/connector/sqldb"
	"github.com/pblumer/atlas/connector/webscrape"
	"github.com/pblumer/atlas/logging"
)

// Connector kinds this worker can serve out of process (ADR-0168).
//
// A connector job arrives already resolved: the engine found the task's detail in
// the compiled process and evaluated it against the instance's variables, because it
// is the only one who can, and what travels is plain values. So the code here does
// only the work itself — for CSV, parsing — and needs nothing from the engine but
// the payload it was handed.
//
// CSV import is the first kind to move because no credential is involved in it at
// all, so the mechanism could be built and reviewed before any secret rode on it.
// Mail is the first kind where one does: the engine resolves the message, and the
// SMTP host and password come from *this process's* environment. That is ADR-0168's
// decision made concrete — a worker can serve a provider the engine has no
// configuration for, and reach a network the engine does not sit in.
//
// Credentials are read from the environment rather than taken as flags because argv
// is readable by anyone who can list processes.

// BuiltinConnectors returns handlers for the named connector kinds, keyed by the job
// type each serves. env looks up this worker's configuration; pass os.Getenv outside
// of tests.
//
// An unknown kind name yields nothing, and the caller compares counts to catch it: a
// worker is configured from its own command line, so a name it does not implement is
// a mistake to report at startup rather than a queue to lease work from and then
// fail. A kind that *is* implemented but misconfigured returns an error here for the
// same reason — the operator is still watching at startup, and a per-job discovery
// would spend a retry budget learning what was knowable before the first poll.
func BuiltinConnectors(env func(string) string, kinds ...string) (Connectors, error) {
	if env == nil {
		env = func(string) string { return "" }
	}
	built := Connectors{Handlers: map[string]Exec{}}
	for _, kind := range kinds {
		switch kind {
		case "csv":
			built.Handlers[compiler.CsvImportJobType] = ExecFunc(runCSV)
		case "ldif":
			built.Handlers[compiler.LdifJobType] = ExecFunc(runLdif)
		case "webscrape":
			// Nothing to configure: the reach is the worker's network position, not a
			// credential, so there is no environment to read and nothing to report as
			// a held connector name.
			client := webscrape.NewHTTPClient()
			built.Handlers[compiler.WebScrapeJobType] = ExecFunc(func(ctx context.Context, j Job) (map[string]any, error) {
				return runWebScrape(ctx, j, client)
			})
		case "script":
			// One handler per language, because a worker subscribes per job type: a
			// Python worker and a PowerShell worker are simply two workers, each on a
			// machine that has that interpreter installed. Nothing to configure — what
			// this worker contributes is the interpreter, not a credential.
			for name, lang := range map[string]script.Lang{
				compiler.PwshJobType:   script.PowerShell,
				compiler.PythonJobType: script.Python,
				compiler.JsJobType:     script.JavaScript,
			} {
				exec := script.New(lang)
				built.Handlers[name] = ExecFunc(func(ctx context.Context, j Job) (map[string]any, error) {
					return runScript(ctx, j, exec)
				})
			}
		case "rest":
			// The authored auth arrives with the job; the secret behind its reference
			// is read here, from this process's own environment, under the same
			// ATLAS_CONNECTOR_<REF>_TOKEN name the engine uses — so a secret moves by
			// being set on the worker instead, not by being spelled differently.
			client := rest.NewHTTPClient()
			secret := rest.SecretResolver(func(ref string) string {
				return env("ATLAS_CONNECTOR_" + envFold(ref) + "_TOKEN")
			})
			built.Handlers[compiler.RestJobType] = ExecFunc(func(ctx context.Context, j Job) (map[string]any, error) {
				return runREST(ctx, j, client, secret)
			})
		case "mail":
			reg, names, err := mailRegistryFromEnv(env)
			if err != nil {
				return Connectors{}, err
			}
			if reg == nil {
				// Told to serve mail, holding no connector to send through. Not an
				// error: this worker very likely serves other kinds too, and killing
				// those because no mailbox is configured yet is how a server started
				// with nothing configured — the case the opt-out default is for —
				// would come up with no worker at all. It simply does not subscribe
				// to mail, so mail tasks wait for a worker that can serve them rather
				// than being leased and failed. Configure one and the supervisor
				// brings this worker back holding it.
				//
				// A worker serving *only* mail still stops at startup, because it
				// then has no handler at all and `atlas worker` has nothing to do.
				built.Unconfigured = append(built.Unconfigured, kind)
				continue
			}
			built.Names = append(built.Names, names...)
			built.Handlers[compiler.MailJobType] = ExecFunc(func(ctx context.Context, j Job) (map[string]any, error) {
				return RunMailJob(ctx, j, reg)
			})
		case "ad":
			// Two shapes at once, because a model may carry either
			// (ADR-draft-ad-as-a-console-connector): directories an operator configured
			// in the Console, addressed by name, and tasks that carry their own url
			// with a per-task bind-password reference. A worker holding no directories
			// still serves the second kind, which is every model written before records
			// existed.
			dirs, names, err := adDirectoriesFromEnv(env)
			if err != nil {
				return Connectors{}, err
			}
			built.Names = append(built.Names, names...)
			// And the one thing that is this worker's own: whether it reaches a real
			// directory or a mock one, and the entries a mock starts from.
			dialer, mock, err := adDialerFromEnv(env)
			if err != nil {
				return Connectors{}, err
			}
			if mock != nil {
				announceADMock(mock, env(adMockSeedEnv))
			}
			secret := adSecretFromEnv(env)
			built.Handlers[compiler.AdJobType] = ExecFunc(func(ctx context.Context, j Job) (map[string]any, error) {
				return RunADJob(ctx, j, dialer, secret, dirs)
			})
		case "entra":
			reg, names, err := entraRegistryFromEnv(env)
			if err != nil {
				return Connectors{}, err
			}
			if reg == nil {
				// Told to serve Entra, holding no tenant. Not an error, for the reason
				// mail's identical branch above is not: this worker may serve other
				// kinds, and a kind supervised by default must park rather than fail
				// on a server where nobody has configured a tenant yet.
				built.Unconfigured = append(built.Unconfigured, kind)
				continue
			}
			built.Names = append(built.Names, names...)
			built.Handlers[compiler.EntraJobType] = ExecFunc(func(ctx context.Context, j Job) (map[string]any, error) {
				return RunEntraJob(ctx, j, reg)
			})
		case "remedy":
			reg, names, err := remedyRegistryFromEnv(env)
			if err != nil {
				return Connectors{}, err
			}
			if reg == nil {
				// Told to serve Remedy, holding no AR System to file against. Not an
				// error, for the reason mail's and Entra's identical branches above are
				// not: this worker very likely serves other kinds, and an ITSM instance
				// nobody has configured yet must park its tasks rather than take down
				// the kinds that are configured.
				built.Unconfigured = append(built.Unconfigured, kind)
				continue
			}
			built.Names = append(built.Names, names...)
			built.Handlers[compiler.RemedyJobType] = ExecFunc(func(ctx context.Context, j Job) (map[string]any, error) {
				return RunRemedyJob(ctx, j, reg)
			})
		case "mssql", "mariadb", "postgres":
			// The three SQL products (ADR-0173). Unlike the
			// kinds above them they have no in-process counterpart to fall back to, so
			// a worker is the only way a SQL task ever runs — which is why a
			// misconfigured one is refused here, at startup, rather than discovered a
			// retry budget later.
			p, ok := sqldb.ProductByName(kind)
			if !ok {
				return Connectors{}, sqldb.UnknownProduct(kind)
			}
			reg, names, err := sqlRegistryFromEnv(env, p)
			if err != nil {
				return Connectors{}, err
			}
			if reg == nil {
				// Told to serve this product, holding no database — the state every
				// server starts in. Parks like mail and Entra above rather than
				// failing; a connector configured in the Console brings it back.
				built.Unconfigured = append(built.Unconfigured, kind)
				continue
			}
			built.Names = append(built.Names, names...)
			built.Handlers[p.JobType] = ExecFunc(func(ctx context.Context, j Job) (map[string]any, error) {
				return RunSQLJob(ctx, j, reg)
			})
		default:
			return Connectors{}, fmt.Errorf("worker: --connector names a kind this worker does not implement: %q (have: %s)",
				kind, strings.Join(KnownConnectorKinds(), ", "))
		}
	}
	sort.Strings(built.Names)
	return built, nil
}

// Connectors is what a worker was configured to serve: the handlers, keyed by the
// job type each answers, and the connector *names* this worker holds credentials
// for.
//
// The names are reported to the engine on every poll, because they are the half of
// "can this connector be served" that only the worker knows — once a kind is
// offloaded the engine holds no credential for it and cannot read another process's
// environment. The Workers view subtracts them from what deployed models reference
// to show the names configured nowhere (ADR-0168).
type Connectors struct {
	Handlers map[string]Exec
	Names    []string
	// Unconfigured are kinds this worker was asked to serve and holds no
	// configuration for, so it does not subscribe to them. It is reported at startup
	// rather than swallowed: "mail is not being served here" is the answer to why a
	// mail task is waiting, and the worker's log is in the Workers console.
	Unconfigured []string
}

// KnownConnectorKinds are the kinds [BuiltinConnectors] implements, for the error a
// misspelling produces. A kind may serve several job types — script serves one per
// language — which is why a caller must not check this by counting handlers.
func KnownConnectorKinds() []string {
	return []string{"ad", "csv", "entra", "ldif", "mail", "mariadb", "mssql", "postgres", "remedy", "rest", "script", "webscrape"}
}

// mailEnvPrefix is where a mail worker's credentials live.
const mailEnvPrefix = "ATLAS_MAIL_"

// mailOutboxURLEnv is where a preview connector delivers what it framed: the API of
// the Atlas whose Operations › Outbox the operator is watching. A preview connector
// is the one mail provider that produces something only the engine can show, so it is
// the one that needs an address back (ADR-0150/0168).
const mailOutboxURLEnv = mailEnvPrefix + "OUTBOX_URL"

// mailRegistryFromEnv builds the mail connectors this worker holds.
// ATLAS_MAIL_CONNECTORS lists the names, and each name contributes its own
// configuration under ATLAS_MAIL_<NAME>_.
//
// There are two ways to write that configuration and both are supported on purpose.
// _PROVIDER names one of Atlas's mail providers and is described by _ENDPOINT,
// _SENDER and _SECRET — the same four values [mail.ProviderConfig] takes, so a worker
// builds the identical client the engine would have, for SMTP, Gmail, Microsoft Graph
// or preview alike. It is what an engine supervising this worker writes. Without
// _PROVIDER the older SMTP-only form applies: _ENDPOINT plus _USERNAME, _PASSWORD and
// _FROM, which is what an operator's hand-written worker environment says today and
// which keeps working exactly as it did.
//
// The names are the ones models reference, so the list is also the answer to "what
// can this worker actually send through".
func mailRegistryFromEnv(env func(string) string) (*mail.Registry, []string, error) {
	names := splitAndTrim(env(mailEnvPrefix + "CONNECTORS"))
	if len(names) == 0 {
		// No connector to send through. The caller decides what that means — see the
		// "mail" arm of BuiltinConnectors — because it depends on what else this
		// worker was asked to serve.
		return nil, nil, nil
	}
	reg := mail.NewRegistry()
	for _, name := range names {
		client, err := mailClientFromEnv(env, name)
		if err != nil {
			return nil, nil, err
		}
		reg.Register(name, client)
	}
	return reg, names, nil
}

// mailClientFromEnv builds one connector's client from its environment.
func mailClientFromEnv(env func(string) string, name string) (mail.Client, error) {
	key := mailEnvPrefix + envFold(name) + "_"
	if provider := strings.TrimSpace(env(key + "PROVIDER")); provider != "" {
		client, err := mail.NewProviderClient(mail.ProviderConfig{
			Provider: provider,
			Endpoint: env(key + "ENDPOINT"),
			Sender:   env(key + "SENDER"),
			Secret:   env(key + "SECRET"),
			Name:     name,
			Outbox:   previewSink(env),
		})
		if err != nil {
			return nil, fmt.Errorf("worker: mail connector %q: %w", name, err)
		}
		return client, nil
	}
	endpoint := env(key + "ENDPOINT")
	if endpoint == "" {
		return nil, fmt.Errorf("worker: mail connector %q has no endpoint: set %sENDPOINT", name, key)
	}
	return mail.NewSMTPClient(mail.Connector{
		Endpoint: endpoint,
		Username: env(key + "USERNAME"),
		Password: env(key + "PASSWORD"),
		From:     env(key + "FROM"),
	}), nil
}

// previewSink is where this worker's preview connectors deliver, or nil when it was
// told no address — in which case [mail.NewProviderClient] refuses the connector at
// startup, naming it, rather than letting a preview task discover at send time that
// its message went nowhere.
func previewSink(env func(string) string) mail.Sink {
	url := strings.TrimSpace(env(mailOutboxURLEnv))
	if url == "" {
		return nil
	}
	return &httpOutbox{url: url, token: strings.TrimSpace(env("ATLAS_TOKEN")), client: nettimeout.HTTPClient()}
}

// httpOutbox is a [mail.Sink] that posts a framed message to an Atlas server's
// preview outbox. It is the whole of what a preview connector needs from out here:
// the framing is [mail.PreviewClient]'s and is identical in both processes, so what a
// preview run proves about a message stays true wherever it ran.
type httpOutbox struct {
	url    string
	token  string
	client *http.Client
}

// Deliver posts the message and reports whether it arrived. A preview whose outbox is
// unreachable fails its job, which is the honest outcome: the operator went looking
// in Operations › Outbox and the message is not there.
func (o *httpOutbox) Deliver(m mail.OutboxMessage) error {
	body, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("mail: preview: encode the message: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, o.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mail: preview: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if o.token != "" {
		req.Header.Set("Authorization", "Bearer "+o.token)
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return fmt.Errorf("mail: preview: deliver to the outbox at %s: %w", o.url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("mail: preview: the outbox at %s answered %s", o.url, resp.Status)
	}
	return nil
}

// envFold turns a connector name into the environment-variable form of itself:
// upper case, with anything that cannot appear in a variable name becoming an
// underscore. It is applied the one way, and the error messages quote the result, so
// an operator sets exactly the variable that was looked for.
func envFold(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	pendingSep := false
	for _, r := range strings.ToUpper(name) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			if pendingSep && b.Len() > 0 {
				b.WriteByte('_')
			}
			pendingSep = false
			b.WriteRune(r)
			continue
		}
		pendingSep = true
	}
	return b.String()
}

// splitAndTrim reads a comma-separated list, dropping blanks so a trailing comma
// does not become a nameless connector.
func splitAndTrim(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// RunMailJob sends a resolved mail job through a registry the caller owns. It is
// exported because the environment is only the *default* place a worker's mail
// credentials come from: a caller embedding this package can build a registry from a
// vault, a file, or an instance profile and still get the identical send. It shares
// [mail.Run] with the in-process path, so no two of those can disagree about what a
// resolved mail task means — only about which credentials are in reach.
func RunMailJob(ctx context.Context, j Job, reg *mail.Registry) (map[string]any, error) {
	if j.Connector == nil {
		return nil, fmt.Errorf("mail: the job carried no resolved connector detail; is this server offloading the mail kind?")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task mail.Job
	if err := json.Unmarshal(raw, &task); err != nil {
		return nil, fmt.Errorf("mail: cannot read the resolved detail: %w", err)
	}
	// A mail task writes no result variable: the send is the whole of its effect.
	return nil, mail.Run(ctx, task, reg)
}

// runCSV parses a resolved CSV-import job and returns the variables it completes
// with. It shares [csvimport.Run] with the in-process path, so the two cannot
// disagree about defaults, validation, or what a headerless file's column list
// means.
func runCSV(_ context.Context, j Job) (map[string]any, error) {
	if j.Connector == nil {
		return nil, fmt.Errorf("csv: the job carried no resolved connector detail; is this server offloading the csv kind?")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task csvimport.Job
	if err := json.Unmarshal(raw, &task); err != nil {
		return nil, fmt.Errorf("csv: cannot read the resolved detail: %w", err)
	}
	res, err := csvimport.Run(task)
	if err != nil {
		return nil, err
	}
	// Result decides what a job completes with, so a read's rows and a write's
	// rendered file take the same path here as in the engine.
	return res.Variables()
}

// runLdif reads or writes a resolved directory-file job. It shares [ldif.Run] and
// [ldif.Result] with the in-process path, so the two cannot disagree about what a
// read's entries or a write's file look like.
func runLdif(_ context.Context, j Job) (map[string]any, error) {
	if j.Connector == nil {
		return nil, fmt.Errorf("ldif: the job carried no resolved connector detail; is this server offloading the ldif kind?")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task ldif.Job
	if err := json.Unmarshal(raw, &task); err != nil {
		return nil, fmt.Errorf("ldif: cannot read the resolved detail: %w", err)
	}
	res, err := ldif.Run(task)
	if err != nil {
		return nil, err
	}
	return res.Variables(), nil
}

// runWebScrape fetches a resolved scrape and returns the variable it completes with.
// It shares [webscrape.Run] with the in-process path, so the two cannot disagree
// about what a selector or an attribute means.
func runWebScrape(ctx context.Context, j Job, client webscrape.Client) (map[string]any, error) {
	if j.Connector == nil {
		return nil, fmt.Errorf("webscrape: the job carried no resolved connector detail; is this server offloading the webscrape kind?")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task webscrape.Job
	if err := json.Unmarshal(raw, &task); err != nil {
		return nil, fmt.Errorf("webscrape: cannot read the resolved detail: %w", err)
	}
	res, err := webscrape.Run(ctx, task, client)
	if err != nil {
		return nil, err
	}
	if res.ResultVariable == "" {
		return nil, nil // the task writes nothing back
	}
	// The values travel as a plain list; the engine stores it the same way the
	// in-process path does.
	items := make([]any, len(res.Values))
	for i, v := range res.Values {
		items[i] = v
	}
	return map[string]any{res.ResultVariable: items}, nil
}

// runScript runs a resolved script task through this worker's interpreter. It shares
// [script.Run] with the in-process path, so the two cannot disagree about what a
// script sees or what its output means.
func runScript(ctx context.Context, j Job, exec script.Exec) (map[string]any, error) {
	if j.Connector == nil {
		return nil, fmt.Errorf("script: the job carried no resolved source; is this server offloading the script kind?")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task script.Job
	if err := json.Unmarshal(raw, &task); err != nil {
		return nil, fmt.Errorf("script: cannot read the resolved detail: %w", err)
	}
	res, err := script.Run(ctx, task, exec)
	if err != nil {
		return nil, err
	}
	if res.ResultVariable == "" {
		return nil, nil // the task writes nothing back
	}
	return map[string]any{res.ResultVariable: res.Output}, nil
}

// runREST calls a resolved REST task with this worker's own credential. It shares
// [rest.Run] with the in-process path; only whose secret store is in reach differs.
func runREST(ctx context.Context, j Job, client rest.Client, secret rest.SecretResolver) (map[string]any, error) {
	if j.Connector == nil {
		return nil, fmt.Errorf("rest: the job carried no resolved connector detail; is this server offloading the rest kind?")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task rest.Job
	if err := json.Unmarshal(raw, &task); err != nil {
		return nil, fmt.Errorf("rest: cannot read the resolved detail: %w", err)
	}
	// No token provider: OAuth2 client-credentials would need one, and a worker that
	// silently sent an unauthenticated call would be worse than one that says so.
	res, err := rest.Run(ctx, task, client, secret, nil)
	if err != nil {
		return nil, err
	}
	if res.ResultVariable == "" {
		return nil, nil // the model discards the response
	}
	return map[string]any{res.ResultVariable: res.Body}, nil
}

// announceADMock says once, at startup, that this worker writes to no directory, and
// then says what it does instead — one line per simulated operation.
//
// Both matter, and the warning matters most: a worker in mock mode looks exactly like
// a working one from the engine's side, because it completes every job. The line in
// its log is the only place the difference is visible, and the Workers console is
// where an operator reads it (ADR-0157).
func announceADMock(mock *ad.MockDirectory, seed string) {
	// The *seed* count, not the entry count: every directory this worker simulates
	// starts from these, and none of them exists until a job dials one.
	attrs := []slog.Attr{slog.Int("seeded", len(mock.Seed()))}
	if seed = strings.TrimSpace(seed); seed != "" {
		attrs = append(attrs, slog.String("seed", seed))
	}
	logging.Warn(logging.ADMockEnabled,
		"the ad connector is in mock mode: operations are simulated in this worker's memory and reach no domain controller",
		attrs...)
	mock.Observe(func(op ad.MockOperation) {
		logging.Info(logging.ADMockPerformed, "ad mock directory",
			slog.String("operation", op.Op), slog.String("dn", op.DN), slog.String("detail", op.Detail))
	})
}
