package filekind

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// Verify reads query_only and trusted_schema before it reads the file and
// refuses with ErrUnsafeOpen a connection that can write the file or would run
// the code the file's own schema carries; with both settings safe it passes.
func TestVerifyRefusesAConnectionThatCanWriteOrTrustsTheSchema(t *testing.T) {
	ctx := context.Background()
	_, path := fixture(t, "rows.db")
	for name, dsn := range map[string]string{
		"query_only off":    "file:" + path + "?_pragma=trusted_schema(0)",
		"trusted_schema on": "file:" + path + "?mode=ro&_pragma=query_only(1)&_pragma=trusted_schema(1)",
	} {
		if _, err := Verify(ctx, openFile(t, dsn), Want{Kind: Fixture, Newest: 1}); !errors.Is(err, ErrUnsafeOpen) {
			t.Errorf("Verify with %s = %v, want ErrUnsafeOpen", name, err)
		}
	}
	if _, err := Verify(ctx, openForVerify(t, path), Want{Kind: Fixture, Newest: 1}); err != nil {
		t.Fatalf("Verify on a safe connection = %v", err)
	}
}

// unreported answers one pragma with no row, as an engine without that setting
// does, and passes every other statement to the connection it wraps.
type unreported struct {
	Querier
	pragma string
}

func (u unreported) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if query == "PRAGMA "+u.pragma {
		return u.Querier.QueryContext(ctx, "SELECT 1 WHERE 0")
	}
	return u.Querier.QueryContext(ctx, query, args...)
}

// Verify refuses with ErrUnsafeOpen a connection that answers query_only or
// trusted_schema with no row, though every other setting is safe: an engine
// that does not report a setting cannot be shown to hold it.
func TestVerifyRefusesAConnectionThatDoesNotReportASetting(t *testing.T) {
	ctx := context.Background()
	_, path := fixture(t, "rows.db")
	db := openForVerify(t, path)
	for _, pragma := range []string{"query_only", "trusted_schema"} {
		if _, err := Verify(ctx, unreported{db, pragma}, Want{Kind: Fixture, Newest: 1}); !errors.Is(err, ErrUnsafeOpen) {
			t.Errorf("Verify with no %s row = %v, want ErrUnsafeOpen", pragma, err)
		}
	}
	if _, err := Verify(ctx, unreported{db, "journal_mode"}, Want{Kind: Fixture, Newest: 1}); err != nil {
		t.Fatalf("Verify with both settings reported = %v", err)
	}
}
