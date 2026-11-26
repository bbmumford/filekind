package filekind

import (
	"context"
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
