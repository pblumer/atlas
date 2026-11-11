package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/pblumer/atlas/api"
)

// The line where the Operations replay stops loading a whole timeline is the
// operator's to lower (--replay-full-max-iterations), and /api/v1/info is how the
// browser learns it. Nothing may raise it above the ceiling.
func TestInfoSaysWhereTheFullReplayEnds(t *testing.T) {
	cases := []struct {
		name string
		opts []api.Option
		want int
	}{
		{"default", nil, api.DefaultFullReplayMaxIterations},
		{"lowered", []api.Option{api.WithFullReplayMaxIterations(120)}, 120},
		{"zero", []api.Option{api.WithFullReplayMaxIterations(0)}, 0},
		{"above the ceiling", []api.Option{api.WithFullReplayMaxIterations(5000)}, api.DefaultFullReplayMaxIterations},
		{"negative", []api.Option{api.WithFullReplayMaxIterations(-3)}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := newTestServerWith(t, tc.opts...)
			code, raw := doReq(t, ts, http.MethodGet, "/api/v1/info", "", "")
			if code != http.StatusOK {
				t.Fatalf("info: %d (%s)", code, raw)
			}
			var info struct {
				FullReplayMaxIterations *int `json:"fullReplayMaxIterations"`
			}
			if err := json.Unmarshal(raw, &info); err != nil {
				t.Fatalf("decode info: %v", err)
			}
			if info.FullReplayMaxIterations == nil {
				t.Fatalf("info carries no fullReplayMaxIterations: %s", raw)
			}
			if *info.FullReplayMaxIterations != tc.want {
				t.Errorf("fullReplayMaxIterations = %d, want %d", *info.FullReplayMaxIterations, tc.want)
			}
		})
	}
}
