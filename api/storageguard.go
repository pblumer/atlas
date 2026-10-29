package api

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/storage"
)

// The server's half of the storage guard (ADR-0481): which directories
// the monitor reads, which requests a frozen or halted server still answers, and the
// background work it stops starting.
//
// Nothing here may lose a write somebody was told succeeded, and nothing does: every
// refusal happens before a command reaches the engine, so the caller hears no and
// keeps what it sent. The guard does not cache refused work in memory to apply later.
// That would be a promise the server could only keep by not crashing, which is the
// one thing a server short of disk cannot promise — the durable copy of a refused
// write is the one the caller still holds.

// newStorageMonitor builds the monitor over the directories this server writes to.
// The WAL and the state store are usually on the data directory's file system, and
// the monitor then reads it once; a volume mounted for either is read on its own,
// and the tighter one decides.
func (s *Server) newStorageMonitor() *storage.Monitor {
	dirs := []string{s.dataDir}
	for _, sub := range []string{"wal", "state"} {
		dir := filepath.Join(s.dataDir, sub)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			dirs = append(dirs, dir)
		}
	}
	return storage.New(storage.Config{
		Dirs:    dirs,
		Reserve: s.storageReserve,
		Read:    s.storageRead,
		Halted:  s.proc.Halted,
		Footprint: func() storage.Footprint {
			segments, bytes := s.walFootprint()
			return storage.Footprint{WALBytes: bytes, WALSegments: segments, StateBytes: s.store.DiskSpaceUsage()}
		},
	})
}

// writesPaused reports whether the server starts no work of its own: the disk is
// below the reserve, or the engine has halted. The timer scheduler, the retention
// sweep, the inbound bridge and the in-process workers ask it before every round;
// what they skip is still due and runs once writes resume, as it would after a
// restart.
func (s *Server) writesPaused() bool {
	return s.storage.Frozen() || s.proc.Halted() != nil
}

// writesWhileFrozen are the requests a frozen server still takes, each with the reason
// it may. Everything else that is not a read is refused while the disk is below the
// reserve. The list is short on purpose: each entry spends reserve.
var writesWhileFrozen = map[string]string{
	"POST /api/v1/auth/login":    "an operator has to be able to sign in to see why the server is frozen; a session is a few bytes",
	"POST /api/v1/auth/logout":   "ending a session gives space back rather than taking it",
	"POST /api/v1/auth/presence": "held in memory; it writes nothing",
	"POST " + oauthTokenPath:     "a machine client's access token, as small as a session",
	"POST " + oauthRevokePath:    "giving a grant back deletes a record; it gives space back rather than taking it",
	"POST /api/v1/checkpoints":   "a checkpoint followed by WAL compaction is how the server gives log space back",
	// A job a worker has finished has done its work already. Refusing the report would
	// not undo it: the lease would run out and the job would run a second time.
	"POST /api/v1/jobs/{key}/complete":              "the work has already happened; refusing the report would run it again",
	"POST /api/v1/jobs/{key}/fail":                  "the attempt has already happened; refusing the report would run it again",
	"POST /api/v1/jobs/{key}/error":                 "the work has already happened; refusing the report would run it again",
	"POST /api/v1/drafts/{id}/session/presence":     "collaborative presence is held in memory; it writes nothing",
	"POST /api/v1/dmn-drafts/{id}/session/presence": "collaborative presence is held in memory; it writes nothing",
	// The MCP transport is a door, not a write: every call a tool makes into the API
	// comes back through this check as a request of its own.
	"/mcp":  "the transport; each API call a tool makes passes this check on its own",
	"/mcp/": "the transport; each API call a tool makes passes this check on its own",
}

// withStorageAdmission refuses writes while the disk is below the reserve (507, and
// the client may retry) or the engine has halted (503, and only a restart helps).
// Reads always pass. The check is one atomic load per request until the server is
// actually frozen; only then is the route looked up, to find the few writes it takes.
func (s *Server) withStorageAdmission(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isRead(r.Method) {
			mux.ServeHTTP(w, r)
			return
		}
		frozen := s.storage.Frozen()
		halted := s.proc.Halted()
		if !frozen && halted == nil {
			mux.ServeHTTP(w, r)
			return
		}
		if _, pattern := mux.Handler(r); pattern != "" {
			if _, ok := writesWhileFrozen[pattern]; ok && halted == nil {
				mux.ServeHTTP(w, r)
				return
			}
		}
		if halted != nil {
			httpapi.Error(w, http.StatusServiceUnavailable,
				"the engine halted after a write to disk failed and takes no writes until the server is restarted; "+
					"free space first if the disk is full (see GET /api/v1/storage)")
			return
		}
		w.Header().Set("Retry-After", "60")
		httpapi.Error(w, http.StatusInsufficientStorage,
			"the disk under the data directory is below its reserve, so the server takes no new writes; "+
				"nothing was changed — retry once space has been freed (see GET /api/v1/storage)")
	})
}

func isRead(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}
