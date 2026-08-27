package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/connector/ad"
	"github.com/pblumer/atlas/connector/ldif"
	"github.com/pblumer/atlas/logging"
)

// adSecretPrefix is where an AD worker's bind passwords live. It is deliberately the
// *same* convention the engine uses (ADR-0041 A2): offloading the kind moves the
// variable from the server's environment to the worker's, and changes nothing about
// the model or the reference it authored.
const adSecretPrefix = "ATLAS_CONNECTOR_"

// adSecretFromEnv resolves an AD bind-password reference against this worker's own
// environment.
//
// Unlike mail, SQL and Entra there is nothing to validate at startup, and that is a
// property of AD's shape rather than an omission: a reference is authored per *task*,
// not per connector name, so the worker cannot know which references the models it
// will serve use — and an anonymous bind authors no reference at all. A reference
// nothing answers to fails that job with the variable named, which is the same
// failure the in-process path gives today.
func adSecretFromEnv(env func(string) string) ad.SecretResolver {
	return func(ref string) string {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return ""
		}
		return strings.TrimSpace(env(adSecretPrefix + envFold(ref) + "_TOKEN"))
	}
}

// RunADJob performs a resolved AD job with the caller's own dialer and secret store.
// It is exported for the same reason RunMailJob and the rest are: the environment is
// only the default place a worker's credentials come from, and a caller embedding
// this package can resolve references from a vault of its own and get the identical
// operation.
func RunADJob(ctx context.Context, j Job, dialer ad.Dialer, secret ad.SecretResolver) (map[string]any, error) {
	if j.Connector == nil {
		return nil, fmt.Errorf("ad: the job carried no resolved connector detail; is this server offloading the ad kind?")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task ad.Job
	if err := json.Unmarshal(raw, &task); err != nil {
		return nil, fmt.Errorf("ad: cannot read the resolved detail: %w", err)
	}
	// Every AD operation but sync writes to the directory rather than back to a
	// variable, and returns nothing; sync returns the changes and the next cookie.
	return ad.Run(ctx, task, dialer, secret)
}

// Mock mode: an AD worker with no domain controller behind it.
//
// An identity process is the one kind nobody can try out where it will run — the
// directory a joiner/mover/leaver touches is production by definition, and a test
// account created in it is a real account. So a worker can be told to serve the AD
// kind against [ad.MockDirectory] instead: the same resolved job, the same
// [ad.Run], entries that live in this process's memory and are gone when it stops
// (ADR-0181).
//
// The switch is the *worker's*, not the model's, and that is the whole point. A
// mockup flag on the task would be a model that behaves differently in test and in
// production, and would eventually be deployed with the flag still set. Here the
// model is identical either way and what differs is which worker leases its jobs —
// so a mockup run proves the process, and moving it to the real directory is a
// worker's environment, not an edit.
const (
	// adMockEnv turns mock mode on for this worker.
	adMockEnv = "ATLAS_AD_MOCK"
	// adMockSeedEnv names an LDIF or DSML file of entries the mock directory starts
	// with — the accounts a process expects to find, since a leaver has nothing to
	// disable in an empty forest.
	adMockSeedEnv = "ATLAS_AD_MOCK_SEED"
)

// adDialerFromEnv decides which directory this worker's AD tasks reach: a real one,
// or a mock in its own memory. It returns the mock as well when there is one, so the
// caller can log what it does; nil means the production dialer.
func adDialerFromEnv(env func(string) string) (ad.Dialer, *ad.MockDirectory, error) {
	on, err := envBool(env, adMockEnv)
	if err != nil {
		return nil, nil, err
	}
	seed := strings.TrimSpace(env(adMockSeedEnv))
	if !on {
		if seed != "" {
			// Read into a directory nothing would ever reach. Almost certainly a
			// half-finished mock setup, and silence would leave the operator
			// believing they had one.
			return nil, nil, fmt.Errorf("worker: %s names %s but %s is not set, so the seed would be read into a directory no job reaches", adMockSeedEnv, seed, adMockEnv)
		}
		return ad.NewDialer(), nil, nil
	}
	// A seed that cannot be read starts an empty directory rather than taking the
	// worker down. The refusal it replaces was a real outage: an *optional* field
	// holding a stale path made every AD task unservable, and because the supervisor
	// restarts a child that exits, the worker sat in a restart loop where the Workers
	// view showed hundreds of starts and no explanation but one log line.
	//
	// Degrading is safe here in a way it is not elsewhere, and only because this is a
	// mock: an empty directory touches nothing real. A joiner creates its account and
	// does not notice; a leaver fails one job with "no such object", which surfaces as
	// an incident against the task that needed the account — pointing at the missing
	// seed instead of hiding it (ADR-draft-atlas-manages-the-ad-mock-seed).
	entries, err := adMockSeed(seed)
	if err != nil {
		logging.Warn(logging.ADMockSeedUnusable,
			"the AD mockup seed could not be read; the mock directory starts empty, so anything a "+
				"process expects to find there will not be found",
			slog.String("seed", seed), slog.String("error", err.Error()))
		entries = nil
	}
	mock := ad.NewMockDirectory(entries...)
	return mock, mock, nil
}

// adMockSeed reads the seed file, if one was named. It is parsed by the directory-file
// connector's own reader (ADR-0171), so LDIF and DSML mean one thing in Atlas rather
// than one thing per package.
func adMockSeed(path string) ([]ad.Entry, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("worker: %s: %w", adMockSeedEnv, err)
	}
	parsed, err := ldif.Parse(adSeedFormat(path), data)
	if err != nil {
		return nil, fmt.Errorf("worker: %s %s: %w", adMockSeedEnv, path, err)
	}
	entries := make([]ad.Entry, 0, len(parsed))
	for _, e := range parsed {
		entries = append(entries, ad.Entry{DN: e.DN, Attributes: e.Attributes})
	}
	return entries, nil
}

// adSeedFormat reads the seed's format off its file name. LDIF is the default because
// it is what a directory exports.
func adSeedFormat(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".dsml", ".xml":
		return ldif.FormatDSML
	default:
		return ldif.FormatLDIF
	}
}

// envBool reads a yes/no environment variable. An unset one is false; a value that is
// neither is an error rather than a false, because "ATLAS_AD_MOCK=maybe" quietly
// dialling the production directory is exactly the outcome the switch exists to
// prevent.
func envBool(env func(string) string, name string) (bool, error) {
	raw := strings.TrimSpace(env(name))
	if raw == "" {
		return false, nil
	}
	switch strings.ToLower(raw) {
	case "yes", "on":
		return true, nil
	case "no", "off":
		return false, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("worker: %s=%q is not a yes/no value", name, raw)
	}
	return v, nil
}
