package filekind

import (
	"context"
	"testing"
)

// A row set naming no schema reads the main database's table even when the
// connection holds a TEMP table of the same name, which an unqualified name
// would resolve to first: the digest is of the rows the base-table check
// judged.
func TestATempTableDoesNotShadowTheMainTable(t *testing.T) {
	ctx := context.Background()
	db, _ := newFile(t)
	exec(t, db, "CREATE TABLE t (k INTEGER PRIMARY KEY, v TEXT)", "INSERT INTO t VALUES (1, 'main')")
	set := RowSet{Kind: Fixture, Set: "s", Table: "t", Columns: []string{"k", "v"}, Key: []string{"k"}}
	want, err := DigestRows(ctx, db, set)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	closeAtEnd(t, conn)
	if _, err := conn.ExecContext(ctx, "CREATE TEMP TABLE t (k INTEGER PRIMARY KEY, v TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "INSERT INTO temp.t VALUES (1, 'temp'), (2, 'temp')"); err != nil {
		t.Fatal(err)
	}
	got, err := DigestRows(ctx, conn, set)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("digest on a connection holding TEMP t = %s (%d rows), want the main table's %s (%d rows)",
			got.SHA256Hex(), got.Rows, want.SHA256Hex(), want.Rows)
	}
}
