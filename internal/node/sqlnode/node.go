package sqlnode

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Pools hands out shared connection pools (satisfied by port.DBPool).
type Pools interface {
	DB(ctx context.Context, driver string, cred nodesdk.Credential) (*sql.DB, error)
}

// Operations.
const (
	opExecuteQuery = "executeQuery"
	opSelect       = "select"
	opInsert       = "insert"
	opUpdate       = "update"
	opUpsert       = "upsert"
	opDelete       = "delete"
)

// Run modes of Execute Query and Select.
const (
	runPerItem = "perItem"
	runOnce    = "once"
)

// Transaction modes.
const (
	txNone    = "none"
	txAll     = "all"
	txPerItem = "perItem"
)

// Data modes of the write operations.
const (
	dataAutoMap = "autoMap"
	dataDefine  = "define"
)

// Spec describes one SQL node flavor.
type Spec struct {
	Dialect        Dialect
	Type           string
	DisplayName    string
	CredentialType string
	// Schema adds a schema parameter (Postgres).
	Schema bool
	// Placeholder is shown in the query parameter's help, e.g. "$1".
	Placeholder string
}

// Node is a SQL database node.
type Node struct {
	spec  Spec
	pools Pools
}

// New returns a SQL node. Without pools Execute fails with nodesdk.ErrUnavailable.
func New(spec Spec, pools Pools) *Node { return &Node{spec: spec, pools: pools} }

