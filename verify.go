package filekind

import (
	"context"
	"errors"
	"fmt"
)

// ErrNotIntact: PRAGMA quick_check did not answer the single row "ok".
var ErrNotIntact = errors.New("filekind: quick_check found the file damaged")

// Verify checks a file from elsewhere that the caller has already opened
// read-only, its size bounded first and query_only and trusted_schema=OFF set
// where its engine supports them (SPEC.md, "Verifying a file from
// elsewhere"): PRAGMA quick_check must answer the one row "ok", and then the
// header must satisfy want, as Check judges it. A damaged file is refused
// before its header is trusted. The error names how many rows quick_check
// returned, never their text, which can quote the file. An engine that
// refuses to read a damaged page returns its own error instead.
func Verify(ctx context.Context, q Querier, want Want) (Header, error) {
	if err := want.valid(); err != nil {
		return Header{}, err
	}
	rows, err := q.QueryContext(ctx, "PRAGMA quick_check")
	if err != nil {
		return Header{}, err
	}
	defer closeOnEarlyReturn(rows)
	var answers []string
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			return Header{}, err
		}
		answers = append(answers, asString(v))
	}
	if err := rows.Err(); err != nil {
		return Header{}, err
	}
	if err := rows.Close(); err != nil {
		return Header{}, err
	}
	if len(answers) != 1 || answers[0] != "ok" {
		return Header{}, fmt.Errorf("%w: quick_check returned %d rows", ErrNotIntact, len(answers))
	}
	return Check(ctx, q, want)
}
