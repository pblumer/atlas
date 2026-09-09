package state

import (
	"strings"

	"github.com/pblumer/atlas/model"
)

// A collection under construction is a list variable whose elements are held one key
// per element rather than inside the variable's own record
// (ADR-draft-a-collection-under-construction).
//
// A multi-instance activity writes one result per iteration into one list. Recording
// the element rather than the whole list kept the *log* linear (ADR-0294 and the
// change that followed it), but the fold still put the assembled collection back
// under a single key each round, so the store went on absorbing bytes that grew with
// the square of the iteration count. Holding the elements apart is what makes a round
// cost one element there too.
//
// The form is private to the storage layer. A record whose Parts is positive is a
// stub: it says how long the list is and nothing about what is in it. Every path that
// hands a variable to a caller assembles it first, so no reader — FEEL, the API, a
// connector, a snapshot — ever meets a stub.

// assembleCollection turns a stub into the list it stands for, reading the scope's
// elements and joining their canonical JSON. A record that is not a stub is left
// exactly as it was read.
//
// Joining the elements' own JSON is the whole trick, and it is exact rather than
// approximate: encoding/json renders a list as its elements separated by commas
// inside brackets, and escapes an element the same way whether it encodes it alone or
// in place. So the text assembled here is byte-for-byte the text a whole-list write
// would have stored — which is what lets the two forms be the same value, and what
// lets a collection change form without any reader noticing.
//
// A slot no iteration has written yet reads as null: the same null the collection was
// seeded with, and the same null a reader saw there before this change.
func assembleCollection(r iterReader, v *model.VariableValue) error {
	if v.Parts <= 0 {
		return nil
	}
	frags := make([]string, v.Parts)
	for i := range frags {
		frags[i] = "null"
	}
	prefix := variableElementPrefix(v.ScopeKey, v.Name)
	if err := scanRangeWith(r, prefix, prefixEnd(prefix), func(k, raw []byte) error {
		// An index outside the list is not this stub's business. It cannot arise from a
		// loop — the collection is seeded before any iteration fills it — but the scan
		// is over stored bytes, and dropping what does not fit is the reading that
		// cannot produce a list of the wrong length.
		if i := elementIndexFromKey(k); i >= 0 && i < v.Parts {
			frags[i] = string(raw)
		}
		return nil
	}); err != nil {
		return err
	}
	v.Kind, v.Bool, v.Text, v.Parts = model.VarJSON, false, "["+strings.Join(frags, ",")+"]", 0
	return nil
}