// Describe implements nodesdk.Node.
func (n *Node) Describe() nodesdk.Descriptor {
	ops := []string{opExecuteQuery, opSelect, opInsert, opUpdate, opUpsert, opDelete}
	names := []string{"Execute Query", "Select", "Insert", "Update", "Insert or Update (Upsert)", "Delete"}
	opOpts := make([]nodesdk.Option, len(ops))
	for i := range ops {
		opOpts[i] = nodekit.Opt(ops[i], names[i])
	}
	table := nodekit.Show("operation", opSelect, opInsert, opUpdate, opUpsert, opDelete)
	whereOps := nodekit.Show("operation", opSelect, opDelete)
	writes := nodekit.Show("operation", opInsert, opUpdate, opUpsert)
	props := []nodesdk.Property{
		{Name: "operation", DisplayName: "Operation", Type: nodesdk.PropOptions, Default: opExecuteQuery, Options: opOpts},
	}
	if n.spec.Schema {
		props = append(props, nodesdk.Property{Name: "schema", DisplayName: "Schema", Type: nodesdk.PropString, Default: "public", DisplayOptions: table})
	}
	props = append(props,
		nodesdk.Property{Name: "table", DisplayName: "Table", Type: nodesdk.PropString, Required: true, DisplayOptions: table},
		nodesdk.Property{Name: "query", DisplayName: "Query", Type: nodesdk.PropCode, Required: true, DisplayOptions: nodekit.Show("operation", opExecuteQuery),
			Description: "SQL text. Reference values only through placeholders (" + n.spec.Placeholder + "), never by pasting data into the text."},
		nodesdk.Property{Name: "queryParams", DisplayName: "Query parameters", Type: nodesdk.PropJSON, DisplayOptions: nodekit.Show("operation", opExecuteQuery),
			Description: "Array of values bound to the placeholders in order, e.g. [{{ $json.id }}, \"open\"]."},
		nodesdk.Property{Name: "columns", DisplayName: "Columns", Type: nodesdk.PropString, DisplayOptions: nodekit.Show("operation", opSelect),
			Description: "Comma-separated columns to return. Empty returns all."},
		nodesdk.Property{Name: "combinator", DisplayName: "Combine conditions with", Type: nodesdk.PropOptions, Default: CombineAnd, DisplayOptions: whereOps,
			Options: []nodesdk.Option{nodekit.Opt(CombineAnd, "AND"), nodekit.Opt(CombineOr, "OR")}},
		nodesdk.Property{Name: "where", DisplayName: "Conditions", Type: nodesdk.PropFixedCollection, DisplayOptions: whereOps,
			Description: "Delete requires at least one condition.",
			Children: []nodesdk.Property{
				{Name: "column", DisplayName: "Column", Type: nodesdk.PropString, Required: true},
				{Name: "operator", DisplayName: "Operator", Type: nodesdk.PropOptions, Default: OpEqual, Options: whereOperatorOptions()},
				{Name: "value", DisplayName: "Value", Type: nodesdk.PropString},
			}},
		nodesdk.Property{Name: "sortColumn", DisplayName: "Sort by", Type: nodesdk.PropString, DisplayOptions: nodekit.Show("operation", opSelect)},
		nodesdk.Property{Name: "sortDirection", DisplayName: "Sort direction", Type: nodesdk.PropOptions, Default: "asc", DisplayOptions: nodekit.Show("operation", opSelect),
			Options: []nodesdk.Option{nodekit.Opt("asc", "Ascending"), nodekit.Opt(SortDesc, "Descending")}},
		nodesdk.Property{Name: "limit", DisplayName: "Limit", Type: nodesdk.PropNumber, Default: float64(0), DisplayOptions: nodekit.Show("operation", opSelect),
			Description: "Maximum rows to return. 0 means no limit."},
		nodesdk.Property{Name: "dataMode", DisplayName: "Data", Type: nodesdk.PropOptions, Default: dataAutoMap, DisplayOptions: writes,
			Options: []nodesdk.Option{nodekit.Opt(dataAutoMap, "Map input fields to columns by name"), nodekit.Opt(dataDefine, "Define columns below")}},
		nodesdk.Property{Name: "values", DisplayName: "Column values", Type: nodesdk.PropFixedCollection, DisplayOptions: nodekit.Show("dataMode", dataDefine),
			Children: []nodesdk.Property{
				{Name: "name", DisplayName: "Column", Type: nodesdk.PropString, Required: true},
				{Name: "value", DisplayName: "Value", Type: nodesdk.PropString},
			}},
		nodesdk.Property{Name: "matchColumns", DisplayName: "Match columns", Type: nodesdk.PropString, DisplayOptions: nodekit.Show("operation", opUpdate, opUpsert),
			Description: "Comma-separated columns that identify the row (WHERE for update, conflict key for upsert). Their values come from the data above."},
		nodesdk.Property{Name: "runMode", DisplayName: "Run", Type: nodesdk.PropOptions, Default: runPerItem, DisplayOptions: nodekit.Show("operation", opExecuteQuery, opSelect),
			Description: "Once runs a single time using the first item; per item runs for every input item. Writes always run per item.",
			Options:     []nodesdk.Option{nodekit.Opt(runPerItem, "Once for each item"), nodekit.Opt(runOnce, "Once for all items")}},
		nodesdk.Property{Name: "transaction", DisplayName: "Transaction", Type: nodesdk.PropOptions, Default: txNone,
			Description: "All wraps every statement of the run in one transaction; per item commits each item on its own. Any error rolls back the open transaction.",
			Options:     []nodesdk.Option{nodekit.Opt(txNone, "None"), nodekit.Opt(txAll, "Single transaction for all items"), nodekit.Opt(txPerItem, "One transaction per item")}},
		nodesdk.Property{Name: "numbersAsString", DisplayName: "Bigint and decimal as string", Type: nodesdk.PropBoolean, Default: false,
			Description: "Return BIGINT and NUMERIC/DECIMAL columns as strings to keep full precision. Timestamps are always RFC 3339 (UTC)."},
	)
	return nodesdk.Descriptor{
		Type: n.spec.Type, Version: 1, DisplayName: n.spec.DisplayName, Group: nodesdk.GroupAction, Icon: "database",
		Description: "Run SQL against " + n.spec.DisplayName + ": query, select, insert, update, upsert or delete.",
		Inputs:      []nodesdk.PortSpec{nodekit.Main("main", "")},
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Credentials: []nodesdk.CredentialRef{{Name: n.spec.CredentialType, Type: n.spec.CredentialType, Required: true}},
		Properties:  props,
	}
}

func whereOperatorOptions() []nodesdk.Option {
	pairs := [][2]string{
		{OpEqual, "Equals"}, {OpNotEqual, "Not equals"}, {OpGreater, "Greater than"}, {OpGreaterEq, "Greater than or equal"},
		{OpLess, "Less than"}, {OpLessEq, "Less than or equal"}, {OpLike, "Like"}, {OpNotLike, "Not like"},
		{OpILike, "Like (case-insensitive)"}, {OpIsNull, "Is null"}, {OpIsNotNull, "Is not null"}, {OpIn, "In list"}, {OpNotIn, "Not in list"},
	}
	out := make([]nodesdk.Option, len(pairs))
	for i, p := range pairs {
		out[i] = nodekit.Opt(p[0], p[1])
	}
	return out
}

// planned is a built statement and the input item it belongs to.
type planned struct {
	item int
	st   Statement
}

