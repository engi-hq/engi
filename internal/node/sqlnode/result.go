package sqlnode

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// maxExactFloat is the largest integer a float64 holds exactly (2^53).
const maxExactFloat = 1 << 53

// ResultOptions control how column values become item JSON.
type ResultOptions struct {
	// NumbersAsString returns BIGINT and NUMERIC/DECIMAL columns as strings so
	// no precision is lost. Otherwise bigints are integers and decimals floats.
	NumbersAsString bool
}

// Querier is what *sql.DB and *sql.Tx share.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// coerceArg prepares an expression value for binding: whole floats become
// integers (expression numbers are float64) and containers become JSON text.
func coerceArg(v any) any {
	switch t := v.(type) {
	case float64:
		if t == math.Trunc(t) && math.Abs(t) < maxExactFloat {
			return int64(t)
		}
	case map[string]any, []any:
		if b, err := json.Marshal(t); err == nil {
			return string(b)
		}
	}
	return v
}

var (
	leadingNoise = regexp.MustCompile(`^(?:\s+|--[^\n]*\n?|/\*.*?\*/|\()+`)
	returningRe  = regexp.MustCompile(`(?i)\breturning\b`)
)

// returnsRows guesses whether user SQL yields a result set.
func returnsRows(query string) bool {
	q := leadingNoise.ReplaceAllString(query, "")
	word, _, _ := strings.Cut(strings.ToLower(q), " ")
	word = strings.TrimFunc(word, func(r rune) bool { return r == ';' || r == '\n' || r == '\t' || r == '\r' })
	switch word {
	case "select", "with", "show", "explain", "values", "table", "describe", "desc":
		return true
	}
	return returningRe.MatchString(query)
}

// Run executes st on q and returns one JSON object per row, or a single
// {affectedRows} object for statements that return no rows.
func Run(ctx context.Context, q Querier, st Statement, opts ResultOptions) ([]map[string]any, error) {
	if !st.ReturnsRows {
		res, err := q.ExecContext(ctx, st.SQL, st.Args...)
		if err != nil {
			return nil, fmt.Errorf("executing statement: %w", err)
		}
		out := map[string]any{}
		if n, err := res.RowsAffected(); err == nil {
			out["affectedRows"] = n
		}
		if id, err := res.LastInsertId(); err == nil && id > 0 {
			out["lastInsertId"] = id
		}
		return []map[string]any{out}, nil
	}
	rows, err := q.QueryContext(ctx, st.SQL, st.Args...)
	if err != nil {
		return nil, fmt.Errorf("running query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.ColumnTypes()
	if err != nil {
		return nil, fmt.Errorf("reading columns: %w", err)
	}
	var out []map[string]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			row[c.Name()] = normalize(vals[i], strings.ToUpper(c.DatabaseTypeName()), opts)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading rows: %w", err)
	}
	return out, nil
}

func isBigint(typ string) bool { return strings.Contains(typ, "BIGINT") || typ == "INT8" }

func isDecimal(typ string) bool { return typ == "NUMERIC" || typ == "DECIMAL" || typ == "NEWDECIMAL" }

func isBinary(typ string) bool {
	switch typ {
	case "BYTEA", "BLOB", "TINYBLOB", "MEDIUMBLOB", "LONGBLOB", "BINARY", "VARBINARY", "BIT":
		return true
	}
	return false
}

// textTimeLayouts are the MySQL text-protocol date formats.
var textTimeLayouts = []string{"2006-01-02 15:04:05.999999", "2006-01-02"}

// normalize converts a driver value to a JSON-friendly one using the column's
// database type name.
func normalize(v any, typ string, o ResultOptions) any {
	switch t := v.(type) {
	case nil:
		return nil
	case time.Time:
		return t.UTC().Format(time.RFC3339Nano)
	case int64:
		if o.NumbersAsString && isBigint(typ) {
			return strconv.FormatInt(t, 10)
		}
		return t
	case uint64:
		if (o.NumbersAsString && isBigint(typ)) || t > math.MaxInt64 {
			return strconv.FormatUint(t, 10)
		}
		return int64(t)
	case string:
		return normalizeText(t, typ, o)
	case []byte:
		if isBinary(typ) {
			return base64.StdEncoding.EncodeToString(t)
		}
		return normalizeText(string(t), typ, o)
	}
	return v
}

func normalizeText(s, typ string, o ResultOptions) any {
	switch {
	case typ == "JSON" || typ == "JSONB":
		var out any
		if err := json.Unmarshal([]byte(s), &out); err == nil {
			return out
		}
	case isDecimal(typ):
		if !o.NumbersAsString {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return f
			}
		}
	case strings.Contains(typ, "INT") || typ == "YEAR":
		if strings.HasPrefix(typ, "UNSIGNED") {
			if u, err := strconv.ParseUint(s, 10, 64); err == nil {
				return normalize(u, typ, o)
			}
		} else if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return normalize(n, typ, o)
		}
	case typ == "FLOAT" || typ == "DOUBLE" || typ == "REAL" || strings.HasPrefix(typ, "FLOAT"):
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	case typ == "DATETIME" || typ == "TIMESTAMP" || typ == "DATE":
		for _, l := range textTimeLayouts {
			if ts, err := time.Parse(l, s); err == nil {
				return ts.UTC().Format(time.RFC3339Nano)
			}
		}
	}
	return s
}
