package catalog

import (
	"strings"
	"testing"
)

// TestImportNamesWhatTheDocumentIsInstead: a document whose root is not <model> is
// refused by naming its root, and a document that is not well-formed by saying so —
// "nothing imported" would send somebody looking for a bug in their model.
func TestImportNamesWhatTheDocumentIsInstead(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{`<?xml version="1.0"?><definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"/>`,
			"its root is <definitions>, not <model>"},
		{`<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/"><elements></model>`,
			"catalog: invalid XML"},
	} {
		if _, err := ImportArchiMate([]byte(tc.doc), "cat_1"); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ImportArchiMate = %v, want an error containing %q", err, tc.want)
		}
	}
}
