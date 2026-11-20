// Package sqlnode holds what the Postgres and MySQL nodes share: a SQL
// statement builder (identifier quoting, placeholders, where conditions), result
// normalization and the node itself, parameterized by a Dialect.
//
// User data only ever travels as bound parameters. Identifiers (table and
// column names) are quoted, never interpolated raw.
package sqlnode

import (
	"errors"
	"fmt"
	"strings"
)

// Dialect captures the differences between the supported SQL flavors.
type Dialect struct {
	// Name is "postgres" or "mysql".
	Name string
	// quote is the identifier quote character.
	quote string
	// numbered selects $1..$n placeholders instead of ?.
	numbered bool
	// returning means INSERT/UPDATE support RETURNING *.
	returning bool
}

// The supported dialects.
var (
	Postgres = Dialect{Name: "postgres", quote: `"`, numbered: true, returning: true}
	MySQL    = Dialect{Name: "mysql", quote: "`"}
)

// Condition operators of a where clause.
const (
	OpEqual      = "equal"
	OpNotEqual   = "notEqual"
	OpGreater    = "greaterThan"
	OpGreaterEq  = "greaterThanOrEqual"
	OpLess       = "lessThan"
	OpLessEq     = "lessThanOrEqual"
	OpLike       = "like"
	OpNotLike    = "notLike"
	OpILike      = "ilike"
	OpIsNull     = "isNull"
	OpIsNotNull  = "isNotNull"
	OpIn         = "in"
	OpNotIn      = "notIn"
	CombineAnd   = "and"
	CombineOr    = "or"
	SortDesc     = "desc"
	noMatchSQL   = "1 = 0"
	alwaysSQL    = "1 = 1"
	maxIdentSize = 128
)

// Table names a table, optionally schema-qualified.
type Table struct{ Schema, Name string }

// Condition is one where predicate.
type Condition struct {
	Column   string
	Operator string
	Value    any
}

// Where is a list of conditions joined by Combinator (and/or; default and).
type Where struct {
	Combinator string
	Conditions []Condition
}

// Pair is a column and the value to write.
type Pair struct {
	Column string
	Value  any
}

// Sort orders a select.
type Sort struct {
	Column string
	Desc   bool
}

// Statement is a ready-to-run query.
type Statement struct {
	SQL  string
	Args []any
	// ReturnsRows tells whether to read rows (true) or only the affected count.
	ReturnsRows bool
}

// QuoteIdent quotes one identifier for the dialect.
func (d Dialect) QuoteIdent(name string) (string, error) {
	if name == "" {
		return "", errors.New("identifier must not be empty")
	}
	if len(name) > maxIdentSize {
		return "", fmt.Errorf("identifier longer than %d bytes", maxIdentSize)
	}
	if strings.ContainsRune(name, 0) {
		return "", errors.New("identifier must not contain NUL")
	}
	return d.quote + strings.ReplaceAll(name, d.quote, d.quote+d.quote) + d.quote, nil
}

func (d Dialect) table(t Table) (string, error) {
	name, err := d.QuoteIdent(t.Name)
	if err != nil {
		return "", fmt.Errorf("table: %w", err)
	}
	if t.Schema == "" {
		return name, nil
	}
	schema, err := d.QuoteIdent(t.Schema)
	if err != nil {
		return "", fmt.Errorf("schema: %w", err)
	}
	return schema + "." + name, nil
}

// builder accumulates SQL text and bound arguments in placeholder order.
type builder struct {
	d    Dialect
	sb   strings.Builder
	args []any
	err  error
}

func (b *builder) ident(name string) string {
	q, err := b.d.QuoteIdent(name)
	if err != nil && b.err == nil {
		b.err = fmt.Errorf("column %q: %w", name, err)
	}
	return q
}

// arg binds a value and returns its placeholder.
func (b *builder) arg(v any) string {
	b.args = append(b.args, coerceArg(v))
	if b.d.numbered {
		return fmt.Sprintf("$%d", len(b.args))
	}
	return "?"
}

func (b *builder) write(s string) { b.sb.WriteString(s) }

