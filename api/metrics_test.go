package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/pblumer/atlas/checkpoint"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/metrics"
	"github.com/pblumer/atlas/opensearch"
)

// ADR-0142 slice 1: the /metrics endpoint and the durability metrics behind it. Every
// number here is read from durable state when Prometheus scrapes — the applied log
// position, what is in the checkpoint root, what is in the WAL directory, the exporter's
// watermark — so a metric cannot claim more than is on disk (invariant I2 holds by
// construction rather than by discipline), and nothing touches the engine's hot path.

// scrape fetches the exposition and returns it as text.
func scrape(t *testing.T, h *compactionHarness) string {
	t.Helper()
	code, body := h.x.do(http.MethodGet, "/metrics", "")
	if code != http.StatusOK {
		t.Fatalf("GET /metrics status=%d body=%s", code, body)
	}
	return string(body)
}

// sampleValue finds one sample by metric name (no labels) in an exposition.
func sampleValue(t *testing.T, exposition, name string) float64 {
	t.Helper()
	for _, line := range strings.Split(exposition, "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != name {
			continue
		}
		v, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			t.Fatalf("sample %q has an unparseable value %q: %v", name, fields[1], err)
		}
		return v
	}
	t.Fatalf("exposition has no sample named %q:\n%s", name, exposition)
	return 0
}

func hasMetric(exposition, name string) bool {
	for _, line := range strings.Split(exposition, "\n") {
		if strings.HasPrefix(line, "# TYPE "+name+" ") {
			return true
		}
	}
	return false
}

// TestMetricsReportTheDurableCheckpointAndWALState is the core of this slice: after a
// checkpoint pass, the exposition describes exactly what is on disk.
func TestMetricsReportTheDurableCheckpointAndWALState(t *testing.T) {
	dir := t.TempDir()
	h := newCompactionHarness(t, dir)
	h.deploy()
	h.create(2)

	before := scrape(t, h)
	if got := sampleValue(t, before, "atlas_checkpoints"); got != 0 {
		t.Errorf("checkpoints = %v before any pass, want 0", got)
	}
	if got := sampleValue(t, before, "atlas_wal_segments"); got == 0 {
		t.Error("wal segments = 0 on a server that has written some")
	}
	applied := sampleValue(t, before, "atlas_applied_log_position")
	if applied == 0 {
		t.Error("applied log position = 0 after durable work")
	}

	h.pass()

	after := scrape(t, h)
	if got := sampleValue(t, after, "atlas_checkpoints"); got != 1 {
		t.Errorf("checkpoints = %v after one pass, want 1", got)
	}
	pos := sampleValue(t, after, "atlas_checkpoint_position")
	if pos != applied {
		t.Errorf("checkpoint position = %v, want the applied position %v", pos, applied)
	}
	if got := sampleValue(t, after, "atlas_wal_bytes"); got == 0 {
		t.Error("wal bytes = 0 on a server that has written segments")
	}
	// The pass succeeded, so the failure gauge stays down and the removal count is real.
	if got := sampleValue(t, after, "atlas_checkpoint_last_pass_failed"); got != 0 {
		t.Errorf("last pass failed = %v after a successful pass, want 0", got)
	}
	if got := sampleValue(t, after, "atlas_checkpoint_last_pass_segments_removed"); got != 0 {
		t.Errorf("segments removed = %v with compaction off, want 0", got)
	}
}

// TestMetricsReportAFailedPass: the gauge an operator alerts on has to move when a pass
// fails, or the alert never fires. A file where the checkpoint root belongs fails it.
func TestMetricsReportAFailedPass(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(checkpoint.Dir(dir), []byte("in the way"), 0o644); err != nil {
		t.Fatalf("seed blocking file: %v", err)
	}
	h := newCompactionHarness(t, dir)
	h.deploy()
	h.create(1)
	h.pass()

	exposition := scrape(t, h)
	if got := sampleValue(t, exposition, "atlas_checkpoint_last_pass_failed"); got != 1 {
		t.Fatalf("last pass failed = %v after a failing pass, want 1", got)
	}
	if got := sampleValue(t, exposition, "atlas_checkpoints"); got != 0 {
		t.Errorf("checkpoints = %v when none could be published, want 0", got)
	}
}

