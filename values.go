package filekind

import (
	"context"
	"fmt"
	"strings"
)

// CheckValues refuses a file of a Base3 kind that holds an INTEGER or REAL
// value in any column of any of its tables, naming the first table and column
// it finds. A rowid alias (a rowid table's single INTEGER PRIMARY KEY column)
// is the engine's own row number and passes. Kinds whose values are not Base3
// pass without a read.
func CheckValues(ctx context.Context, q Querier, kind Kind) error {
	entry, ok := Lookup(kind)
	if !ok {
		return fmt.Errorf("%w: kind 0x%02x is not registered", ErrInvalidArgument, uint8(kind))
	}
	if entry.Values != Base3 {
		return nil
	}
	tables, err := userTables(ctx, q)
	if err != nil {
		return err
	}
	for _, t := range tables {
		columns, err := tableColumns(ctx, q, t)
		if err != nil {
			return err
		}
		for _, c := range columns {
			if c.rowidAlias {
				continue
			}
			hit, err := holdsBinaryNumber(ctx, q, t.name, c.name)
			if err != nil {
				return err
			}
			if hit {
				return fmt.Errorf("%w: %s.%s", ErrBinaryNumber, t.name, c.name)
			}
		}
	}
	return nil
}

type table struct {
	name         string
	withoutRowid bool
}

type column struct {
	name       string
	rowidAlias bool
}

// userTables lists the file's ordinary tables, leaving out the engine's own
// sqlite_ tables and virtual tables.
func userTables(ctx context.Context, q Querier) ([]table, error) {
	rows, err := q.QueryContext(ctx, "SELECT name, sql FROM sqlite_schema WHERE type = 'table' ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer closeOnEarlyReturn(rows)
	var out []table
	for rows.Next() {
		var name, text any
		if err := rows.Scan(&name, &text); err != nil {
			return nil, err
		}
		n := asString(name)
		sql := strings.ToUpper(strings.Join(strings.Fields(asString(text)), " "))
		if strings.HasPrefix(strings.ToLower(n), "sqlite_") || strings.HasPrefix(sql, "CREATE VIRTUAL") {
			continue
		}
		out = append(out, table{name: n, withoutRowid: strings.Contains(sql, "WITHOUT ROWID")})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, rows.Close()
}

// tableColumns reads PRAGMA table_info. A column is a rowid alias when it is
// the table's only primary-key column, declared exactly INTEGER, in a rowid
// table.
func tableColumns(ctx context.Context, q Querier, t table) ([]column, error) {
	rows, err := q.QueryContext(ctx, "PRAGMA table_info("+quoteName(t.name)+")")
	if err != nil {
		return nil, err
	}
	defer closeOnEarlyReturn(rows)
	type info struct {
		name, declared string
		pk             int64
	}
	var infos []info
	keys := 0
	for rows.Next() {
		var cid, name, declared, notnull, dflt, pk any
		if err := rows.Scan(&cid, &name, &declared, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		p, _ := pk.(int64)
		if p > 0 {
			keys++
		}
		infos = append(infos, info{name: asString(name), declared: asString(declared), pk: p})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]column, 0, len(infos))
	for _, i := range infos {
		alias := !t.withoutRowid && keys == 1 && i.pk == 1 && strings.EqualFold(strings.TrimSpace(i.declared), "INTEGER")
		out = append(out, column{name: i.name, rowidAlias: alias})
	}
	return out, rows.Close()
}

func holdsBinaryNumber(ctx context.Context, q Querier, tableName, columnName string) (bool, error) {
	c := quoteName(columnName)
	rows, err := q.QueryContext(ctx, "SELECT 1 FROM "+quoteName(tableName)+
		" WHERE typeof("+c+") IN ('integer', 'real') LIMIT 1")
	if err != nil {
		return false, err
	}
	defer closeOnEarlyReturn(rows)
	hit := rows.Next()
	if err := rows.Err(); err != nil {
		return false, err
	}
	return hit, rows.Close()
}
