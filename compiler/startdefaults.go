package compiler

// Start-variable defaults (ADR-0455).
//
// A declared start variable's default used to be what the start surfaces prefilled and
// nothing more: an instance started by the API, MCP, a timer, a message, a signal, a call
// activity or a CSV upload without the variable simply did not have it, and a script read
// null. The default is now what every instance starts with when its start did not supply
// the variable, on every path that starts one. It is parsed here, once, into the form any
// other variable is stored in, so the engine seeds it without parsing (I5).
//
// Nothing here refuses a model. The declaration has always been accepted as written, and a
// stored definition is compiled again on every restart: a refusal would turn a model that
// ran yesterday into one that no longer loads. A default the engine cannot seed is
// withheld instead, with the reason the deploy warns with.

// startVariableDefault reads a declaration's default into its seeded form. An empty
// default is no default — the attribute cannot tell "absent" from "empty", and the start
// surfaces treat an empty field as one not filled in. A default that is not a literal of
// the declared type is withheld, with the reason.
func startVariableDefault(sv *StartVariable) {
	if sv.Default == "" {
		return
	}
	kind, b, text, err := ParseConstantValue(sv.Type, sv.Default)
	if err != nil {
		sv.DefaultProblem = err.Error()
		return
	}
	sv.HasDefault, sv.Kind, sv.Bool, sv.Text = true, kind, b, text
}

// withholdPersonalDefaults takes the default from a start variable that holds personal
// data or names the data subject (ADR-0314). A personal value must arrive enciphered under
// its subject's key, and a default stated in the model is in the clear in every copy of it:
// the engine would refuse the write and park the instance it was starting. And a default
// subject would name the same person for every instance started without one, so one
// erasure would destroy the data of all of them. The start form still prefills it — a
// person typing over it is a value that is enciphered on its way in like any other.
func (p *CompiledProcess) withholdPersonalDefaults() {
	if len(p.personalVars) == 0 && p.dataSubjectVar == "" {
		return
	}
	for i := range p.startVars {
		sv := &p.startVars[i]
		if !sv.HasDefault {
			continue
		}
		switch {
		case p.IsPersonal(sv.Name):
			sv.HasDefault = false
			sv.DefaultProblem = "it is declared personal data, which is never seeded from the model in the clear (ADR-0314)"
		case sv.Name == p.dataSubjectVar:
			sv.HasDefault = false
			sv.DefaultProblem = "it is the data subject, and a default would make every instance started without one the same person's (ADR-0314)"
		}
	}
}
