package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"sort"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/ad"
	"github.com/pblumer/atlas/connector/csvimport"
	"github.com/pblumer/atlas/connector/mail"
	"github.com/pblumer/atlas/connector/sqldb"
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
		case "mail":
			reg, names, err := mailRegistryFromEnv(env)
			if err != nil {
				return Connectors{}, err
			}
			built.Names = append(built.Names, names...)
			built.Handlers[compiler.MailJobType] = ExecFunc(func(ctx context.Context, j Job) (map[string]any, error) {
				return RunMailJob(ctx, j, reg)
			})
		case "ad":
			// AD needs no startup configuration: its server is model-authored and its
			// bind password is a per-task reference, so there is nothing here a
			// misconfiguration could be caught in (see adSecretFromEnv).
			secret := adSecretFromEnv(env)
			built.Handlers[compiler.AdJobType] = ExecFunc(func(ctx context.Context, j Job) (map[string]any, error) {
				return RunADJob(ctx, j, ad.NewDialer(), secret)
			})
		case "entra":
			reg, names, err := entraRegistryFromEnv(env)
			if err != nil {
				return Connectors{}, err
			}
			built.Names = append(built.Names, names...)
			built.Handlers[compiler.EntraJobType] = ExecFunc(func(ctx context.Context, j Job) (map[string]any, error) {
				return RunEntraJob(ctx, j, reg)
			})
		default:
			// The three SQL products (ADR-0170). Unlike every kind above them they
			// have no in-process counterpart to fall back to, so a worker is the only
			// way a SQL task ever runs — which is why a misconfigured one is refused
			// here, at startup, rather than discovered a retry budget later.
			p, ok := sqldb.ProductByName(kind)
			if !ok {
				continue // an unknown kind: the caller compares counts and reports it
			}
			reg, names, err := sqlRegistryFromEnv(env, p)
			if err != nil {
				return Connectors{}, err
			}
			built.Names = append(built.Names, names...)
			built.Handlers[p.JobType] = ExecFunc(func(ctx context.Context, j Job) (map[string]any, error) {
				return RunSQLJob(ctx, j, reg)
			})
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
}

// mailEnvPrefix is where a mail worker's credentials live.
const mailEnvPrefix = "ATLAS_MAIL_"

// mailRegistryFromEnv builds the mail connectors this worker holds. ATLAS_MAIL_CONNECTORS
// lists the names; each name contributes ATLAS_MAIL_<NAME>_ENDPOINT and the optional
// _USERNAME, _PASSWORD and _FROM. The names are the ones models reference, so the
// list is also the answer to "what can this worker actually send through".
func mailRegistryFromEnv(env func(string) string) (*mail.Registry, []string, error) {
	names := splitAndTrim(env(mailEnvPrefix + "CONNECTORS"))
	if len(names) == 0 {
		return nil, nil, fmt.Errorf("worker: --connector mail needs at least one connector: set %sCONNECTORS to the names this worker sends through", mailEnvPrefix)
	}
	reg := mail.NewRegistry()
	for _, name := range names {
		key := mailEnvPrefix + envFold(name) + "_"
		endpoint := env(key + "ENDPOINT")
		if endpoint == "" {
			return nil, nil, fmt.Errorf("worker: mail connector %q has no endpoint: set %sENDPOINT", name, key)
		}
		reg.Register(name, mail.NewSMTPClient(mail.Connector{
			Endpoint: endpoint,
			Username: env(key + "USERNAME"),
			Password: env(key + "PASSWORD"),
			From:     env(key + "FROM"),
		}))
	}
	return reg, names, nil
}

// envFold turns a connector name into the environment-variable form of itself:
// upper case, with anything that cannot appear in a variable name becoming an
// underscore. It is applied the one way, and the error messages quote the result, so
// an operator sets exactly the variable that was looked for.
func envFold(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range strings.ToUpper(name) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
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
