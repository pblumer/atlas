package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/pblumer/atlas/api/blueprintregistry"
)

// tracedBlueprintSource is a registry source that can report the way to the
// registry station by station while it loads the index (the pinned public client
// does). A source that cannot is still listed; its connection reports no stations.
type tracedBlueprintSource interface {
	LoadIndexTraced(context.Context, func(blueprintregistry.Step)) (blueprintregistry.Index, error)
	Host() string
}

// connectionStart opens the stream: which stations follow, to which host, from
// which atlas.
type connectionStart struct {
	Type         string   `json:"type"`
	Host         string   `json:"host,omitempty"`
	AtlasVersion string   `json:"atlasVersion"`
	Stations     []string `json:"stations"`
}

// connectionStep is one station as the caller sees it. Error is the error text
// the station stopped at, for an administrator only: it can name this network's
// resolver or addresses. Everyone else reads the cause, from a fixed vocabulary.
type connectionStep struct {
	Type string `json:"type"`
	blueprintregistry.Step
	Error string `json:"error,omitempty"`
}

// connectionDone closes the stream. On success it carries the verified index —
// the same the list route answers — and how many of its releases this atlas may
// install, so the Console needs no second load.
type connectionDone struct {
	Type         string                   `json:"type"`
	OK           bool                     `json:"ok"`
	Millis       int64                    `json:"ms"`
	AtlasVersion string                   `json:"atlasVersion"`
	Index        *blueprintregistry.Index `json:"index,omitempty"`
	Releases     int                      `json:"releases"`
	Compatible   int                      `json:"compatible"`
	// Incompatible names each release this atlas is too old (or too new) for, with
	// the oldest atlas it asks for: it is listed, and refused at its preview.
	Incompatible []incompatibleRelease `json:"incompatible,omitempty"`
}

type incompatibleRelease struct {
	BlueprintID string `json:"blueprintId"`
	Version     string `json:"version"`
	MinAtlas    string `json:"minAtlas,omitempty"`
}

// handleBlueprintConnection loads the signed registry index as the list route
// does, and streams each station of the way there as it is passed or where it
// stops, one JSON object per line (application/x-ndjson): a start line, a line
// per station, a done line. It is the same load, not a probe beside it, so a
// person watching it sees where the real request ends — the name, the
// connection, the TLS handshake, the index, the signature.
func (s *Server) handleBlueprintConnection(w http.ResponseWriter, r *http.Request) {
	if !s.requireBlueprints(w) {
		return
	}
	traced, ok := s.blueprints.(tracedBlueprintSource)
	host := ""
	if ok {
		host = traced.Host()
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	enc := json.NewEncoder(w)
	send := func(v any) {
		_ = enc.Encode(v)
		if flusher != nil {
			flusher.Flush()
		}
	}
	admin := s.isAdmin(r)
	start := time.Now()
	send(connectionStart{Type: "start", Host: host, AtlasVersion: Version, Stations: blueprintregistry.Stations()})

	type result struct {
		index blueprintregistry.Index
		err   error
	}
	// The stations come from the transport's goroutines; only this one writes the
	// response. Five stations at most, so the buffer never blocks the load.
	steps := make(chan blueprintregistry.Step, 16)
	done := make(chan result, 1)
	go func() {
		var res result
		if ok {
			res.index, res.err = traced.LoadIndexTraced(r.Context(), func(st blueprintregistry.Step) { steps <- st })
		} else {
			res.index, res.err = s.blueprints.LoadIndex(r.Context())
		}
		done <- res
	}()
	line := func(st blueprintregistry.Step) connectionStep {
		out := connectionStep{Type: "step", Step: st}
		if st.Err != nil && admin {
			out.Error = st.Err.Error()
		}
		return out
	}
	for {
		select {
		case st := <-steps:
			send(line(st))
		case res := <-done:
			// Every station was handed over before the load returned.
			for drained := false; !drained; {
				select {
				case st := <-steps:
					send(line(st))
				default:
					drained = true
				}
			}
			out := connectionDone{Type: "done", OK: res.err == nil, Millis: time.Since(start).Milliseconds(), AtlasVersion: Version}
			if res.err != nil {
				logRegistryUnavailable(res.err)
			} else {
				out.Index = &res.index
				out.Releases = len(res.index.Releases)
				for _, rel := range res.index.Releases {
					if blueprintregistry.Compatible(rel.Atlas, Version) {
						out.Compatible++
					} else {
						out.Incompatible = append(out.Incompatible, incompatibleRelease{
							BlueprintID: rel.BlueprintID, Version: rel.Version, MinAtlas: rel.Atlas.MinVersion})
					}
				}
			}
			send(out)
			return
		}
	}
}

// Compile-time: the pinned public client is a traced source.
var _ tracedBlueprintSource = (*blueprintregistry.PublicClient)(nil)
