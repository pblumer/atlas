package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pblumer/atlas/connector/jira"
)

// jiraEnvPrefix is where a Jira worker's site URLs and credentials live.
const jiraEnvPrefix = "ATLAS_JIRA_"

// jiraRegistryFromEnv builds the Jira instances this worker holds.
// ATLAS_JIRA_CONNECTORS lists the names; each name contributes
// ATLAS_JIRA_<NAME>_URL (the site base URL, e.g. https://acme.atlassian.net) and
// exactly one credential shape — _EMAIL together with _API_TOKEN for Jira Cloud, or
// _TOKEN alone for a Data Center personal access token. Those are the values
// [jira.Connector] is built from, so a worker builds the identical client the engine
// would have.
//
// Requiring exactly one shape rather than preferring one is deliberate: the shape also
// decides how an assignee is addressed (an accountId on Cloud, a username on Data
// Center) and, since Atlassian's search deprecation, which search endpoint is used. An
// operator who set both would be guessing at which product this worker thinks it is
// talking to, and that is a question to answer at startup rather than in a failed job.
//
// The credential comes from the environment and not from a flag because argv is
// readable by anyone who can list processes.
func jiraRegistryFromEnv(env func(string) string) (*jira.Registry, []string, error) {
	names := splitAndTrim(env(jiraEnvPrefix + "CONNECTORS"))
	if len(names) == 0 {
		// Unconfigured, not misconfigured — a nil registry and no error, which the
		// caller reports as a kind this worker does not serve. A *named* instance
		// missing a field, below, is still an error: the operator named it, so the
		// omission is a mistake to report at startup rather than a queue to lease work
		// from and then fail.
		return nil, nil, nil
	}
	reg := jira.NewRegistry()
	for _, name := range names {
		key := jiraEnvPrefix + envFold(name) + "_"
		url, email, apiToken, token := env(key+"URL"), env(key+"EMAIL"), env(key+"API_TOKEN"), env(key+"TOKEN")
		if url == "" {
			return nil, nil, fmt.Errorf("worker: jira worker %q is missing its URL: set %sURL", name, key)
		}
		cloud := email != "" || apiToken != ""
		switch {
		case cloud && token != "":
			return nil, nil, fmt.Errorf("worker: jira worker %q sets both a Cloud credential and a Data Center token: set %sEMAIL with %sAPI_TOKEN, or %sTOKEN alone", name, key, key, key)
		case cloud && (email == "" || apiToken == ""):
			return nil, nil, fmt.Errorf("worker: jira worker %q has half a Cloud credential: set both %sEMAIL and %sAPI_TOKEN", name, key, key)
		case !cloud && token == "":
			return nil, nil, fmt.Errorf("worker: jira worker %q is missing its credential: set %sEMAIL with %sAPI_TOKEN for Jira Cloud, or %sTOKEN for a Data Center personal access token", name, key, key, key)
		}
		reg.Register(name, jira.NewHTTPClient(jira.Connector{
			BaseURL:  url,
			Email:    email,
			APIToken: apiToken,
			Token:    token,
		}))
	}
	return reg, names, nil
}

// RunJiraJob performs a resolved Jira job through a registry the caller owns. It is
// exported for the same reason RunRemedyJob and RunMailJob are: the environment is only
// the default place a worker's credentials come from, and a caller embedding this
// package can build a registry from a vault or an instance profile and get the
// identical call.
//
// It shares [jira.Run] with the in-process path, so no two of those can disagree about
// what a resolved Jira task means — only about which credentials are in reach.
func RunJiraJob(ctx context.Context, j Job, reg *jira.Registry) (map[string]any, error) {
	if j.Connector == nil {
		return nil, fmt.Errorf("jira: the job carried no resolved worker detail; is this server offloading the jira kind?")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task jira.Job
	if err := json.Unmarshal(raw, &task); err != nil {
		return nil, fmt.Errorf("jira: cannot read the resolved detail: %w", err)
	}
	res, err := jira.Run(ctx, task, reg)
	if err != nil {
		return nil, err
	}
	if task.ResultVariable == "" || res == nil {
		// Either the model discards the answer, or the operation is one Jira answers
		// with no content — the same distinction the in-process handler makes, so an
		// offloaded assign does not write a null where a read would write a value.
		return nil, nil
	}
	return map[string]any{task.ResultVariable: res}, nil
}
