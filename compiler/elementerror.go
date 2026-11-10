package compiler

import "fmt"

// ElementError is a compile error anchored to one BPMN element: the element-local
// faults stages 1–4 raise while the process is still being built (a duplicate id, an
// unknown reference, a condition that does not parse). Its message is exactly the
// wrapped error's, so a deploy's response and every caller reading the text are
// unchanged; what it adds is the element, which [ValidateModel] reports as the
// Problem's Element so the Problems panel can select it (ADR-0547).
//
// It is a diagnostic only. It does not change which models compile, and an element-
// local fault stays fatal on reload as before, since nothing was built to bring back
// (ADR-0177).
type ElementError struct {
	Element string // the BPMN id of the element the fault belongs to
	Err     error
}

func (e *ElementError) Error() string { return e.Err.Error() }

func (e *ElementError) Unwrap() error { return e.Err }

// elementErrorf is fmt.Errorf anchored to element. The format and arguments are
// fmt.Errorf's, %w included; element is usually also one of the arguments, since the
// message names it.
func elementErrorf(element, format string, args ...any) error {
	return &ElementError{Element: element, Err: fmt.Errorf(format, args...)}
}