// Execute implements nodesdk.Node.
func (n *Node) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	items := in.Items(0)
	if len(items) == 0 {
		return nodesdk.Output{Ports: [][]nodesdk.Item{nil}}, nil
	}
	if n.pools == nil {
		return nodesdk.Output{}, fmt.Errorf("database pools: %w", nodesdk.ErrUnavailable)
	}
	op, err := nodesdk.ParamString(ctx, in, "operation", 0)
	if err != nil {
		return nodesdk.Output{}, err
	}
	txMode, err := nodesdk.ParamString(ctx, in, "transaction", 0)
	if err != nil {
		return nodesdk.Output{}, err
	}
	asString, err := nodesdk.ParamBool(ctx, in, "numbersAsString", 0)
	if err != nil {
		return nodesdk.Output{}, err
	}
	if !slices.Contains([]string{txNone, txAll, txPerItem}, txMode) {
		return nodesdk.Output{}, fmt.Errorf("unknown transaction mode %q", txMode)
	}
	plan, err := n.plan(ctx, in, op, len(items))
	if err != nil {
		return nodesdk.Output{}, err
	}
	cred, err := in.Credential(ctx, n.spec.CredentialType)
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("loading credential %q: %w", n.spec.CredentialType, err)
	}
	db, err := n.pools.DB(ctx, n.spec.Dialect.Name, cred)
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("opening %s pool: %w", n.spec.Dialect.Name, err)
	}
	var out []nodesdk.Item
	collect := func(item int, rows []map[string]any) {
		for _, r := range rows {
			out = append(out, nodekit.Paired(nodesdk.Item{JSON: r}, item))
		}
	}
	if err := execute(ctx, db, plan, txMode, ResultOptions{NumbersAsString: asString}, collect); err != nil {
		return nodesdk.Output{}, err
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

// execute runs the plan under the transaction mode.
func execute(ctx context.Context, db *sql.DB, plan []planned, txMode string, o ResultOptions, collect func(int, []map[string]any)) error {
	run := func(q Querier, p planned) error {
		rows, err := Run(ctx, q, p.st, o)
		if err != nil {
			return nodekit.ItemError(p.item, err)
		}
		collect(p.item, rows)
		return nil
	}
	switch txMode {
	case txAll:
		return inTx(ctx, db, plan, run)
	case txPerItem:
		for i := 0; i < len(plan); {
			j := i
			for j < len(plan) && plan[j].item == plan[i].item {
				j++
			}
			if err := inTx(ctx, db, plan[i:j], run); err != nil {
				return err
			}
			i = j
		}
	default:
		for _, p := range plan {
			if err := run(db, p); err != nil {
				return err
			}
		}
	}
	return nil
}

// inTx runs the group in one transaction: commit on success, rollback on error.
func inTx(ctx context.Context, db *sql.DB, group []planned, run func(Querier, planned) error) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer rollbackOnError(tx, &err)
	for _, p := range group {
		if err := run(tx, p); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}
	return nil
}

// rollbackOnError rolls tx back when the surrounding function fails.
func rollbackOnError(tx *sql.Tx, errp *error) {
	if *errp == nil {
		return
	}
	if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
		*errp = errors.Join(*errp, fmt.Errorf("rolling back: %w", rbErr))
	}
}

// plan builds every statement before any of them runs, so parameter errors
// never leave a half-applied batch.
func (n *Node) plan(ctx context.Context, in nodesdk.Input, op string, count int) ([]planned, error) {
	indexes := make([]int, count)
	for i := range indexes {
		indexes[i] = i
	}
	if op == opExecuteQuery || op == opSelect {
		mode, err := nodesdk.ParamString(ctx, in, "runMode", 0)
		if err != nil {
			return nil, err
		}
		if mode == runOnce {
			indexes = indexes[:1]
		}
	}
	plan := make([]planned, 0, len(indexes))
	for _, i := range indexes {
		st, err := n.statement(ctx, in, op, i)
		if err != nil {
			return nil, nodekit.ItemError(i, err)
		}
		plan = append(plan, planned{item: i, st: st})
	}
	return plan, nil
}