func (b *builder) finish(returnsRows bool) (Statement, error) {
	if b.err != nil {
		return Statement{}, b.err
	}
	return Statement{SQL: b.sb.String(), Args: b.args, ReturnsRows: returnsRows}, nil
}

var comparison = map[string]string{
	OpEqual: "=", OpNotEqual: "<>", OpGreater: ">", OpGreaterEq: ">=", OpLess: "<", OpLessEq: "<=",
	OpLike: "LIKE", OpNotLike: "NOT LIKE",
}

// where writes " WHERE ..." for a non-empty clause.
func (b *builder) where(w Where) error {
	if len(w.Conditions) == 0 {
		return nil
	}
	join := " AND "
	switch w.Combinator {
	case "", CombineAnd:
	case CombineOr:
		join = " OR "
	default:
		return fmt.Errorf("unknown combinator %q", w.Combinator)
	}
	parts := make([]string, 0, len(w.Conditions))
	for i, c := range w.Conditions {
		p, err := b.condition(c)
		if err != nil {
			return fmt.Errorf("condition %d: %w", i, err)
		}
		parts = append(parts, p)
	}
	b.write(" WHERE " + strings.Join(parts, join))
	return nil
}

func (b *builder) condition(c Condition) (string, error) {
	col := b.ident(c.Column)
	switch c.Operator {
	case OpIsNull:
		return col + " IS NULL", nil
	case OpIsNotNull:
		return col + " IS NOT NULL", nil
	case OpEqual:
		if c.Value == nil {
			return col + " IS NULL", nil
		}
	case OpNotEqual:
		if c.Value == nil {
			return col + " IS NOT NULL", nil
		}
	case OpILike:
		if b.d.numbered {
			return col + " ILIKE " + b.arg(c.Value), nil
		}
		return col + " LIKE " + b.arg(c.Value), nil
	case OpIn, OpNotIn:
		list := listOf(c.Value)
		if len(list) == 0 {
			if c.Operator == OpIn {
				return noMatchSQL, nil
			}
			return alwaysSQL, nil
		}
		ph := make([]string, len(list))
		for i, v := range list {
			ph[i] = b.arg(v)
		}
		kw := " IN ("
		if c.Operator == OpNotIn {
			kw = " NOT IN ("
		}
		return col + kw + strings.Join(ph, ", ") + ")", nil
	}
	op, ok := comparison[c.Operator]
	if !ok {
		return "", fmt.Errorf("unknown operator %q", c.Operator)
	}
	return col + " " + op + " " + b.arg(c.Value), nil
}

// listOf accepts an array or a comma-separated string.
func listOf(v any) []any {
	switch t := v.(type) {
	case nil:
		return nil
	case []any:
		return t
	case string:
		if strings.TrimSpace(t) == "" {
			return nil
		}
		parts := strings.Split(t, ",")
		out := make([]any, len(parts))
		for i, p := range parts {
			out[i] = strings.TrimSpace(p)
		}
		return out
	}
	return []any{v}
}

// Select builds SELECT columns FROM table [WHERE] [ORDER BY] [LIMIT]. Empty
// columns select everything; limit <= 0 means no limit.
func (d Dialect) Select(t Table, columns []string, w Where, s Sort, limit int) (Statement, error) {
	tbl, err := d.table(t)
	if err != nil {
		return Statement{}, err
	}
	b := &builder{d: d}
	cols := "*"
	if len(columns) > 0 {
		q := make([]string, len(columns))
		for i, c := range columns {
			q[i] = b.ident(c)
		}
		cols = strings.Join(q, ", ")
	}
	b.write("SELECT " + cols + " FROM " + tbl)
	if err := b.where(w); err != nil {
		return Statement{}, err
	}
	if s.Column != "" {
		dir := "ASC"
		if s.Desc {
			dir = "DESC"
		}
		b.write(" ORDER BY " + b.ident(s.Column) + " " + dir)
	}
	if limit > 0 {
		b.write(fmt.Sprintf(" LIMIT %d", limit))
	}
	return b.finish(true)
}

// Insert builds an INSERT of one row.
func (d Dialect) Insert(t Table, row []Pair) (Statement, error) {
	b, err := d.insertPrefix(t, row)
	if err != nil {
		return Statement{}, err
	}
	return b.finishWrite()
}

