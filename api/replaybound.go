package api

// Where the full instance replay hands over to the bounded one (ADR-0517).
//
// The Operations replay loads an instance's whole timeline — every step and every
// token frame — unless the instance runs a multi-instance body of more iterations
// than this, or more than four times this across its bodies. Above it the replay
// shows the bounded history and the Multi-Instance Explorer, which page on the server.
// The decision is the browser's, from one page of the bounded history; the server
// only says where the line is (/api/v1/info), so the operator sets it once for every
// viewer.
//
// The line can be lowered, not raised. The timeline grows with the square of the
// iteration count — 139 MB at 1,000 iterations, 2.2 GB at 4,000 (ADR-0517) — so the
// default is a ceiling on harm, not a tuning point. What an installation may want is
// the other direction: a subprocess iteration runs several steps, so a few hundred of
// them already make a timeline of thousands of rows and frames, and the bounded view
// is the better one well below the default. Zero sends every instance that runs a
// multi-instance activity to the bounded view.

// DefaultFullReplayMaxIterations is the largest multi-instance body the full replay
// loads by default, and the most it may ever be set to.
const DefaultFullReplayMaxIterations = 500

// WithFullReplayMaxIterations sets the largest multi-instance body the Operations
// replay still loads in full; a larger instance opens in the bounded view. A value
// outside 0..DefaultFullReplayMaxIterations is clamped into it — the caller (the
// serve command) refuses one before it gets here, so this only keeps a programmatic
// caller from raising the ceiling.
func WithFullReplayMaxIterations(n int) Option {
	return func(s *Server) {
		s.fullReplayMax = min(max(n, 0), DefaultFullReplayMaxIterations)
		s.fullReplayMaxSet = true
	}
}

// fullReplayMaxIterations is the line /api/v1/info reports.
func (s *Server) fullReplayMaxIterations() int {
	if !s.fullReplayMaxSet {
		return DefaultFullReplayMaxIterations
	}
	return s.fullReplayMax
}
