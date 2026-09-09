package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrAdminQuery = errors.New("invalid database query")

type AdminColumn struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	NotNull    bool   `json:"notNull"`
	PrimaryKey bool   `json:"primaryKey"`
}

type AdminTable struct {
	Name    string        `json:"name"`
	SQL     string        `json:"sql"`
	Columns []AdminColumn `json:"columns"`
	Count   int64         `json:"count"`
}

type AdminQuery struct {
	Query, Column, Value, Sort, Direction, From, To string
	CommentsOnly                                    bool
	Limit, Offset                                   int
}

type AdminPage struct {
	Rows   []map[string]any `json:"rows"`
	Total  int64            `json:"total"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
}

// Identifiers come only from SQLite's schema and are quoted even after validation.
func adminIdentifier(value string) string { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }

func (db *DB) adminTable(ctx context.Context, name string) (AdminTable, error) {
	table := AdminTable{Name: name, Columns: []AdminColumn{}}
	err := db.sql.QueryRowContext(ctx, `SELECT COALESCE(sql, '') FROM sqlite_schema WHERE type = 'table' AND name = ?`, name).Scan(&table.SQL)
	if errors.Is(err, sql.ErrNoRows) {
		return table, ErrNotFound
	}
	if err != nil {
		return table, err
	}
	rows, err := db.sql.QueryContext(ctx, `SELECT name, type, "notnull", pk FROM pragma_table_info(?)`, name)
	if err != nil {
		return table, err
	}
	defer rows.Close()
	for rows.Next() {
		var column AdminColumn
		var required, primary int
		if err := rows.Scan(&column.Name, &column.Type, &required, &primary); err != nil {
			return table, err
		}
		column.NotNull, column.PrimaryKey = required != 0, primary != 0
		table.Columns = append(table.Columns, column)
	}
	return table, rows.Err()
}

func (db *DB) AdminTables(ctx context.Context) ([]AdminTable, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT name FROM sqlite_schema WHERE type = 'table' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		names = append(names, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	tables := []AdminTable{}
	for _, name := range names {
		table, err := db.adminTable(ctx, name)
		if err != nil {
			return nil, err
		}
		if err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+adminIdentifier(name)).Scan(&table.Count); err != nil {
			return nil, err
		}
		tables = append(tables, table)
	}
	return tables, nil
}

func (db *DB) AdminRows(ctx context.Context, name string, query AdminQuery) (AdminPage, error) {
	page := AdminPage{Rows: []map[string]any{}, Limit: query.Limit, Offset: query.Offset}
	if query.Limit < 1 || query.Limit > 100 || query.Offset < 0 || len(query.Query) > 500 || len(query.Value) > 500 {
		return page, ErrAdminQuery
	}
	table, err := db.adminTable(ctx, name)
	if err != nil {
		return page, err
	}
	columns := map[string]bool{}
	primary := ""
	for _, column := range table.Columns {
		columns[column.Name] = true
		if column.PrimaryKey {
			primary = column.Name
		}
	}
	if query.Sort == "" {
		query.Sort = primary
		if query.Sort == "" {
			query.Sort = table.Columns[0].Name
		}
	}
	if !columns[query.Sort] || (query.Column != "" && !columns[query.Column]) {
		return page, ErrAdminQuery
	}
	if query.Direction == "" {
		query.Direction = "desc"
	}
	if query.Direction != "asc" && query.Direction != "desc" {
		return page, ErrAdminQuery
	}
	where := []string{"1=1"}
	args := []any{}
	if query.Query != "" {
		parts := []string{}
		for _, column := range table.Columns {
			parts = append(parts, `instr(lower(CAST(t.`+adminIdentifier(column.Name)+` AS TEXT)), lower(?)) > 0`)
			args = append(args, query.Query)
		}
		if columns["vocabulary_item_id"] {
			parts = append(parts, `EXISTS (SELECT 1 FROM vocabulary_items v WHERE v.id = t.vocabulary_item_id AND instr(lower(v.term), lower(?)) > 0)`)
			args = append(args, query.Query)
		}
		where = append(where, "("+strings.Join(parts, " OR ")+")")
	}
	if query.Column != "" {
		where = append(where, "t."+adminIdentifier(query.Column)+" = ?")
		args = append(args, query.Value)
	}
	if query.CommentsOnly {
		if !columns["comment"] {
			return page, ErrAdminQuery
		}
		where = append(where, "trim(t.comment) <> ''")
	}
	dateColumn := ""
	for _, candidate := range []string{"reviewed_at", "shown_at", "created_at", "fetched_at", "applied_at"} {
		if columns[candidate] {
			dateColumn = candidate
			break
		}
	}
	for i, value := range []string{query.From, query.To} {
		if value == "" {
			continue
		}
		date, err := time.Parse("2006-01-02", value)
		if err != nil || dateColumn == "" {
			return page, ErrAdminQuery
		}
		operator := ">="
		if i == 1 {
			operator = "<"
			date = date.AddDate(0, 0, 1)
		}
		where = append(where, "julianday(t."+adminIdentifier(dateColumn)+") "+operator+" julianday(?)")
		args = append(args, TimeString(date))
	}
	from := " FROM " + adminIdentifier(name) + " t WHERE " + strings.Join(where, " AND ")
	// Keep the count and page in one snapshot while MCP writes continue.
	tx, err := db.sql.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*)"+from, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	projection := "t.*"
	if columns["vocabulary_item_id"] {
		projection += ", (SELECT v.term FROM vocabulary_items v WHERE v.id = t.vocabulary_item_id) AS _term"
	}
	sortExpression := "t." + adminIdentifier(query.Sort)
	if strings.HasSuffix(query.Sort, "_at") {
		// Match the existing chronological indexes for variable-precision RFC3339 timestamps.
		sortExpression = "rtrim(" + sortExpression + ", 'Z')"
	}
	order := " ORDER BY " + sortExpression + " " + query.Direction
	if primary != "" && primary != query.Sort {
		order += ", t." + adminIdentifier(primary) + " " + query.Direction
	}
	rows, err := tx.QueryContext(ctx, "SELECT "+projection+from+order+" LIMIT ? OFFSET ?", append(args, query.Limit, query.Offset)...)
	if err != nil {
		return page, err
	}
	page.Rows, err = adminScan(rows)
	if err != nil {
		return page, err
	}
	return page, tx.Commit()
}

func adminScan(rows *sql.Rows) ([]map[string]any, error) {
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	result := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		row := map[string]any{}
		for i, column := range columns {
			if bytes, ok := values[i].([]byte); ok {
				row[column] = string(bytes)
			} else {
				row[column] = values[i]
			}
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (db *DB) AdminAnalytics(ctx context.Context, owner string) (map[string]any, error) {
	result := map[string]any{"owner": owner}
	queries := []struct {
		name, sql string
		args      []any
	}{
		{"statuses", `SELECT learning_status AS label, COUNT(*) AS count FROM vocabulary_items WHERE owner_key = ? GROUP BY learning_status`, []any{owner}},
		{"ratings", `SELECT rating AS label, COUNT(*) AS count FROM review_attempts WHERE owner_key = ? GROUP BY rating`, []any{owner}},
		{"effectiveRatings", `SELECT COALESCE(effective_rating, rating) AS label, COUNT(*) AS count FROM review_attempts WHERE owner_key = ? GROUP BY COALESCE(effective_rating, rating)`, []any{owner}},
		{"activity", `SELECT substr(reviewed_at, 1, 10) AS day, COUNT(*) AS reviews, SUM(CASE WHEN rating IN ('good', 'easy') THEN 1 ELSE 0 END) AS recalled FROM review_attempts WHERE owner_key = ? AND julianday(reviewed_at) >= julianday('now', 'start of day', '-29 days') GROUP BY day ORDER BY day`, []any{owner}},
		{"due", `SELECT COUNT(*) AS count FROM learning_cards c JOIN vocabulary_items v ON v.id = c.vocabulary_item_id WHERE v.owner_key = ? AND v.learning_status <> 'archived' AND julianday(c.due_at) <= julianday('now')`, []any{owner}},
		{"comments", `SELECT COUNT(*) AS count FROM review_attempts WHERE owner_key = ? AND trim(comment) <> ''`, []any{owner}},
		{"difficult", `SELECT v.id, v.term, c.lapses, c.consecutive_failures, c.difficulty, c.due_at FROM learning_cards c JOIN vocabulary_items v ON v.id = c.vocabulary_item_id WHERE v.owner_key = ? AND v.learning_status <> 'archived' ORDER BY c.lapses DESC, c.consecutive_failures DESC, c.difficulty DESC, v.id LIMIT 10`, []any{owner}},
	}
	tx, err := db.sql.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, query := range queries {
		rows, err := tx.QueryContext(ctx, query.sql, query.args...)
		if err != nil {
			return nil, fmt.Errorf("admin analytics: %w", err)
		}
		data, err := adminScan(rows)
		if err != nil {
			return nil, err
		}
		result[query.name] = data
	}
	return result, tx.Commit()
}