func (n *Node) statement(ctx context.Context, in nodesdk.Input, op string, i int) (Statement, error) {
	d := n.spec.Dialect
	if op == opExecuteQuery {
		q, err := nodesdk.ParamString(ctx, in, "query", i)
		if err != nil {
			return Statement{}, err
		}
		raw, err := in.Param(ctx, "queryParams", i)
		if err != nil {
			return Statement{}, fmt.Errorf("reading parameter %q: %w", "queryParams", err)
		}
		args, err := bindList(raw)
		if err != nil {
			return Statement{}, err
		}
		for k := range args {
			args[k] = coerceArg(args[k])
		}
		return Statement{SQL: q, Args: args, ReturnsRows: returnsRows(q)}, nil
	}
	var t Table
	var err error
	if n.spec.Schema {
		if t.Schema, err = nodesdk.ParamString(ctx, in, "schema", i); err != nil {
			return Statement{}, err
		}
	}
	if t.Name, err = nodesdk.ParamString(ctx, in, "table", i); err != nil {
		return Statement{}, err
	}
	switch op {
	case opSelect, opDelete:
		w, err := readWhere(ctx, in, i)
		if err != nil {
			return Statement{}, err
		}
		if op == opDelete {
			return d.Delete(t, w)
		}
		cols, err := nodekit.ParamStringList(ctx, in, "columns", i)
		if err != nil {
			return Statement{}, err
		}
		sortCol, err := nodesdk.ParamString(ctx, in, "sortColumn", i)
		if err != nil {
			return Statement{}, err
		}
		dir, err := nodesdk.ParamString(ctx, in, "sortDirection", i)
		if err != nil {
			return Statement{}, err
		}
		limit, err := nodesdk.ParamInt(ctx, in, "limit", i)
		if err != nil {
			return Statement{}, err
		}
		return d.Select(t, cols, w, Sort{Column: sortCol, Desc: dir == SortDesc}, limit)
	case opInsert, opUpdate, opUpsert:
		return n.writeStatement(ctx, in, op, t, i)
	}
	return Statement{}, fmt.Errorf("unknown operation %q", op)
}

func (n *Node) writeStatement(ctx context.Context, in nodesdk.Input, op string, t Table, i int) (Statement, error) {
	d := n.spec.Dialect
	row, err := readRow(ctx, in, i)
	if err != nil {
		return Statement{}, err
	}
	if op == opInsert {
		return d.Insert(t, row)
	}
	match, err := nodekit.ParamStringList(ctx, in, "matchColumns", i)
	if err != nil {
		return Statement{}, err
	}
	if op == opUpsert {
		return d.Upsert(t, row, match)
	}
	var set, where []Pair
	for _, p := range row {
		if slices.Contains(match, p.Column) {
			where = append(where, p)
		} else {
			set = append(set, p)
		}
	}
	if len(where) != len(match) {
		return Statement{}, errors.New("every match column needs a value in the data")
	}
	return d.Update(t, set, where)
}

// readRow returns the column/value pairs of a write: the item's fields in key
// order (autoMap) or the defined list.
func readRow(ctx context.Context, in nodesdk.Input, i int) ([]Pair, error) {
	mode, err := nodesdk.ParamString(ctx, in, "dataMode", i)
	if err != nil {
		return nil, err
	}
	if mode == dataDefine {
		raw, err := in.Param(ctx, "values", i)
		if err != nil {
			return nil, fmt.Errorf("reading parameter %q: %w", "values", err)
		}
		kvs, err := nodekit.KeyValues(raw)
		if err != nil {
			return nil, fmt.Errorf("values: %w", err)
		}
		out := make([]Pair, len(kvs))
		for k, kv := range kvs {
			out[k] = Pair{Column: kv.Name, Value: kv.Value}
		}
		return out, nil
	}
	data := in.Items(0)[i].JSON
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := make([]Pair, len(keys))
	for k, key := range keys {
		out[k] = Pair{Column: key, Value: data[key]}
	}
	return out, nil
}

func readWhere(ctx context.Context, in nodesdk.Input, i int) (Where, error) {
	comb, err := nodesdk.ParamString(ctx, in, "combinator", i)
	if err != nil {
		return Where{}, err
	}
	raw, err := in.Param(ctx, "where", i)
	if err != nil {
		return Where{}, fmt.Errorf("reading parameter %q: %w", "where", err)
	}
	w := Where{Combinator: comb}
	switch t := raw.(type) {
	case nil:
	case []any:
		for k, e := range t {
			m, ok := e.(map[string]any)
			if !ok {
				return Where{}, fmt.Errorf("where[%d] must be an object, got %s", k, nodekit.TypeName(e))
			}
			col, _ := m["column"].(string)
			op, _ := m["operator"].(string)
			if op == "" {
				op = OpEqual
			}
			w.Conditions = append(w.Conditions, Condition{Column: strings.TrimSpace(col), Operator: op, Value: m["value"]})
		}
	default:
		return Where{}, fmt.Errorf("where must be an array, got %s", nodekit.TypeName(raw))
	}
	return w, nil
}

// bindList turns the queryParams value into positional arguments.
func bindList(v any) ([]any, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case []any:
		return slices.Clone(t), nil
	case string:
		if strings.TrimSpace(t) == "" {
			return nil, nil
		}
		var out []any
		if err := json.Unmarshal([]byte(t), &out); err != nil {
			return nil, fmt.Errorf("queryParams must be a JSON array: %w", err)
		}
		return out, nil
	}
	return nil, fmt.Errorf("queryParams must be an array, got %s", nodekit.TypeName(v))
}
