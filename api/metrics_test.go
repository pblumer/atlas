package api

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/pblumer/atlas/checkpoint"
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