// TestMetricsReportExporterLagOnlyWhenExporting: lag is meaningless without an exporter,
// and a gauge that reads zero when nothing is configured is worse than an absent one —
// it looks like a healthy exporter.
func TestMetricsReportExporterLagOnlyWhenExporting(t *testing.T) {
	dir := t.TempDir()
	h := newCompactionHarness(t, dir)
	h.deploy()
	h.create(1)
	if exposition := scrape(t, h); hasMetric(exposition, "atlas_exporter_lag_positions") {
		t.Error("exporter lag is exposed on a server with no exporter")
	}

	withExp := t.TempDir()
	e := newCompactionHarness(t, withExp,
		WithOpenSearchExporter(opensearch.Config{URL: "http://127.0.0.1:1", Index: "atlas-test"}))
	e.deploy()
	e.create(1)

	exposition := scrape(t, e)
	if !hasMetric(exposition, "atlas_exporter_lag_positions") {
		t.Fatalf("exporter lag missing on a server with an exporter:\n%s", exposition)
	}
	applied := sampleValue(t, exposition, "atlas_applied_log_position")
	if got := sampleValue(t, exposition, "atlas_exporter_position"); got != 0 {
		t.Errorf("exporter position = %v before anything was exported, want 0", got)
	}
	if got := sampleValue(t, exposition, "atlas_exporter_lag_positions"); got != applied {
		t.Errorf("exporter lag = %v with nothing exported, want the whole applied log %v", got, applied)
	}
}

