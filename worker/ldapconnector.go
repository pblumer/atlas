package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pblumer/atlas/connector/envname"
	"github.com/pblumer/atlas/connector/ldap"
)

// ldapSecretFromEnv resolves an LDAP bind password or client certificate reference
// against this worker's own environment, under the same `ATLAS_CONNECTOR_<REF>_TOKEN`
// convention the engine uses (ADR-0041 A2): offloading the kind moves the variable
// from the server's environment to the worker's, and changes nothing about the model
// or the reference it authored.
//
// Like AD's, there is nothing to validate at startup, and that is a property of the
// kind's shape rather than an omission: a reference is authored per *task*, not per
// worker name, so a worker cannot know which references the models it will serve use
// — and an anonymous bind authors none at all. A reference nothing answers to fails
// that job with the variable named, which is what the in-process path does today.
func ldapSecretFromEnv(env func(string) string) ldap.SecretResolver {
	return func(ref string) string {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return ""
		}
		return strings.TrimSpace(env(envname.ConnectorToken(ref)))
	}
}

// RunLdapJob performs a resolved LDAP job with the caller's own dialer and secret
// store. It is exported for the same reason RunADJob and the rest are: the
// environment is only the default place a worker's credentials come from, and a
// caller embedding this package can resolve references from a vault of its own and
// get the identical operation.
//
// dialer is a [ldap.Pool] in a worker that leases many jobs — an LDAP bind is
// expensive enough that ADR-0154 pooled it, and a worker that dials per job would
// give that back the moment the work moved out of the engine.
func RunLdapJob(ctx context.Context, j Job, dialer ldap.Dialer, secret ldap.SecretResolver) (map[string]any, error) {
	if j.Connector == nil {
		return nil, fmt.Errorf("ldap: the job carried no resolved connector detail; is this server offloading the ldap kind?")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task ldap.Job
	if err := json.Unmarshal(raw, &task); err != nil {
		return nil, fmt.Errorf("ldap: cannot read the resolved detail: %w", err)
	}
	res, err := ldap.Run(ctx, task, dialer, secret)
	if err != nil {
		return nil, err
	}
	return ldapVariables(res), nil
}

// ldapVariables renders a run's result as the variables the job completes with. It
// goes through [ldap.Result.Variables] rather than building a map from the value, so
// an offloaded search writes what an in-engine one writes — the entries reach a model
// in the same shape, and the four mutating operations complete with nothing rather
// than with an empty object.
func ldapVariables(res ldap.Result) map[string]any {
	vars := res.Variables()
	if len(vars) == 0 {
		return nil
	}
	out := make(map[string]any, len(vars))
	for _, v := range vars {
		out[v.Name] = variableValue(v)
	}
	return out
}
