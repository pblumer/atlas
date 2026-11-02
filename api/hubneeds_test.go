package api

import (
	"os"
	"strings"
	"testing"
)

// Two processes may name one worker as two Worker Types; both types are needed.
func TestProcessNeedsKeepsEveryWorkerType(t *testing.T) {
	read := func(name string) []byte {
		raw, err := os.ReadFile("../examples/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	types, _, unread := ProcessNeeds(map[string][]byte{
		"a": read("exchange-austritt.bpmn"),
		"b": read("entra-delta-sync.bpmn"),
	})
	if len(unread) != 0 {
		t.Fatalf("unread: %v", unread)
	}
	if got := strings.Join(types, ","); !strings.Contains(got, "entra") || !strings.Contains(got, "exchange") {
		t.Fatalf("worker types = %q, want entra and exchange", got)
	}
	if PortableApplicationKey("Beispiele: Identität & Verzeichnis") != "beispiele-identitaet-verzeichnis" {
		t.Fatalf("key = %q", PortableApplicationKey("Beispiele: Identität & Verzeichnis"))
	}
}
