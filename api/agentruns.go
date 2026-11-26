package api

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/vault"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// --- An agent's run on the record (ADR-0117, as built by ADR-draft-agent-run-record) ---
//
// This file is the two edges of that record: the completion route taking a worker's
// account of the model call it made, and the read route giving an instance's calls
// back. The engine between them states everything that decides anything; see
// engine/agentrun.go.

// agentRunReport is a worker's account of one call to a language model, as a job
// completion carries it. It mirrors workersdk.AgentRunReport field for field.
type agentRunReport struct {
	Model            string `json:"model"`
	StopReason       string `json:"stopReason"`
	InputTokens      uint64 `json:"inputTokens"`
	OutputTokens     uint64 `json:"outputTokens"`
	CacheReadTokens  uint64 `json:"cacheReadTokens"`
	CacheWriteTokens uint64 `json:"cacheWriteTokens"`
	DurationMs       uint64 `json:"durationMs"`
	System           string `json:"system"`
	Prompt           string `json:"prompt"`
	Answer           string `json:"answer"`
	Wire             string `json:"wire"`
	Turn             string `json:"turn"`
	// Context is what the call was given of the process, by variable name.
	Context map[string]string `json:"context"`
}

// maxReportedTokens bounds one reported token count. No call reads or writes a trillion
// tokens; a report that says so is a broken worker, and summing it would wrap the run's
// totals round to a small number that reads as a run well inside its budget.
const maxReportedTokens = 1 << 40

// The names an agent run's texts are sealed under. A name is part of what an envelope is
// bound to (ADR-0314), so a sealed prompt cannot be moved into the answer's place.
const (
	agentRunSealPrompt = "agentRun.prompt"
	agentRunSealAnswer = "agentRun.answer"
	agentRunSealTurn   = "agentRun.turn"
	agentRunSealCtx    = "agentRun.context"
)

