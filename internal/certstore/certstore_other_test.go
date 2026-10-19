//go:build !windows

package certstore

import (
	"errors"
	"testing"
)

// Outside Windows there is no certificate store to sign with. The refusal says so and
// says what to do instead, rather than reporting the certificate as missing.
func TestOpenOutsideWindowsSaysThereIsNoStore(t *testing.T) {
	if Supported {
		t.Fatal("Supported is true outside Windows")
	}
	_, _, err := Open("02CB6038F2E91693AE8D31C983569366034B2100")
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("err = %v, want ErrUnsupported", err)
	}
}
