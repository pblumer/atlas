package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/pblumer/atlas/connector/calendar"
)

// calendarEnvPrefix is where a calendar worker's accounts live.
const calendarEnvPrefix = "ATLAS_CALENDAR_"

// calendarRegistryFromEnv builds the calendar accounts this worker holds.
// ATLAS_CALENDAR_CONNECTORS lists the names; each name contributes
// ATLAS_CALENDAR_<NAME>_URL — the provider's API root, empty for its public one — and
// ATLAS_CALENDAR_<NAME>_CREDENTIALS, the bundle [calendar.Bundle] describes, which also
// says which provider answers.
//
// The credential arrives as the whole bundle, one opaque JSON value, for CardDAV's
// reason: [calendar.NewBackend] is the one place that reads it, here as in the server's
// supervision check. It comes from the environment and not from a flag because argv is
// readable by anyone who can list processes.
func calendarRegistryFromEnv(env func(string) string) (*calendar.Registry, []string, error) {
	names := splitAndTrim(env(calendarEnvPrefix + "CONNECTORS"))
	if len(names) == 0 {
		// Unconfigured, not misconfigured: a nil registry and no error. A *named*
		// account that does not build, below, is still an error.
		return nil, nil, nil
	}
	reg := calendar.NewRegistry()
	for _, name := range names {
		key := calendarEnvPrefix + envFold(name) + "_"
		b, err := calendar.NewBackend(env(key+"URL"), env(key+"CREDENTIALS"), calendar.Options{})
		if err != nil {
			return nil, nil, fmt.Errorf("worker: calendar worker %q is not usable: %w (set %sCREDENTIALS to the credential bundle, and %sURL only to replace the provider's API root)", name, err, key, key)
		}
		reg.Register(name, b)
	}
	return reg, names, nil
}

// RunCalendarJob performs a resolved calendar task through a registry the caller owns.
// It is exported for the reason RunCardDAVJob is: a caller embedding this package can
// build a registry from its own secret store and get the identical call.
func RunCalendarJob(ctx context.Context, j Job, reg *calendar.Registry) (map[string]any, error) {
	if j.Connector == nil {
		// The server resolves a task's input when it hands the job out and sends nothing
		// when it cannot — an input that is not an object, a key the operation does not
		// take — as well as when it predates this Worker Type.
		return nil, fmt.Errorf("calendar: the job carried no resolved task; the server could not evaluate its input (is every variable an =expression reads set?) or does not resolve calendar tasks")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task calendar.Job
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&task); err != nil {
		return nil, fmt.Errorf("calendar: cannot read the resolved detail: %w", err)
	}
	return calendar.Run(ctx, task, reg)
}
