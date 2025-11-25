package storage

import "testing"

func TestIsUniqueViolation(t *testing.T) {
	if !isUniqueViolation(assertError("duplicate key value violates unique constraint")) {
		t.Fatal("expected duplicate to be unique violation")
	}
	if isUniqueViolation(assertError("some other error")) {
		t.Fatal("did not expect unique violation")
	}
}

type assertError string

func (e assertError) Error() string { return string(e) }
