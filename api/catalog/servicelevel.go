package catalog

import "strings"

// ServiceLevel is what a product promises about its delivery
// (ADR-0515): a name the shop shows — "Standard",
// "Express" — and how many working days may pass between the approval of a position
// and the start of the right it provisions.
//
// # Why it is frozen like the price
//
// A delivery is measured against a promise, and the promise that counts is the one
// the orderer was given. A product manager who shortens the delivery time next month
// must not make last month's deliveries late after the fact. So it travels into the
// release and onto the order line, as the price does (ADR-0361).
//
// # What it deliberately is not
//
// An availability or an incident-response target. Atlas measures how long fulfilment
// took, because it runs fulfilment; it does not see whether a service stayed up, and
// a promise it cannot measure would be reported as kept by default.
type ServiceLevel struct {
	// Name is the key the shop and the reports show where no wording for the reader's
	// language exists.
	Name string `json:"name"`
	// NameTexts is the name per language tag, read like the category headings
	// (ADR-0412). Missing languages fall back to Name.
	NameTexts map[string]string `json:"nameTexts,omitempty"`
	// DeliveryDays is the promise: at most this many working days, Monday to Friday,
	// from the later of the order and its approval until the right begins.
	DeliveryDays int `json:"deliveryDays"`
}

// maxDeliveryDays bounds the promise at a working year. Anything longer is not a
// delivery time but a typo, and a report measuring against it would call every late
// delivery on time.
const maxDeliveryDays = 365

// Copy answers a copy that shares nothing with s, or nil for nil.
func (s *ServiceLevel) Copy() *ServiceLevel {
	if s == nil {
		return nil
	}
	out := *s
	out.NameTexts = copyTexts(s.NameTexts)
	return &out
}

// NameIn answers the name a reader of lang reads: the wording for that language
// where one is written, the key otherwise, and nothing for no service level.
func (s *ServiceLevel) NameIn(lang string) string {
	if s == nil {
		return ""
	}
	if t := strings.TrimSpace(s.NameTexts[lang]); t != "" {
		return t
	}
	return s.Name
}

// serviceLevelProblems names what is wrong with a product's promise, if anything.
func serviceLevelProblems(it Item) []Problem {
	sl := it.ServiceLevel
	if sl == nil {
		return nil
	}
	var out []Problem
	if strings.TrimSpace(sl.Name) == "" {
		out = append(out, Problem{Item: it.ID, Message: "names a service level without a name; " +
			"the shop and every report would show a promise nobody can refer to"})
	}
	switch {
	case sl.DeliveryDays < 1:
		out = append(out, Problem{Item: it.ID, Message: "names a service level without a " +
			"delivery time of at least one working day; leave the service level out for a " +
			"product that promises none"})
	case sl.DeliveryDays > maxDeliveryDays:
		out = append(out, Problem{Item: it.ID, Message: "names a delivery time longer than a " +
			"year; a service level promises at most 365 working days"})
	}
	return out
}
