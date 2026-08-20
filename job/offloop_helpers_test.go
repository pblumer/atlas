package job_test

import (
	"errors"

	"github.com/pblumer/atlas/model"
)

var errBoom = errors.New("boom")

// fakeReader stands in for a read view; the tests only need identity.
type fakeReader struct{}

func (*fakeReader) GetElementInstance(uint64) (*model.ElementInstanceValue, bool, error) {
	return nil, false, nil
}

func (*fakeReader) VariablesOfScope(uint64, func(*model.VariableValue) error) error { return nil }
