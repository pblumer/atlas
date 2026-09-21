package api

// The two edges of ADR-0314's crypto-shredding: values a process declares personal are
// sealed under the data subject's key on the way *in*, and opened again on the way *out*
// at the two places the record permits plaintext — the payload handed to a worker, and
// the form or task detail a person is shown.
//
// The engine itself is untouched, which is the whole of how this stays inside the
// invariants. A command already carries ciphertext, so nothing is enciphered per command
// on the processor path (I1). applyToState stores and returns bytes: it never holds a
// key, never fails because one is gone, and replays an erased subject's instance exactly
// as it replayed it before, because the ciphertext is still there and still
// deterministic (I4). Deciphering happens in handlers and in the post-commit phase,
// where reading a key is permitted (I2).

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/vault"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// ---------------------------------------------------------------------------
// In: sealing at the edge
// ---------------------------------------------------------------------------

// personalSealing is the decision "these values must be sealed, under this subject's
// key". It is resolved on the run loop, because finding the subject means reading the
// instance's variables, and then used off it, because sealing reads the vault.
type personalSealing struct {
	cp      *compiler.CompiledProcess
	subject string
}

// needed reports whether anything has to be sealed.
func (p personalSealing) needed() bool { return p.cp != nil }

// personalSealingFor decides on the run loop whether vars carry any value this process
// declared personal, and if so whose data key seals them. It must be called on the loop
// (it reads the store) and returns before any crypto happens.
//
// scope is the element instance whose variable chain names the data subject — a job's
// element instance, or the process instance root — so a subject shadowed inside a
// subprocess resolves the way the model's own scoping says it should. A scope of 0 is a
// start: no instance exists yet, so the subject can only come from the submitted values
// themselves.
func (s *Server) personalSealingFor(cp *compiler.CompiledProcess, scope uint64, vars []model.VariableValue) (personalSealing, error) {
	if cp == nil || !cp.HasPersonalVariables() || len(vars) == 0 {
		return personalSealing{}, nil
	}
	// Nothing declared is actually being written: the overwhelmingly common case, and it
	// costs one pass over a handful of names and no store read at all.
	any := false
	for i := range vars {
		if cp.IsPersonal(vars[i].Name) && vars[i].Kind != model.VarNull {
			any = true
			break
		}
	}
	if !any {
		return personalSealing{}, nil
	}
	name := cp.DataSubjectVariable()
	subject := ""
	if scope != 0 {
		if err := state.VisibleVariables(s.store, scope, func(v *model.VariableValue) error {
			if v.Name == name && subject == "" {
				subject = subjectText(v)
			}
			return nil
		}); err != nil {
			return personalSealing{}, fmt.Errorf("read the data subject %q: %w", name, err)
		}
	}
	if subject == "" {
		// A start carries its subject in the same submission as the personal values, and
		// a later write can too — an output mapping that writes both at once.
		for i := range vars {
			if vars[i].Name == name {
				subject = subjectText(&vars[i])
				break
			}
		}
	}
	if subject == "" {
		return personalSealing{}, fmt.Errorf(
			"this process declares personal data and names %q as the data subject, but %q has no value here: a personal value can only be enciphered under the key of the person it belongs to, and a value stored in the clear could never be erased. Supply %q with the personal values (ADR-0314)",
			name, name, name)
	}
	return personalSealing{cp: cp, subject: subject}, nil
}

// subjectText is a data subject id as the vault names a key by: the text of a string or
// a number, and nothing else. A structured or boolean subject is refused by returning ""
// — it would name a key nobody could ask an erasure for.
func subjectText(v *model.VariableValue) string {
	switch v.Kind {
	case model.VarString, model.VarNumber:
		return strings.TrimSpace(v.Text)
	default:
		return ""
	}
}

