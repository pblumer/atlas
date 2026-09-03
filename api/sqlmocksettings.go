package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/connector/sqldb"
)

// The Console's database mockup switch (ADR-0221).
//
// [ADR-0173] decided the mockup belongs to the *worker* rather than to the model, and
// that is unchanged — this only moves where the operator reaches it, from the process
// environment into the Console, because a variable set once at start is the wrong
// ceremony for a thing you flip while trying a process out. It is the move
// ADR-0193 already made for the Active Directory mockup, and
// everything here is that switch with a seed of answers in place of a seed of entries.
//
// [ADR-0173]: https://github.com/pblumer/atlas/blob/main/docs/adr/0173-generic-sql-connector.md

// maxSQLMockBytes bounds the mockup body, which carries the seed's whole text. It is
// the AD switch's limit for the same reason: a seed of a few hundred answers is
// unremarkable, and past this the answer is a smaller seed rather than a bigger field.
const maxSQLMockBytes = 1 << 18 // 256 KiB

// handleGetSQLMock reports the switch and what is loaded behind it.
//
// "configured" tells the Console the difference between a decision made here and none
// at all — without a record the server's own environment decides, and the switch must
// not claim otherwise.
//
// The seed's *content* is admin-only, the rest is not. What the switch is set to
// answers a question every operator watching a database task has — did that row really
// get written? — so hiding it would be the wrong secrecy. The seed is a different
// matter: it is shaped like the answers a production query returns, and there is no
// reason for everyone signed in to read one.
func (s *Server) handleGetSQLMock(w http.ResponseWriter, r *http.Request) {
	var (
		m      sqlMockSetting
		stored bool
		err    error
	)
	s.do(func() { m, stored, err = s.settings.getSQLMock() })
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read sql mock: "+err.Error())
		return
	}
	seed := ""
	if s.isAdmin(r) {
		seed = m.Seed
	}
	httpapi.JSON(w, http.StatusOK, struct {
		Enabled    bool   `json:"enabled"`
		Seed       string `json:"seed,omitempty"`
		SeedName   string `json:"seedName,omitempty"`
		Answers    int    `json:"seedAnswers,omitempty"`
		HasSeed    bool   `json:"hasSeed"`
		Configured bool   `json:"configured"`
	}{
		Enabled: m.Enabled, Seed: seed, SeedName: m.SeedName,
		Answers: m.SeedAnswers, HasSeed: strings.TrimSpace(m.Seed) != "", Configured: stored,
	})
}

// handleSetSQLMock stores the switch and restarts the supervised SQL workers holding
// it.
func (s *Server) handleSetSQLMock(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSQLMockBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var p struct {
		Enabled  bool   `json:"enabled"`
		Seed     string `json:"seed"`
		SeedName string `json:"seedName"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	m := sqlMockSetting{
		Enabled:  p.Enabled,
		Seed:     strings.TrimSpace(p.Seed),
		SeedName: strings.TrimSpace(p.SeedName),
	}
	// Parse the seed here, where the person who can fix it is waiting for an answer.
	// The worker parses it again at startup and degrades to an unseeded mock if it
	// cannot — which is right there, because refusing to start over an optional file is
	// a restart loop (ADR-0202) — but a mockup that quietly answers nothing is a bad
	// way to learn about a typo, and this is the one moment somebody is looking at the
	// thing they just wrote.
	if m.Seed != "" {
		answers, err := sqldb.ParseMockSeed([]byte(m.Seed))
		if err != nil {
			httpapi.Error(w, http.StatusBadRequest, err.Error())
			return
		}
		if len(answers) == 0 {
			// An empty answers list parses but says nothing, and a mockup with no
			// answers refuses every statement. That is a legitimate state, reached by
			// storing no seed at all — reaching it by uploading a document that looks
			// like a seed is almost always a file the operator picked by mistake.
			httpapi.Error(w, http.StatusBadRequest,
				"the seed holds no answers; remove it to start from an unseeded mockup, "+
					"or add an entry under \"answers\"")
			return
		}
		m.SeedAnswers = len(answers)
	}
	var saveErr error
	s.doAndRefresh(func() { saveErr = s.settings.saveSQLMock(m) })
	if saveErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "save sql mock: "+saveErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, struct {
		Enabled  bool   `json:"enabled"`
		SeedName string `json:"seedName,omitempty"`
		Answers  int    `json:"seedAnswers,omitempty"`
		HasSeed  bool   `json:"hasSeed"`
	}{
		Enabled: m.Enabled, SeedName: m.SeedName, Answers: m.SeedAnswers, HasSeed: m.Seed != "",
	})
}

// sqlMockEnabled reports whether this server's Console switch turns the database
// mockup on. It is the one place that answers the question, so the supervised
// environment, the worker-create rule and the Console's own hints cannot disagree
// about what "mockup mode" means here.
//
// A stored record decides either way; without one the host's own ATLAS_<PRODUCT>_MOCK
// decides, and this reports false — the server cannot read a child's environment, and
// guessing would make a create refuse or accept on something it cannot see.
//
// It reads the settings store, so it runs on the run-loop goroutine (I3) and must be
// called from inside s.do, not around it.
func (s *Server) sqlMockEnabledLocked() bool {
	m, stored, err := s.settings.getSQLMock()
	return err == nil && stored && m.Enabled
}
