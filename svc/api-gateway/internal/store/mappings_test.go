package store

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestUniqueViolation(t *testing.T) {
	t.Parallel()
	if !uniqueViolation(&pgconn.PgError{Code: "23505"}) {
		t.Fatal("23505 was not recognized")
	}
	if uniqueViolation(&pgconn.PgError{Code: "23503"}) {
		t.Fatal("non-unique error was recognized")
	}
	if uniqueViolation(errors.New("duplicate")) {
		t.Fatal("untyped error was recognized")
	}
}
