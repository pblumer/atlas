package api_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// newBackupServer is newTestServer that also hands back the on-disk data dir, so
// backup/restore tests can plant files and assert what did (and did not) land on
// disk after a restore.
func newBackupServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	srv, err := api.New(proc, store, dir)
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Close()
		_ = store.Close()
		_ = log.Close()
	})
	return ts, dir
}

// tarGzEntries decompresses a .tar.gz body and returns the set of entry names.
func tarGzEntries(t *testing.T, body []byte) map[string]bool {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	names := map[string]bool{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next: %v", err)
		}
		names[hdr.Name] = true
	}
	return names
}

// makeTarGz builds a .tar.gz from name→content pairs, for restore tests. A name
// ending in "/" is written as a directory entry.
func makeTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if strings.HasSuffix(name, "/") {
			if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
				t.Fatalf("write dir header: %v", err)
			}
			continue
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content))}); err != nil {
			t.Fatalf("write header: %v", err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("write body: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// TestBackupRestoreRoundTrip backs up one server's design-time data and restores
// it into a second, fresh server. The read-through sidecar stores (drafts here)
// must reflect the restored content live, without a restart.
func TestBackupRestoreRoundTrip(t *testing.T) {
	src, _ := newBackupServer(t)

	// Create some design-time content: a deployment and a saved draft.
	if code, body := doReq(t, src, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	if code, body := doReq(t, src, http.MethodPost, "/api/v1/drafts", draftBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("save draft status=%d body=%s", code, body)
	}

	// Download the backup.
	code, archive := doReq(t, src, http.MethodGet, "/api/v1/backup", "", "")
	if code != http.StatusOK {
		t.Fatalf("backup status=%d body=%s", code, archive)
	}
	names := tarGzEntries(t, archive)
	var sawDraft, sawDeployment bool
	for n := range names {
		if strings.HasPrefix(n, "drafts/") {
			sawDraft = true
		}
		if strings.HasPrefix(n, "deployments/") {
			sawDeployment = true
		}
	}
	if !sawDraft || !sawDeployment {
		t.Fatalf("backup missing content: draft=%v deployment=%v (entries: %v)", sawDraft, sawDeployment, names)
	}

	// Restore into a second, empty server.
	dst, _ := newBackupServer(t)
	code, body := doReqBytes(t, dst, http.MethodPost, "/api/v1/restore", archive, "application/gzip")
	if code != http.StatusOK {
		t.Fatalf("restore status=%d body=%s", code, body)
	}

	// The draft is visible immediately (read-through store), no restart needed.
	code, body = doReq(t, dst, http.MethodGet, "/api/v1/drafts", "", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"processId":"wip-order"`) {
		t.Fatalf("restored drafts status=%d body=%s", code, body)
	}
}

// TestBackupExcludesSecrets asserts the archive never carries the vault key,
// user accounts, or the runtime stores — only the design-time allowlist.
func TestBackupExcludesSecrets(t *testing.T) {
	src, dir := newBackupServer(t)

	// Plant sensitive files that MUST NOT be backed up.
	if err := os.WriteFile(filepath.Join(dir, "vault.key"), []byte("super-secret-key"), 0o600); err != nil {
		t.Fatalf("plant vault.key: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "users"), 0o755); err != nil {
		t.Fatalf("mkdir users: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "users", "admin.json"), []byte(`{"hash":"x"}`), 0o600); err != nil {
		t.Fatalf("plant user: %v", err)
	}
	// And some real design-time content that SHOULD be backed up.
	if code, _ := doReq(t, src, http.MethodPost, "/api/v1/drafts", draftBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("save draft: %d", code)
	}

	code, archive := doReq(t, src, http.MethodGet, "/api/v1/backup", "", "")
	if code != http.StatusOK {
		t.Fatalf("backup status=%d", code)
	}
	names := tarGzEntries(t, archive)
	for n := range names {
		top := n
		if i := strings.IndexByte(n, '/'); i >= 0 {
			top = n[:i]
		}
		switch top {
		case "vault.key", "users", "state", "wal":
			t.Fatalf("backup leaked excluded entry %q", n)
		}
	}
	// Sanity: it did include the design-time draft, so the exclusion above is
	// meaningful and not just an empty archive.
	if !hasPrefix(names, "drafts/") {
		t.Fatalf("backup unexpectedly empty of drafts: %v", names)
	}
}

// TestRestoreRejectsPathTraversal ensures a hostile archive cannot write outside
// the data dir.
func TestRestoreRejectsPathTraversal(t *testing.T) {
	dst, dir := newBackupServer(t)
	evil := makeTarGz(t, map[string]string{
		"../escape.txt":  "pwned",
		"drafts/ok.json": `{"processId":"x"}`,
	})
	code, body := doReqBytes(t, dst, http.MethodPost, "/api/v1/restore", evil, "application/gzip")
	if code != http.StatusBadRequest {
		t.Fatalf("restore of traversal archive status=%d body=%s, want 400", code, body)
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("path traversal wrote a file outside the data dir (stat err=%v)", err)
	}
}

// TestRestoreSkipsNonAllowlisted drops archive entries outside the design-time
// allowlist (e.g. a crafted runtime/secret path) while still restoring valid
// design-time files.
func TestRestoreSkipsNonAllowlisted(t *testing.T) {
	dst, dir := newBackupServer(t)
	arc := makeTarGz(t, map[string]string{
		"secrets/evil.json": "nope",
		"drafts/ok.json":    `{"processId":"x"}`,
	})
	code, body := doReqBytes(t, dst, http.MethodPost, "/api/v1/restore", arc, "application/gzip")
	if code != http.StatusOK {
		t.Fatalf("restore status=%d body=%s", code, body)
	}
	if _, err := os.Stat(filepath.Join(dir, "secrets", "evil.json")); !os.IsNotExist(err) {
		t.Fatalf("non-allowlisted entry was written (stat err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "drafts", "ok.json")); err != nil {
		t.Fatalf("allowlisted entry not written: %v", err)
	}
}

// TestRestoreRejectsGarbage rejects a body that is not a valid gzip stream.
func TestRestoreRejectsGarbage(t *testing.T) {
	dst, _ := newBackupServer(t)
	code, _ := doReqBytes(t, dst, http.MethodPost, "/api/v1/restore", []byte("not a gzip"), "application/gzip")
	if code != http.StatusBadRequest {
		t.Fatalf("restore of garbage status=%d, want 400", code)
	}
}

// TestBackupSkipsTmpAndWalksSubdirs plants an in-flight .tmp file and a nested
// file, then checks the backup skips the former and captures the latter.
func TestBackupSkipsTmpAndWalksSubdirs(t *testing.T) {
	src, dir := newBackupServer(t)
	if err := os.MkdirAll(filepath.Join(dir, "drafts"), 0o755); err != nil {
		t.Fatalf("mkdir drafts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drafts", "keep.json"), []byte(`{"processId":"k"}`), 0o644); err != nil {
		t.Fatalf("write keep: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drafts", "skip.json.tmp"), []byte("half"), 0o644); err != nil {
		t.Fatalf("write tmp: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "dmn-models", "sub"), 0o755); err != nil {
		t.Fatalf("mkdir dmn sub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dmn-models", "sub", "m.dmn"), []byte("<dmn/>"), 0o644); err != nil {
		t.Fatalf("write dmn: %v", err)
	}

	code, archive := doReq(t, src, http.MethodGet, "/api/v1/backup", "", "")
	if code != http.StatusOK {
		t.Fatalf("backup status=%d", code)
	}
	names := tarGzEntries(t, archive)
	if !names["drafts/keep.json"] {
		t.Errorf("backup missing drafts/keep.json: %v", names)
	}
	if !names["dmn-models/sub/m.dmn"] {
		t.Errorf("backup missing nested dmn-models/sub/m.dmn: %v", names)
	}
	if names["drafts/skip.json.tmp"] {
		t.Errorf("backup wrongly included an in-flight .tmp file")
	}
}

// TestRestoreAcceptsDirEntriesAndNestedFiles restores an archive with an
// explicit directory entry (skipped) and a nested regular file (written).
func TestRestoreAcceptsDirEntriesAndNestedFiles(t *testing.T) {
	dst, dir := newBackupServer(t)
	arc := makeTarGz(t, map[string]string{
		"dmn-models/":        "",
		"dmn-models/a/b.dmn": "<dmn/>",
	})
	code, body := doReqBytes(t, dst, http.MethodPost, "/api/v1/restore", arc, "application/gzip")
	if code != http.StatusOK {
		t.Fatalf("restore status=%d body=%s", code, body)
	}
	if _, err := os.Stat(filepath.Join(dir, "dmn-models", "a", "b.dmn")); err != nil {
		t.Fatalf("nested file not written: %v", err)
	}
}

// TestRestoreReportsWriteFailure surfaces a 500 when a restored file cannot be
// written — here because a directory already occupies the target path.
func TestRestoreReportsWriteFailure(t *testing.T) {
	dst, dir := newBackupServer(t)
	// A directory where the archive wants to write a file makes the atomic rename
	// fail.
	if err := os.MkdirAll(filepath.Join(dir, "drafts", "collide.json"), 0o755); err != nil {
		t.Fatalf("plant collision dir: %v", err)
	}
	arc := makeTarGz(t, map[string]string{"drafts/collide.json": `{"processId":"x"}`})
	code, body := doReqBytes(t, dst, http.MethodPost, "/api/v1/restore", arc, "application/gzip")
	if code != http.StatusInternalServerError {
		t.Fatalf("restore onto a colliding dir status=%d body=%s, want 500", code, body)
	}
}

// TestRestoreRejectsCorruptTar rejects a valid gzip stream wrapping invalid tar
// bytes.
func TestRestoreRejectsCorruptTar(t *testing.T) {
	dst, _ := newBackupServer(t)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write([]byte("this is not a tar archive, just some bytes")); err != nil {
		t.Fatalf("gz write: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz close: %v", err)
	}
	code, _ := doReqBytes(t, dst, http.MethodPost, "/api/v1/restore", buf.Bytes(), "application/gzip")
	if code != http.StatusBadRequest {
		t.Fatalf("restore of corrupt tar status=%d, want 400", code)
	}
}

// TestRestoreRejectsTooManyEntries trips the entry-count guard. The entries are
// outside the allowlist, so the guard fires without writing 100k files to disk.
func TestRestoreRejectsTooManyEntries(t *testing.T) {
	dst, _ := newBackupServer(t)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for i := 0; i < 100_001; i++ {
		if err := tw.WriteHeader(&tar.Header{Name: "skip/f", Typeflag: tar.TypeReg, Mode: 0o644, Size: 0}); err != nil {
			t.Fatalf("write header %d: %v", i, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz close: %v", err)
	}
	code, body := doReqBytes(t, dst, http.MethodPost, "/api/v1/restore", buf.Bytes(), "application/gzip")
	if code != http.StatusBadRequest || !strings.Contains(string(body), "too many entries") {
		t.Fatalf("restore of oversized-entry-count archive status=%d body=%s, want 400 too-many-entries", code, body)
	}
}

func hasPrefix(names map[string]bool, prefix string) bool {
	for n := range names {
		if strings.HasPrefix(n, prefix) {
			return true
		}
	}
	return false
}

// doReqBytes is doReq for a raw binary request body.
func doReqBytes(t *testing.T, ts *httptest.Server, method, path string, body []byte, contentType string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, data
}

// TestBackupResponseHeaders checks the download is served as a gzip attachment.
func TestBackupResponseHeaders(t *testing.T) {
	src, _ := newBackupServer(t)
	req, _ := http.NewRequest(http.MethodGet, src.URL+"/api/v1/backup", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "application/gzip" {
		t.Fatalf("Content-Type=%q, want application/gzip", ct)
	}
	cd := res.Header.Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") || !strings.Contains(cd, ".tar.gz") {
		t.Fatalf("Content-Disposition=%q, want an attachment .tar.gz", cd)
	}
	// The body must be a valid gzip stream.
	if _, err := gzip.NewReader(res.Body); err != nil {
		t.Fatalf("body is not gzip: %v", err)
	}
}
