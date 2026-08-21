package api

import (
	"log/slog"
	"sort"
	"strings"

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
	return map[string]func() []string{connectorKindMail: s.mailWorkerEnv}
}

// superviseEnv is the environment a supervised worker is spawned with, for the kinds
// it serves. It returns nil for a worker that serves no provisioned kind — every
// worker an operator configured with --supervise, which inherits this process's
// environment unchanged, exactly as before.
func (s *Server) superviseEnv(spec SuperviseSpec) func() []string {
	provisioned := s.provisionedConnectorKinds()
	var render []func() []string
	for _, k := range spec.Connectors {
		if fn, ok := provisioned[strings.TrimSpace(k)]; ok {
			render = append(render, fn)
		}
	}
	if len(render) == 0 {
		return nil
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