// seal enciphers in place every declared personal value in vars. It runs off the run
// loop: it reads the vault, and a value already sealed is left alone so a re-submitted
// value is not wrapped twice.
func (s *Server) seal(p personalSealing, vars []model.VariableValue) error {
	if !p.needed() {
		return nil
	}
	if s.vault == nil {
		return fmt.Errorf("this process declares personal data, which is enciphered under a key in the vault, and this server runs with the vault disabled (ADR-0314)")
	}
	for i := range vars {
		v := &vars[i]
		if !p.cp.IsPersonal(v.Name) || v.Kind == model.VarNull {
			continue
		}
		if v.Kind == model.VarJSON && vault.IsEnciphered(v.Text) {
			continue // already sealed: a value read out and written back unchanged
		}
		plain := v.Text
		if v.Kind == model.VarBool {
			plain = "false"
			if v.Bool {
				plain = "true"
			}
		}
		env, err := s.vault.Seal(p.subject, v.Name, uint8(v.Kind), plain)
		if err != nil {
			return err
		}
		// The stored value *is* the envelope, so its kind is what the envelope is: JSON.
		// The kind the value had travels inside it and comes back on the way out.
		v.Kind, v.Text, v.Bool = model.VarJSON, env.Text(), false
	}
	return nil
}

// encipherStartVars seals the personal values of a submission that starts an instance.
// defKey is the definition being started.
func (s *Server) encipherStartVars(defKey uint64, vars []model.VariableValue) error {
	if len(vars) == 0 {
		return nil
	}
	var (
		p   personalSealing
		err error
	)
	s.do(func() {
		d, ok := s.deployments[defKey]
		if !ok {
			return
		}
		p, err = s.personalSealingFor(d.cp, 0, vars)
	})
	if err != nil {
		return err
	}
	return s.seal(p, vars)
}

// encipherScopeVars seals the personal values written into a live instance, resolving the
// data subject up the given scope's chain. scope is an element instance key or the
// process instance root.
func (s *Server) encipherScopeVars(scope uint64, vars []model.VariableValue) error {
	if len(vars) == 0 {
		return nil
	}
	var (
		p   personalSealing
		err error
	)
	s.do(func() {
		cp := s.compiledOfScope(scope)
		p, err = s.personalSealingFor(cp, scope, vars)
	})
	if err != nil {
		return err
	}
	return s.seal(p, vars)
}

// encipherJobVars seals the personal values a worker reports back on a job. Runs off the
// loop, before the completion command is issued, so the command already holds ciphertext.
func (s *Server) encipherJobVars(jobKey uint64, vars []model.VariableValue) error {
	if len(vars) == 0 {
		return nil
	}
	var (
		p   personalSealing
		err error
	)
	s.do(func() {
		jv, ok, gerr := s.store.GetJob(jobKey)
		if gerr != nil || !ok {
			return // an unknown job is the handler's 404 to report, not this edge's
		}
		// The job's own scope, so the subject resolves as the task saw it.
		p, err = s.personalSealingFor(s.compiledOfScope(jv.ElementInstanceKey), jv.ElementInstanceKey, vars)
	})
	if err != nil {
		return err
	}
	return s.seal(p, vars)
}

// compiledOfScope is the compiled process a scope belongs to, or nil. Runs on the run
// loop: it reads the store and the loop-owned deployment map.
func (s *Server) compiledOfScope(scope uint64) *compiler.CompiledProcess {
	defKey := uint64(0)
	if ei, ok, err := s.store.GetElementInstance(scope); err == nil && ok {
		defKey = ei.ProcessDefKey
	} else if pi, ok, err := s.store.ProcessInstance(scope); err == nil && ok {
		defKey = pi.ProcessDefKey
	}
	if defKey == 0 {
		return nil
	}
	if d, ok := s.deployments[defKey]; ok {
		return d.cp
	}
	return nil
}

// ---------------------------------------------------------------------------
// Out: opening at the edge
// ---------------------------------------------------------------------------

// personalReader is a [state.Reader] that opens enciphered values as it yields them.
//
// It exists because every connector resolves its own expressions over
// [state.VisibleVariablesMap], which takes a Reader — so wrapping the reader is the one
// intervention that makes *every* connector, present and future, evaluate over plaintext
// rather than over an envelope. ADR-0314 sends transforms that combine personal values
// into the worker that needs the result, and compiler/personal.go exempts those
// expressions from the refusal on exactly that basis: the exemption is only true if the
// values a worker binds are opened before it binds them. This is where that happens.
//
// A value whose subject has been erased is an error, not an empty string. That is the
// consequence ADR-0314 predicts and accepts: an erased subject's live instance can no
// longer provision, and it says so instead of calling a far-end API with a blank name.
type personalReader struct {
	state.Reader
	vault *vault.Vault
}

