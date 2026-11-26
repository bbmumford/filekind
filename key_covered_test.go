package filekind

import (
	"context"
	"errors"
	"testing"
)

// A row set whose key names a column outside its columns is refused with
// ErrKeyNotCovered before any row is read, by DigestRows and EncodeRows alike:
// the stream holds the columns' values only, so a signed digest of such a set
// would not bind the rows' keys. Rows renumbered with their order kept would
// digest the same.
func TestARowSetWhoseKeyIsOutsideItsColumnsIsRefused(t *testing.T) {
	ctx := context.Background()
	db, _ := newFile(t)
	exec(t, db, "CREATE TABLE t (k INTEGER PRIMARY KEY, v TEXT)", "INSERT INTO t VALUES (1, 'a')", "INSERT INTO t VALUES (2, 'b')")
	set := RowSet{Kind: Fixture, Set: "s", Table: "t", Columns: []string{"v"}, Key: []string{"k"}}
	if _, err := DigestRows(ctx, db, set); !errors.Is(err, ErrKeyNotCovered) {
		t.Fatalf("DigestRows of a set keyed outside its columns = %v, want ErrKeyNotCovered", err)
	}
	if _, err := EncodeRows(ctx, db, set, discard{}); !errors.Is(err, ErrKeyNotCovered) {
		t.Fatalf("EncodeRows of a set keyed outside its columns = %v, want ErrKeyNotCovered", err)
	}
	set.Columns = []string{"k", "v"}
	if _, err := DigestRows(ctx, db, set); err != nil {
		t.Fatalf("DigestRows of a set whose key is a column = %v", err)
	}
}

// discard is an io.Writer that keeps nothing.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