// TestMetricsCarryOnlyAllowlistedLabels turns ADR-0142's cardinality rule into a test.
// A label whose values come from the data — an instance key, a process id, a URL —
// makes one metric into unboundedly many series and takes the scrape target down. The
// allowlist is what future slices have to argue against rather than quietly widen.
func TestMetricsCarryOnlyAllowlistedLabels(t *testing.T) {
	allowed := map[string]bool{
		"version":   true, // build info, one series
		"revision":  true,
		"partition": true, // fixed by the deployment, not by the data
		"outcome":   true, // a closed enum
		// A histogram's bucket boundaries and a summary's quantiles are chosen in the
		// code, so their series count is fixed at compile time — the rule is that a
		// label's values must not come from the data, not that no label may exist.
		"le":       true,
		"quantile": true,
	}
	dir := t.TempDir()
	h := newCompactionHarness(t, dir)
	h.deploy()
	h.create(2)
	h.pass()

	labelPattern := regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)=`)
	for _, line := range strings.Split(scrape(t, h), "\n") {
		open := strings.Index(line, "{")
		if strings.HasPrefix(line, "#") || open < 0 {
			continue
		}
		close := strings.LastIndex(line, "}")
		if close < open {
			continue
		}
		for _, m := range labelPattern.FindAllStringSubmatch(line[open:close], -1) {
			if !allowed[m[1]] {
				t.Errorf("metric line %q uses label %q, which is not in the bounded-cardinality allowlist (ADR-0142)", line, m[1])
			}
		}
	}
}

// TestMetricsCanBeTurnedOff: /metrics is an operational surface, so an operator who does
// not want it exposed can say so, exactly like the API docs (ADR-0043).
func TestMetricsCanBeTurnedOff(t *testing.T) {
	dir := t.TempDir()
	h := newCompactionHarness(t, dir, WithoutMetrics())
	if code, _ := h.x.do(http.MethodGet, "/metrics", ""); code != http.StatusNotFound {
		t.Fatalf("GET /metrics with metrics disabled: status=%d, want 404", code)
	}
}

// TestMetricsExposeBuildInfo lets a dashboard correlate a graph with the running build,
// the one place a label carries a value — bounded to a single series.
func TestMetricsExposeBuildInfo(t *testing.T) {
	dir := t.TempDir()
	h := newCompactionHarness(t, dir)
	exposition := scrape(t, h)
	if !hasMetric(exposition, "atlas_build_info") {
		t.Fatalf("exposition carries no build info:\n%s", exposition)
	}
	if !strings.Contains(exposition, `version="`+Version+`"`) {
		t.Errorf("build info does not carry the running version %q", Version)
	}
}

// TestMetricsCountACorruptCheckpointButDoNotCreditIt: a checkpoint whose state files no
// longer match its manifest licenses no compaction and would not carry a restore, so
// crediting its position would tell an operator a restart is shorter than it is. It is
// still counted — it is on disk, and it is occupying it — but the position and age
// gauges report only what still verifies.
func TestMetricsCountACorruptCheckpointButDoNotCreditIt(t *testing.T) {
	dir := t.TempDir()
	h := newCompactionHarness(t, dir)
	h.deploy()
	h.create(2)
	h.pass()

	healthy := scrape(t, h)
	if got := sampleValue(t, healthy, "atlas_checkpoint_position"); got == 0 {
		t.Fatal("no checkpoint position before corruption; the fixture proves nothing")
	}

	// Unlink before writing: a Pebble checkpoint hard-links its SSTables, so writing
	// through the path would corrupt the live store at the same inode.
	positions, err := checkpoint.List(checkpoint.Dir(dir))
	if err != nil || len(positions) != 1 {
		t.Fatalf("List = %v, %v; want exactly one checkpoint", positions, err)
	}
	cpDir := filepath.Join(checkpoint.Dir(dir), checkpoint.DirName(positions[0]))
	entries, err := os.ReadDir(cpDir)
	if err != nil {
		t.Fatalf("ReadDir checkpoint: %v", err)
	}
	corrupted := false
	for _, e := range entries {
		if e.IsDir() || e.Name() == checkpoint.ManifestName {
			continue
		}
		p := filepath.Join(cpDir, e.Name())
		if err := os.Remove(p); err != nil {
			t.Fatalf("unlink %s: %v", e.Name(), err)
		}
		if err := os.WriteFile(p, []byte("corrupted"), 0o644); err != nil {
			t.Fatalf("corrupt %s: %v", e.Name(), err)
		}
		corrupted = true
		break
	}
	if !corrupted {
		t.Fatal("checkpoint held no state file to corrupt")
	}

	after := scrape(t, h)
	if got := sampleValue(t, after, "atlas_checkpoints"); got != 1 {
		t.Errorf("checkpoints = %v, want the corrupt one still counted as occupying disk", got)
	}
	if got := sampleValue(t, after, "atlas_checkpoint_position"); got != 0 {
		t.Errorf("checkpoint position = %v, want 0 — no checkpoint verifies, so none shortens a restart", got)
	}
	if hasMetric(after, "atlas_checkpoint_age_seconds") {
		t.Error("an age is reported for a checkpoint that does not verify")
	}
}

// TestEngineMetricsReportTheBatchLoop: the counters the engine pushes reach the
// exposition, and they describe work that actually happened.
func TestEngineMetricsReportTheBatchLoop(t *testing.T) {
	dir := t.TempDir()
	h := newCompactionHarness(t, dir)
	h.deploy()
	h.create(3)

	exposition := scrape(t, h)
	batches := sampleValue(t, exposition, "atlas_batches_total")
	commands := sampleValue(t, exposition, "atlas_commands_processed_total")
	events := sampleValue(t, exposition, "atlas_events_written_total")
	if batches == 0 || commands == 0 || events == 0 {
		t.Fatalf("batches=%v commands=%v events=%v after real work, want all non-zero", batches, commands, events)
	}
	// Every event went through a batch, and every batch consumed at least one command.
	if batches > commands {
		t.Errorf("more batches (%v) than commands (%v)", batches, commands)
	}
	// The counter cannot exceed what the durable applied position says was written.
	if applied := sampleValue(t, exposition, "atlas_applied_log_position"); events > applied {
		t.Errorf("counted %v events but the store applied only through %v", events, applied)
	}
	// The fsync histogram observed every batch that wrote something.
	if got := sampleValue(t, exposition, "atlas_wal_sync_seconds_count"); got == 0 {
		t.Error("no fsync durations were observed")
	}
	if !hasMetric(exposition, "atlas_batch_events") {
		t.Error("batch size is not exposed")
	}
	// Nothing failed, so the failure counters exist at zero — present, so an alert rule
	// can reference them, rather than absent until the first failure.
	for _, name := range []string{"atlas_wal_sync_failures_total", "atlas_state_commit_failures_total"} {
		if got := sampleValue(t, exposition, name); got != 0 {
			t.Errorf("%s = %v on a healthy server, want 0", name, got)
		}
	}
	if !hasMetric(exposition, "atlas_command_queue_depth") {
		t.Error("command queue depth is not exposed")
	}
}

// TestEngineMetricsReportNoAlloc is the ADR-0142 rule-1 guard against the *real*
// implementation. engine/alloc_test.go proves the call shape is free; this proves the
// Prometheus handles behind it are too — which is what a future metric added with a
// per-batch WithLabelValues lookup would break.
func TestEngineMetricsReportNoAlloc(t *testing.T) {
	m := newEngineMetrics()
	stats := engine.BatchStats{Commands: 12, Events: 30, QueueDepth: 4, SyncSeconds: 0.001, CommitSeconds: 0.0002}

	allocs := testing.AllocsPerRun(1000, func() {
		m.BatchCommitted(stats)
		m.SyncFailed()
		m.CommitFailed()
	})
	if allocs != 0 {
		t.Errorf("recording a batch into Prometheus allocated %v times per run, want 0 "+
			"(every handle must be resolved at construction, never looked up per batch)", allocs)
	}
}

// TestEngineMetricsSkipDurationsForAnEmptyBatch: a batch that wrote nothing had no fsync
// and no commit, so observing its zero durations would report a p99 no real write ever
// achieved. Its commands still count — they were consumed.
func TestEngineMetricsSkipDurationsForAnEmptyBatch(t *testing.T) {
	m := newEngineMetrics()
	m.BatchCommitted(engine.BatchStats{Commands: 4, QueueDepth: 1})

	reg := metrics.NewRegistry()
	if err := reg.Register(m.collectors()...); err != nil {
		t.Fatalf("Register: %v", err)
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, f := range families {
		switch f.GetName() {
		case "atlas_wal_sync_seconds", "atlas_state_commit_seconds", "atlas_batch_events":
			if n := f.GetMetric()[0].GetHistogram().GetSampleCount(); n != 0 {
				t.Errorf("%s observed %d samples for an empty batch, want none", f.GetName(), n)
			}
		case "atlas_commands_processed_total":
			if v := f.GetMetric()[0].GetCounter().GetValue(); v != 4 {
				t.Errorf("commands = %v for an empty batch, want the 4 it consumed", v)
			}
		}
	}
}

// TestMetricsReportRuntimeGauges: the two questions an operator asks first — how much is
// running, and is it moving — answered from the maintained per-definition counters
// (ADR-0080) rather than by walking the runtime set on every scrape.
func TestMetricsReportRuntimeGauges(t *testing.T) {
	dir := t.TempDir()
	h := newCompactionHarness(t, dir)
	h.deploy()

	if got := sampleValue(t, scrape(t, h), "atlas_active_process_instances"); got != 0 {
		t.Fatalf("active instances = %v before anything started, want 0", got)
	}

	h.create(3)
	exposition := scrape(t, h)
	if got := sampleValue(t, exposition, "atlas_active_process_instances"); got != 3 {
		t.Errorf("active process instances = %v after starting 3, want 3", got)
	}
	// The three parked instances each hold a token at the service task.
	if got := sampleValue(t, exposition, "atlas_live_element_tokens"); got == 0 {
		t.Error("live element tokens = 0 while three instances are parked on a task")
	}

	// The gauge must agree with the scan that is ground truth, or the cheap read is
	// telling the operator a different story than the API does.
	scanned, err := h.store.ActiveProcessInstanceCount()
	if err != nil {
		t.Fatalf("ActiveProcessInstanceCount: %v", err)
	}
	if got := sampleValue(t, exposition, "atlas_active_process_instances"); int(got) != scanned {
		t.Errorf("gauge says %v active instances, the authoritative scan says %d", got, scanned)
	}
}

// TestMetricsRuntimeGaugesFallWhenInstancesFinish: a gauge that only climbs would show a
// healthy engine as an ever-growing backlog, which is worse than not reporting at all.
func TestMetricsRuntimeGaugesFallWhenInstancesFinish(t *testing.T) {
	dir := t.TempDir()
	h := newCompactionHarness(t, dir)
	h.deploy()
	h.create(2)
	if got := sampleValue(t, scrape(t, h), "atlas_active_process_instances"); got != 2 {
		t.Fatalf("active instances = %v after starting 2, want 2", got)
	}

	// Terminate one; the other stays parked.
	code, body := h.x.do(http.MethodGet, "/api/v1/instances", "")
	if code != http.StatusOK {
		t.Fatalf("list instances status=%d body=%s", code, body)
	}
	var rows []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, body)
	}
	if len(rows) != 2 {
		t.Fatalf("listed %d instances, want 2", len(rows))
	}
	if code, body := h.x.do(http.MethodDelete, "/api/v1/instances/"+strconv.FormatUint(rows[0].Key, 10), ""); code != http.StatusOK {
		t.Fatalf("terminate status=%d body=%s", code, body)
	}

	if got := sampleValue(t, scrape(t, h), "atlas_active_process_instances"); got != 1 {
		t.Fatalf("active instances = %v after terminating one of two, want 1", got)
	}
}

// TestMetricsReportOpenWork: the backlog gauges — what is waiting for a worker, a
// clock, or a message. Each is a durable counter maintained by applyToState (ADR-0142
// slice 4), so unlike a scan it costs the same whatever the backlog is.
func TestMetricsReportOpenWork(t *testing.T) {
	dir := t.TempDir()
	h := newCompactionHarness(t, dir)
	h.deploy()

	exposition := scrape(t, h)
	for _, name := range []string{"atlas_open_jobs", "atlas_pending_timers", "atlas_message_subscriptions"} {
		if got := sampleValue(t, exposition, name); got != 0 {
			t.Errorf("%s = %v before anything started, want 0", name, got)
		}
	}

	// Three instances park on the service task: three jobs waiting for a worker.
	h.create(3)
	if got := sampleValue(t, scrape(t, h), "atlas_open_jobs"); got != 3 {
		t.Fatalf("open jobs = %v with three parked instances, want 3", got)
	}

	// Completing one closes exactly one job — the gauge falls rather than only climbing.
	// The job type is interned by the compiler, so the test asks the store for whatever
	// is activatable rather than assuming an id.
	var jobKey uint64
	if err := h.store.AllActivatableJobs(func(k uint64) error {
		if jobKey == 0 {
			jobKey = k
		}
		return nil
	}); err != nil {
		t.Fatalf("AllActivatableJobs: %v", err)
	}
	if jobKey == 0 {
		t.Fatal("no activatable job to complete")
	}
	if code, body := h.x.do(http.MethodPost,
		"/api/v1/jobs/"+strconv.FormatUint(jobKey, 10)+"/complete", "{}"); code != http.StatusOK {
		t.Fatalf("complete job status=%d body=%s", code, body)
	}
	if got := sampleValue(t, scrape(t, h), "atlas_open_jobs"); got != 2 {
		t.Fatalf("open jobs = %v after completing one of three, want 2", got)
	}
}

// TestOpenJobsGaugeIgnoresReputs is the subtle half of the open-job counter. Failing a
// job re-puts it with a decremented retry count, and assigning re-puts it with an
// assignee — the job was already open and still is, so neither may move the count.
// Counting every put instead of only creation would inflate the gauge on exactly the
// processes an operator is most likely to be watching: the ones that are retrying.
func TestOpenJobsGaugeIgnoresReputs(t *testing.T) {
	dir := t.TempDir()
	h := newCompactionHarness(t, dir)
	h.deploy()
	h.create(1)

	if got := sampleValue(t, scrape(t, h), "atlas_open_jobs"); got != 1 {
		t.Fatalf("open jobs = %v after one parked instance, want 1", got)
	}

	var jobKey uint64
	if err := h.store.AllActivatableJobs(func(k uint64) error {
		if jobKey == 0 {
			jobKey = k
		}
		return nil
	}); err != nil {
		t.Fatalf("AllActivatableJobs: %v", err)
	}
	if jobKey == 0 {
		t.Fatal("no activatable job in the fixture")
	}

	// Fail it with retries left: the job is re-put, still open, still one job.
	if code, body := h.x.do(http.MethodPost, "/api/v1/jobs/"+strconv.FormatUint(jobKey, 10)+"/fail",
		`{"retries":2,"errorMessage":"transient"}`); code != http.StatusOK {
		t.Fatalf("fail job status=%d body=%s", code, body)
	}
	if got := sampleValue(t, scrape(t, h), "atlas_open_jobs"); got != 1 {
		t.Fatalf("open jobs = %v after failing (re-putting) the one job, want 1", got)
	}
}