// parseAgentRunReport reads the account off a completion body. A body without one is
// not an error: a worker that reports nothing has still completed its job, and the
// engine records the call with what it knows itself.
func parseAgentRunReport(body []byte) (*agentRunReport, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}
	var payload struct {
		AgentRun *agentRunReport `json:"agentRun"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %v", err)
	}
	return payload.AgentRun, nil
}

// agentRunFromReport turns a worker's account into the value the completion command
// carries, or nil when there is no account or the job is not a call to a model. It runs
// off the run loop, takes one turn on it to read the job, and seals after.
//
// The texts of a call are the one place a model call can put personal data into the log
// in the clear: a prompt quotes the variables it was given, and a worker's payload holds
// those deciphered (ADR-0314). So where the job's process declares personal data, the
// texts are enciphered under the data subject's key before the command is issued —
// erasing the subject then reaches this copy like every other — and where that is not
// possible they are left out. The numbers are kept either way; they are about the call,
// not about anybody.
func (s *Server) agentRunFromReport(jobKey uint64, rep *agentRunReport) *model.AgentRunValue {
	if rep == nil {
		return nil
	}
	var (
		isCall   bool
		personal bool
		subject  string
	)
	s.do(func() {
		jv, ok, err := s.store.GetJob(jobKey)
		if err != nil || !ok {
			return // an unknown job is the handler's 404 to report
		}
		isCall = jv.JobType == compiler.AgentJobTypeIndex || jv.JobType == compiler.AiTaskJobTypeIndex
		cp := s.compiledOfScope(jv.ElementInstanceKey)
		if !isCall || cp == nil || !cp.HasPersonalVariables() {
			return
		}
		personal = true
		name := cp.DataSubjectVariable()
		_ = state.VisibleVariables(s.store, jv.ElementInstanceKey, func(v *model.VariableValue) error {
			if v.Name == name && subject == "" {
				subject = subjectText(v)
			}
			return nil
		})
	})
	if !isCall {
		return nil
	}
	run := &model.AgentRunValue{
		Model:            rep.Model,
		StopReason:       rep.StopReason,
		InputTokens:      min(rep.InputTokens, maxReportedTokens),
		OutputTokens:     min(rep.OutputTokens, maxReportedTokens),
		CacheReadTokens:  min(rep.CacheReadTokens, maxReportedTokens),
		CacheWriteTokens: min(rep.CacheWriteTokens, maxReportedTokens),
		DurationMs:       rep.DurationMs,
		System:           rep.System,
		Prompt:           rep.Prompt,
		Answer:           rep.Answer,
		Wire:             rep.Wire,
		Turn:             rep.Turn,
	}
	if len(rep.Context) > 0 {
		// A map marshals with its keys sorted, which is the canonical form (ADR-0037).
		if ctx, err := json.Marshal(rep.Context); err == nil {
			run.Context = string(ctx)
		}
	}
	if personal {
		s.sealAgentRunTexts(run, subject)
	}
	return run
}

// sealAgentRunTexts enciphers a run's texts under the subject's data key, or withholds
// them when there is no key to seal under. The standing instruction stays readable: it
// is the adapter's own text and says nothing about the case.
func (s *Server) sealAgentRunTexts(run *model.AgentRunValue, subject string) {
	withhold := func() {
		run.Prompt, run.Answer, run.Turn, run.Context = "", "", "", ""
		run.TextState = model.AgentRunTextWithheld
	}
	if s.vault == nil || subject == "" {
		withhold()
		return
	}
	for _, f := range []struct {
		name string
		text *string
	}{
		{agentRunSealPrompt, &run.Prompt}, {agentRunSealAnswer, &run.Answer},
		{agentRunSealTurn, &run.Turn}, {agentRunSealCtx, &run.Context},
	} {
		if *f.text == "" {
			continue
		}
		env, err := s.vault.Seal(subject, f.name, uint8(model.VarString), *f.text)
		if err != nil {
			withhold()
			return
		}
		*f.text = env.Text()
	}
	run.TextState = model.AgentRunTextEnciphered
}

// openAgentRunText gives a stored text back readable. ok is false when its key has been
// destroyed — an erasure, which is an answer and not a failure.
func (s *Server) openAgentRunText(name, stored string) (string, bool) {
	if stored == "" {
		return "", true
	}
	env, isEnvelope := vault.ParseEnvelope(stored)
	if !isEnvelope || s.vault == nil {
		return "", false
	}
	plain, err := s.vault.Open(name, env)
	if err != nil {
		return "", false
	}
	return plain, true
}

// agentRunView is one fact of an agent's run as the read route answers it.
type agentRunView struct {
	At    int64  `json:"at"`
	AtKey string `json:"atKey"`
	// Kind is "modelCall" or "toolResult".
	Kind               string `json:"kind"`
	ElementID          string `json:"elementId"`
	ElementInstanceKey string `json:"elementInstanceKey"`
	JobKey             string `json:"jobKey,omitempty"`
	Round              uint32 `json:"round"`
	Model              string `json:"model,omitempty"`
	StopReason         string `json:"stopReason,omitempty"`
	InputTokens        uint64 `json:"inputTokens"`
	OutputTokens       uint64 `json:"outputTokens"`
	CacheReadTokens    uint64 `json:"cacheReadTokens"`
	CacheWriteTokens   uint64 `json:"cacheWriteTokens"`
	DurationMs         uint64 `json:"durationMs"`
	// TotalInputTokens and TotalOutputTokens are the element instance's sums up to and
	// including this call.
	TotalInputTokens  uint64 `json:"totalInputTokens"`
	TotalOutputTokens uint64 `json:"totalOutputTokens"`
	// Limit is "rounds" or "tokens" when this call reached that bound.
	Limit     string          `json:"limit,omitempty"`
	ToolCalls json.RawMessage `json:"toolCalls,omitempty"`
	CallID    string          `json:"callId,omitempty"`
	// Text says what became of the texts below: "clear", "erased" when the data
	// subject's key was destroyed, or "withheld" when they were never stored.
	Text   string `json:"text"`
	System string `json:"system,omitempty"`
	Prompt string `json:"prompt,omitempty"`
	Answer string `json:"answer,omitempty"`
}

// handleInstanceAgentRuns returns what a process instance's agents did, in the order it
// happened: every call to a language model with the model that answered, what it read
// and wrote in tokens and the element's running totals, and what each tool an agent
// called returned. Live and after the instance finished, like its decisions.
func (s *Server) handleInstanceAgentRuns(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid instance key")
		return
	}
	type fact struct {
		ts  int64
		pos uint64
		v   model.AgentRunValue
	}
	var facts []fact
	elementIDs := map[uint64]map[int32]string{}
	err = s.readOffLoop(func(view *state.ReadView, defs defIndex) error {
		return view.AgentRunHistory(key, func(ts int64, pos uint64, v *model.AgentRunValue) error {
			facts = append(facts, fact{ts, pos, *v})
			ids := elementIDs[v.ProcessDefKey]
			if ids == nil {
				ids = map[int32]string{}
				elementIDs[v.ProcessDefKey] = ids
			}
			if _, seen := ids[v.ElementId]; !seen {
				if cp := defs[v.ProcessDefKey].cp; cp != nil {
					ids[v.ElementId] = cp.ElementBpmnId(v.ElementId)
				}
			}
			return nil
		})
	})
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The store keeps the facts agent by agent; a reader wants the instance's own order,
	// which is the log's.
	slices.SortFunc(facts, func(a, b fact) int { return cmp.Compare(a.pos, b.pos) })
	// Opening a sealed text reads the vault, which is why it happens here, after the
	// scan, with the loop long since free.
	out := make([]agentRunView, 0, len(facts))
	for _, f := range facts {
		v := f.v
		view := agentRunView{
			At: f.ts, AtKey: strconv.FormatInt(f.ts, 10),
			Kind:               "modelCall",
			ElementID:          elementIDs[v.ProcessDefKey][v.ElementId],
			ElementInstanceKey: strconv.FormatUint(v.ElementInstanceKey, 10),
			Round:              v.Round,
			Model:              v.Model,
			StopReason:         v.StopReason,
			InputTokens:        v.InputTokens,
			OutputTokens:       v.OutputTokens,
			CacheReadTokens:    v.CacheReadTokens,
			CacheWriteTokens:   v.CacheWriteTokens,
			DurationMs:         v.DurationMs,
			TotalInputTokens:   v.SumInputTokens,
			TotalOutputTokens:  v.SumOutputTokens,
			CallID:             v.CallId,
			System:             v.System,
			Text:               "clear",
		}
		if v.Kind == model.AgentRunToolResult {
			view.Kind = "toolResult"
		}
		if v.JobKey != 0 {
			view.JobKey = strconv.FormatUint(v.JobKey, 10)
		}
		switch v.Limit {
		case model.AgentRunLimitRounds:
			view.Limit = "rounds"
		case model.AgentRunLimitTokens:
			view.Limit = "tokens"
		}
		if v.ToolCalls != "" {
			view.ToolCalls = json.RawMessage(v.ToolCalls)
		}
		switch v.TextState {
		case model.AgentRunTextWithheld:
			view.Text = "withheld"
		case model.AgentRunTextEnciphered:
			prompt, okP := s.openAgentRunText(agentRunSealPrompt, v.Prompt)
			answer, okA := s.openAgentRunText(agentRunSealAnswer, v.Answer)
			if okP && okA {
				view.Prompt, view.Answer = prompt, answer
			} else {
				view.Text = "erased"
			}
		default:
			view.Prompt, view.Answer = v.Prompt, v.Answer
		}
		out = append(out, view)
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// openAgentRunForReplay gives the texts a later round is built from back readable: what
// the call added, the model's turn and the context it was given. It reports false when
// any of them cannot be read — withheld, or enciphered under a key that is gone — in
// which case the run is not replayed (see agent.WithTranscript).
//
// It reads the vault on the path that resolves a job for a worker, which is the edge
// ADR-0314 deciphers at, and where the variables of the same payload are opened.
func (s *Server) openAgentRunForReplay(v *model.AgentRunValue) (prompt, turn, context string, ok bool) {
	switch v.TextState {
	case model.AgentRunTextWithheld:
		return "", "", "", false
	case model.AgentRunTextEnciphered:
		var okP, okT, okC bool
		prompt, okP = s.openAgentRunText(agentRunSealPrompt, v.Prompt)
		turn, okT = s.openAgentRunText(agentRunSealTurn, v.Turn)
		context, okC = s.openAgentRunText(agentRunSealCtx, v.Context)
		return prompt, turn, context, okP && okT && okC
	}
	return v.Prompt, v.Turn, v.Context, true
}
