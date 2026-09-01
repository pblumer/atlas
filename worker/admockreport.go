package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/pblumer/atlas/connector/ad"
	"github.com/pblumer/atlas/connector/nettimeout"
	"github.com/pblumer/atlas/logging"
)

// Reporting a mock forest to the Atlas that shows it
// (ADR-draft-ad-mock-directory-in-the-console).
//
// A mock directory lives in this worker's memory, which is the right place for it and
// the wrong place to *look* at it. The operator trying a joiner out is in the Console,
// and everything they could see there said something else: the seed card holds the
// entries a forest starts from and is never written back, and the log holds one line
// per operation, which says what was asked for and not what is there now.
//
// So the worker sends. The direction is the preview outbox's, and for the same reason
// (ADR-0150/0168): a worker may sit in a network its server cannot dial into, so a
// report the worker posts is the only channel that works for every deployment. It is
// the same API, with this worker's own token, and it carries nothing a job did not
// already put in the mock.
//
// **A report is an observation, never part of the work.** The operation it describes
// has already happened and the job it rode in on has already succeeded, so a report
// that cannot be delivered is logged and dropped. Failing the job instead would mean a
// directory nobody can see is a directory nobody can write to, which is a far worse
// trade than a stale view.

// ADMockViewURLEnv is the Atlas endpoint this worker posts its mock forest to. A
// supervised worker is handed it at spawn; an external one is given it by hand, or
// not at all — in which case it keeps its forest to itself, as before. Exported
// because the engine renders the same name, and a test there asserts the two agree.
const ADMockViewURLEnv = "ATLAS_AD_MOCK_VIEW_URL"

// WorkerIDEnv is this worker's own id, the one the Workers view shows. `atlas worker`
// takes it from --id and puts it here: a report has to say whose directory it is, and
// the connectors a worker builds are built from the environment alone.
const WorkerIDEnv = "ATLAS_WORKER_ID"

// maxReportedEntries bounds one report. A mock forest is unbounded — a bulk import
// against it is an ordinary thing to try — and a view of it is not: past this the
// report says it was truncated and how many entries the forest actually holds, which
// is the honest answer to "show me the directory" for a directory too big to show.
const maxReportedEntries = 2000

// adMockReporter posts this worker's mock directory to an Atlas.
//
// sent is the directory version the last delivered report carried, so a worker leasing
// jobs that change nothing — a search, a replayed create the mock refuses — does not
// post the same forest over and over. It is guarded by mu, which is also held across
// the post: reports are serialized so a slower one cannot overwrite a newer forest
// with an older picture of it.
type adMockReporter struct {
	url    string
	token  string
	worker string
	dir    *ad.MockDirectory
	client *http.Client

	mu   sync.Mutex
	sent uint64
	ever bool
}

// newADMockReporter builds the reporter for a mock directory, or nil when this worker
// was given no address to report to. nil is a working configuration, not a failure:
// the mock serves its jobs exactly as it did before there was a view.
func newADMockReporter(env func(string) string, dir *ad.MockDirectory) *adMockReporter {
	url := strings.TrimSpace(env(ADMockViewURLEnv))
	if url == "" || dir == nil {
		return nil
	}
	return &adMockReporter{
		url:    url,
		token:  strings.TrimSpace(env("ATLAS_TOKEN")),
		worker: strings.TrimSpace(env(WorkerIDEnv)),
		dir:    dir,
		client: nettimeout.HTTPClient(),
	}
}

// report sends what the directory holds now, unless the last report already said it.
// A nil reporter reports nothing, so the AD handler calls this the same way whether
// this worker has a view to feed or not.
func (r *adMockReporter) report(ctx context.Context) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	version := r.dir.Version()
	if r.ever && version == r.sent {
		return
	}
	snap := r.dir.Snapshot(maxReportedEntries)
	snap.Worker = r.worker
	if err := r.post(ctx, snap); err != nil {
		// Warn and carry on. The operation is done and the job is fine; what is lost
		// is one refresh of a view, and the next operation sends the whole directory
		// again — so a Console that was unreachable for a while catches up by itself
		// rather than staying wrong.
		logging.Warn(logging.ADMockReportFailed,
			"the mock directory could not be reported; the Console's view of it is behind",
			slog.String("url", r.url), slog.String("error", err.Error()))
		return
	}
	r.sent, r.ever = version, true
}

// post delivers one snapshot.
func (r *adMockReporter) post(ctx context.Context, snap ad.MockSnapshot) error {
	body, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("encode the snapshot: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s answered %s", r.url, resp.Status)
	}
	return nil
}