// personalReader wraps r so the variables it yields are plaintext. It returns r
// unchanged when there is no vault, because then nothing can be enciphered either.
func (s *Server) personalReader(r state.Reader) state.Reader {
	if s.vault == nil {
		return r
	}
	return personalReader{Reader: r, vault: s.vault}
}

func (p personalReader) VariablesOfScope(scope uint64, fn func(v *model.VariableValue) error) error {
	return p.Reader.VariablesOfScope(scope, func(v *model.VariableValue) error {
		opened, err := p.open(v)
		if err != nil {
			return err
		}
		return fn(opened)
	})
}

// open returns the plaintext form of one variable, or the variable itself when it is not
// enciphered. It never writes through the pointer it is given: that value belongs to the
// store's iteration, and mutating it would corrupt what the reader is reading.
func (p personalReader) open(v *model.VariableValue) (*model.VariableValue, error) {
	if v.Kind != model.VarJSON {
		return v, nil
	}
	env, ok := vault.ParseEnvelope(v.Text)
	if !ok {
		return v, nil
	}
	plain, err := p.vault.Open(v.Name, env)
	if err != nil {
		return nil, fmt.Errorf("variable %q: %w", v.Name, err)
	}
	out := *v
	out.Kind = model.VarKind(env.Kind)
	out.Text, out.Bool = plain, false
	if out.Kind == model.VarBool {
		out.Text, out.Bool = "", plain == "true"
	}
	return &out, nil
}

// ---------------------------------------------------------------------------
// Erasure
// ---------------------------------------------------------------------------

// handleListDataSubjects lists the data subjects that still hold a key, which is the
// operator's view of what an erasure could still act on (ADR-0314). It reveals ids and
// no content: an id is a reference, which is the part this design deliberately keeps
// readable.
func (s *Server) handleListDataSubjects(w http.ResponseWriter, _ *http.Request) {
	if s.vault == nil {
		httpapi.Error(w, http.StatusServiceUnavailable, "vault not configured")
		return
	}
	var (
		metas   []vault.Meta
		loadErr error
	)
	s.do(func() { metas, loadErr = s.vault.DataSubjects() })
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list data subjects: "+loadErr.Error())
		return
	}
	if metas == nil {
		metas = []vault.Meta{}
	}
	httpapi.JSON(w, http.StatusOK, metas)
}

// handleEraseDataSubject destroys one data subject's key, which is the whole of an
// erasure: every copy of every value ever sealed under it becomes permanently
// unreadable, in the live store and in every checkpoint, export and backup, without any
// of them having to be found. It is idempotent, so a repeated request answers the same
// way.
//
// What it does not claim is physical removal of the bytes. Whether rendering data
// permanently unreadable satisfies a given supervisory authority is the operator's data
// protection officer's judgement, which is why this route reports what it did rather
// than certifying an outcome.
func (s *Server) handleEraseDataSubject(w http.ResponseWriter, r *http.Request) {
	if s.vault == nil {
		httpapi.Error(w, http.StatusServiceUnavailable, "vault not configured")
		return
	}
	subject := strings.TrimSpace(r.PathValue("subject"))
	if subject == "" {
		httpapi.Error(w, http.StatusBadRequest, "data subject is required")
		return
	}
	var (
		had    bool
		opErr  error
		hadErr error
	)
	s.do(func() {
		had, hadErr = s.vault.HasDataKey(subject)
		if hadErr != nil {
			return
		}
		opErr = s.vault.Erase(subject)
	})
	switch {
	case hadErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "erase data subject: "+hadErr.Error())
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "erase data subject: "+opErr.Error())
	default:
		httpapi.JSON(w, http.StatusOK, map[string]any{"subject": subject, "erased": had})
	}
}