// Upsert builds an insert that updates the non-match columns when a row with
// the same match columns exists. Postgres needs a unique index on the match
// columns; MySQL uses whichever unique key collides.
func (d Dialect) Upsert(t Table, row []Pair, match []string) (Statement, error) {
	if len(match) == 0 {
		return Statement{}, errors.New("upsert needs at least one match column")
	}
	b, err := d.insertPrefix(t, row)
	if err != nil {
		return Statement{}, err
	}
	isMatch := map[string]bool{}
	for _, m := range match {
		isMatch[m] = true
	}
	var sets []string
	for _, p := range row {
		if isMatch[p.Column] {
			continue
		}
		c := b.ident(p.Column)
		if d.numbered {
			sets = append(sets, c+" = EXCLUDED."+c)
		} else {
			sets = append(sets, c+" = VALUES("+c+")")
		}
	}
	if d.numbered {
		cols := make([]string, len(match))
		for i, m := range match {
			cols[i] = b.ident(m)
		}
		b.write(" ON CONFLICT (" + strings.Join(cols, ", ") + ")")
		if len(sets) == 0 {
			b.write(" DO NOTHING")
		} else {
			b.write(" DO UPDATE SET " + strings.Join(sets, ", "))
		}
	} else {
		if len(sets) == 0 {
			first := b.ident(match[0])
			sets = []string{first + " = " + first}
		}
		b.write(" ON DUPLICATE KEY UPDATE " + strings.Join(sets, ", "))
	}
	return b.finishWrite()
}

func (d Dialect) insertPrefix(t Table, row []Pair) (*builder, error) {
	tbl, err := d.table(t)
	if err != nil {
		return nil, err
	}
	b := &builder{d: d}
	b.write("INSERT INTO " + tbl)
	if len(row) == 0 {
		if d.numbered {
			b.write(" DEFAULT VALUES")
		} else {
			b.write(" () VALUES ()")
		}
		return b, nil
	}
	cols := make([]string, len(row))
	phs := make([]string, len(row))
	for i, p := range row {
		cols[i] = b.ident(p.Column)
		phs[i] = b.arg(p.Value)
	}
	b.write(" (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(phs, ", ") + ")")
	return b, nil
}

// finishWrite appends RETURNING * where supported.
func (b *builder) finishWrite() (Statement, error) {
	if b.d.returning {
		b.write(" RETURNING *")
	}
	return b.finish(b.d.returning)
}

// Update builds UPDATE table SET set... WHERE match.... SET arguments come
// before WHERE arguments. A nil match value compares with IS NULL.
func (d Dialect) Update(t Table, set, match []Pair) (Statement, error) {
	if len(set) == 0 {
		return Statement{}, errors.New("update needs at least one column to set")
	}
	if len(match) == 0 {
		return Statement{}, errors.New("update needs at least one match column")
	}
	tbl, err := d.table(t)
	if err != nil {
		return Statement{}, err
	}
	b := &builder{d: d}
	sets := make([]string, len(set))
	for i, p := range set {
		sets[i] = b.ident(p.Column) + " = " + b.arg(p.Value)
	}
	b.write("UPDATE " + tbl + " SET " + strings.Join(sets, ", "))
	conds := make([]Condition, len(match))
	for i, p := range match {
		conds[i] = Condition{Column: p.Column, Operator: OpEqual, Value: p.Value}
	}
	if err := b.where(Where{Conditions: conds}); err != nil {
		return Statement{}, err
	}
	return b.finishWrite()
}

// Delete builds DELETE FROM table WHERE .... A where clause is required so a
// misconfigured node cannot empty a table.
func (d Dialect) Delete(t Table, w Where) (Statement, error) {
	if len(w.Conditions) == 0 {
		return Statement{}, errors.New("delete needs at least one condition")
	}
	tbl, err := d.table(t)
	if err != nil {
		return Statement{}, err
	}
	b := &builder{d: d}
	b.write("DELETE FROM " + tbl)
	if err := b.where(w); err != nil {
		return Statement{}, err
	}
	return b.finish(false)
}
