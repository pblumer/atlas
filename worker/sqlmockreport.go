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
	"time"

	"github.com/pblumer/atlas/connector/nettimeout"
	"github.com/pblumer/atlas/connector/sqldb"
	"github.com/pblumer/atlas/logging"
)

// Reporting a mockup run to the Atlas that shows it (ADR-draft-sql-mock-journal).
//
// A mock database lives in this worker's memory, which is the right place for it and
// the wrong place to *look* at it. The operator trying a process out is in the Console,
// and everything they could see there said something else: the prepared answers are an
// input the worker reads once, and the log holds one line per failure among everything
// else this worker did.
//
// So the worker sends, exactly as the AD mock reports its forest (ADR-0213) and for the
// same reason (ADR-0150/0168): a worker may sit in a network its server cannot dial
// into, so a report the worker posts is the only channel that works for every
// deployment. It is the same API, with this worker's own token.
//
// **A report is an observation, never part of the work.** The statement it describes has
// already been answered and the job has already settled, so a report that cannot be
// delivered is logged and dropped. Failing the job instead would mean a run nobody can
// see is a run nobody can make.

// SQLMockViewURLEnv is the Atlas endpoint this worker posts its mock journal to. A
// supervised worker is handed it at spawn and only while the mockup is on; an external
// one is given it by hand, or not at all — in which case it keeps its journal to
// itself. Exported because the engine renders the same name, and a test there asserts
// the two agree.
const SQLMockViewURLEnv = "ATLAS_SQL_MOCK_VIEW_URL"

// maxReportedStatements bounds one report. The journal is already capped in the mock,
// but the bound is restated here because this is where the data leaves the process:
// what crosses a network is bounded at the crossing, not by a promise made elsewhere.
const maxReportedStatements = 200

// sqlMockReporter posts this worker's mock journal to an Atlas.
//
// sent is the journal version the last delivered report carried, so a worker leasing
// jobs of other kinds does not post the same journal over and over. It is guarded by
// mu, which is also held across the post: reports are serialized so a slower one cannot
// overwrite a newer journal with an older picture of it.
type sqlMockReporter struct {
	url    string
	token  string
	worker string
	db     *sqldb.MockDatabase
	client *http.Client

	// backoff is this reporter's copy of sqlMockStartupBackoff, taken when it is
	// constructed. The startup report runs on a detached goroutine that outlives
	// whatever created it, so reading the package variable from inside that loop races
	// with anything that replaces it — which a test legitimately does.
	backoff []time.Duration

	mu   sync.Mutex
	sent uint64
	ever bool
}

// newSQLMockReporter builds the reporter for a mock database, or nil when this worker
// was given no address to report to. nil is a working configuration, not a failure: the
// mock answers its statements exactly as it did before there was a view.
func newSQLMockReporter(env func(string) string, db *sqldb.MockDatabase) *sqlMockReporter {
	url := strings.TrimSpace(env(SQLMockViewURLEnv))
	if url == "" || db == nil {
		return nil
	}
	return &sqlMockReporter{
		url:     url,
		token:   strings.TrimSpace(env("ATLAS_TOKEN")),
		worker:  strings.TrimSpace(env(WorkerIDEnv)),
		db:      db,
		client:  nettimeout.HTTPClient(),
		backoff: sqlMockStartupBackoff,
	}
}

// sqlMockStartupBackoff is how long the *startup* report waits between attempts. A
// supervised worker is spawned by the very server it reports to, so it comes up while
// that server is still opening its listener: the first attempt is refused as a matter
// of course. Without the retry a redeployed instance would leave the view empty until
// the first SQL job — missing precisely the state the startup report exists to show,
// which is "the mockup is on, 12 answers are seeded, nothing has been asked yet".
//
// A var, not a const, so a test can shrink it.
var sqlMockStartupBackoff = []time.Duration{
	250 * time.Millisecond, 500 * time.Millisecond, time.Second,
	2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second,
}

// report sends what the journal holds now, unless the last report already said it. A
// nil reporter reports nothing, so the SQL handler calls this the same way whether this
// worker has a view to feed or not.
func (r *sqlMockReporter) report(ctx context.Context) {
	if r == nil {
		return
	}
	if err := r.send(ctx); err != nil {
		// Warn and carry on. The statement is answered and the job is settled; what is
		// lost is one refresh of a view, and the next statement sends the whole journal
		// again — so a Console that was unreachable for a while catches up by itself
		// rather than staying wrong.
		r.warn(err)
	}
}

// reportAtStartup announces this worker before it has answered anything, retrying while
// the server it reports to is still coming up.
//
// The retries are quiet: one refusal during a cold start is the expected case, not news.
// Only giving up is logged, once.
func (r *sqlMockReporter) reportAtStartup(ctx context.Context) {
	if r == nil {
		return
	}
	var err error
	for attempt := 0; ; attempt++ {
		if err = r.send(ctx); err == nil {
			return
		}
		if attempt >= len(r.backoff) {
			r.warn(err)
			return
		}
		timer := time.NewTimer(r.backoff[attempt])
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			r.warn(err)
			return
		}
	}
}

// send posts what the journal holds now, or nothing at all when the last delivered
// report already said it. A skipped send is a success: there is nothing to say.
//
// The startup report is the exception that `ever` exists for: an untouched journal is
// version 0, and "nothing has been asked yet" is exactly what the view needs told.
func (r *sqlMockReporter) send(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	version := r.db.Version()
	if r.ever && version == r.sent {
		return nil
	}
	snap := r.db.Snapshot(maxReportedStatements)
	snap.Worker = r.worker
	if err := r.post(ctx, snap); err != nil {
		return err
	}
	r.sent, r.ever = version, true
	return nil
}

// warn says the view is behind. It is a warning and never a job failure: the statement
// it describes has already been answered.
func (r *sqlMockReporter) warn(err error) {
	logging.Warn(logging.SQLMockReportFailed,
		"the mock database's journal could not be reported; the Console's view of it is behind",
		slog.String("url", r.url), slog.String("error", err.Error()))
}

// post delivers one snapshot.
func (r *sqlMockReporter) post(ctx context.Context, snap sqldb.MockJournalSnapshot) error {
	body, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("encode the snapshot: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build the request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("the server answered %s", resp.Status)
	}
	return nil
}
