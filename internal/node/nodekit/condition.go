package nodekit

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Combinators of a condition group.
const (
	CombinatorAnd = "and"
	CombinatorOr  = "or"
)

// Condition compares Left with Right using a typed operator such as
// "string:contains" or "number:gt". Right is ignored by unary operators.
type Condition struct {
	Left     any
	Operator string
	Right    any
}

// Group is a list of conditions joined by a combinator.
type Group struct {
	Combinator string
	Conditions []Condition
}

// EvalOptions tune how values are compared.
type EvalOptions struct {
	// IgnoreCase makes string comparisons case-insensitive.
	IgnoreCase bool
	// Loose converts values to the operator's type instead of failing, for
	// example "5" to the number 5. It applies to expression results; typed
	// literals are converted by ReadGroup whatever this is.
	Loose bool
}

type operator struct {
	id      string
	name    string
	compare func(left, right any, o EvalOptions) (bool, error)
}

// operators is the catalogue of condition operators in display order.
var operators = buildOperators()

var operatorByID = func() map[string]operator {
	m := make(map[string]operator, len(operators))
	for _, op := range operators {
		m[op.id] = op
	}
	return m
}()

// OperatorOptions lists the operators as property options for a form.
func OperatorOptions() []nodesdk.Option {
	out := make([]nodesdk.Option, len(operators))
	for i, op := range operators {
		out[i] = nodesdk.Option{Name: op.name, Value: op.id}
	}
	return out
}

// ParseConditions converts a resolved "conditions" parameter (an array of
// {left, operator, right} objects) into conditions.
func ParseConditions(v any) ([]Condition, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case []any:
		out := make([]Condition, 0, len(t))
		for i, e := range t {
			m, ok := e.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("conditions[%d] must be an object, got %s", i, TypeName(e))
			}
			op, _ := m["operator"].(string)
			if op == "" {
				return nil, fmt.Errorf("conditions[%d]: operator is required", i)
			}
			if _, known := operatorByID[op]; !known {
				return nil, fmt.Errorf("conditions[%d]: unknown operator %q", i, op)
			}
			out = append(out, Condition{Left: m["left"], Operator: op, Right: m["right"]})
		}
		return out, nil
	}
	return nil, fmt.Errorf("conditions must be an array, got %s", TypeName(v))
}

// Eval evaluates the group. An empty group is true. Evaluation short-circuits.
func (g Group) Eval(o EvalOptions) (bool, error) {
	if len(g.Conditions) == 0 {
		return true, nil
	}
	or := g.Combinator == CombinatorOr
	for i, c := range g.Conditions {
		ok, err := c.Eval(o)
		if err != nil {
			return false, fmt.Errorf("condition %d: %w", i+1, err)
		}
		if or && ok {
			return true, nil
		}
		if !or && !ok {
			return false, nil
		}
	}
	return !or, nil
}

// Eval evaluates one condition.
func (c Condition) Eval(o EvalOptions) (bool, error) {
	op, ok := operatorByID[c.Operator]
	if !ok {
		return false, fmt.Errorf("unknown operator %q", c.Operator)
	}
	res, err := op.compare(c.Left, c.Right, o)
	if err != nil {
		return false, fmt.Errorf("%s: %w", op.name, err)
	}
	return res, nil
}

func buildOperators() []operator {
	var ops []operator
	add := func(id, name string, f func(l, r any, o EvalOptions) (bool, error)) {
		ops = append(ops, operator{id: id, name: name, compare: f})
	}
	ops = append(ops, stringOperators()...)
	ops = append(ops, numberOperators()...)
	add("boolean:isTrue", "Boolean: is true", func(l, _ any, o EvalOptions) (bool, error) { return ToBool(l, o.Loose) })
	add("boolean:isFalse", "Boolean: is false", func(l, _ any, o EvalOptions) (bool, error) {
		b, err := ToBool(l, o.Loose)
		return !b, err
	})
	ops = append(ops, dateOperators()...)
	ops = append(ops, arrayOperators()...)
	add("object:isEmpty", "Object: is empty", func(l, _ any, o EvalOptions) (bool, error) {
		m, err := toObject(l, o)
		return len(m) == 0, err
	})
	add("object:isNotEmpty", "Object: is not empty", func(l, _ any, o EvalOptions) (bool, error) {
		m, err := toObject(l, o)
		return len(m) > 0, err
	})
	add("exists", "Exists", func(l, _ any, _ EvalOptions) (bool, error) { return l != nil, nil })
	add("notExists", "Does not exist", func(l, _ any, _ EvalOptions) (bool, error) { return l == nil, nil })
	return ops
}

func toObject(v any, o EvalOptions) (map[string]any, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case map[string]any:
		return t, nil
	}
	if o.Loose {
		return nil, nil
	}
	return nil, fmt.Errorf("expected an object, got %s", TypeName(v))
}

func toArray(v any, o EvalOptions) ([]any, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case []any:
		return t, nil
	}
	if o.Loose {
		return []any{v}, nil
	}
	return nil, fmt.Errorf("expected an array, got %s", TypeName(v))
}

// textOf returns the string form of a value, failing on non-strings unless loose.
func textOf(v any, o EvalOptions) (string, error) {
	if s, ok := v.(string); ok {
		return s, nil
	}
	if v == nil {
		return "", nil
	}
	if o.Loose {
		return ToString(v), nil
	}
	return "", fmt.Errorf("expected a string, got %s", TypeName(v))
}

func textPair(l, r any, o EvalOptions) (left, right string, err error) {
	ls, err := textOf(l, o)
	if err != nil {
		return "", "", err
	}
	rs, err := textOf(r, o)
	if err != nil {
		return "", "", fmt.Errorf("right value: %w", err)
	}
	if o.IgnoreCase {
		ls, rs = strings.ToLower(ls), strings.ToLower(rs)
	}
	return ls, rs, nil
}

func stringOperators() []operator {
	binary := func(id, name string, f func(l, r string) bool) operator {
		return operator{id: "string:" + id, name: "String: " + name, compare: func(l, r any, o EvalOptions) (bool, error) {
			ls, rs, err := textPair(l, r, o)
			if err != nil {
				return false, err
			}
			return f(ls, rs), nil
		}}
	}
	regex := func(id, name string, want bool) operator {
		return operator{id: "string:" + id, name: "String: " + name, compare: func(l, r any, o EvalOptions) (bool, error) {
			ls, err := textOf(l, o)
			if err != nil {
				return false, err
			}
			pattern, err := textOf(r, o)
			if err != nil {
				return false, fmt.Errorf("right value: %w", err)
			}
			if o.IgnoreCase {
				pattern = "(?i)" + pattern
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				return false, fmt.Errorf("invalid regular expression: %w", err)
			}
			return re.MatchString(ls) == want, nil
		}}
	}
	empty := func(id, name string, want bool) operator {
		return operator{id: "string:" + id, name: "String: " + name, compare: func(l, _ any, o EvalOptions) (bool, error) {
			s, err := textOf(l, o)
			return (s == "") == want, err
		}}
	}
	return []operator{
		binary("equals", "equals", func(l, r string) bool { return l == r }),
		binary("notEquals", "does not equal", func(l, r string) bool { return l != r }),
		binary("contains", "contains", strings.Contains),
		binary("notContains", "does not contain", func(l, r string) bool { return !strings.Contains(l, r) }),
		binary("startsWith", "starts with", strings.HasPrefix),
		binary("endsWith", "ends with", strings.HasSuffix),
		regex("regex", "matches regex", true),
		regex("notRegex", "does not match regex", false),
		empty("isEmpty", "is empty", true),
		empty("isNotEmpty", "is not empty", false),
	}
}

func numberOperators() []operator {
	mk := func(id, name string, f func(l, r float64) bool) operator {
		return operator{id: "number:" + id, name: "Number: " + name, compare: func(l, r any, o EvalOptions) (bool, error) {
			lf, err := ToFloat(l, o.Loose)
			if err != nil {
				return false, err
			}
			rf, err := ToFloat(r, o.Loose)
			if err != nil {
				return false, fmt.Errorf("right value: %w", err)
			}
			return f(lf, rf), nil
		}}
	}
	return []operator{
		mk("equals", "equals", func(l, r float64) bool { return l == r }),
		mk("notEquals", "does not equal", func(l, r float64) bool { return l != r }),
		mk("gt", "is greater than", func(l, r float64) bool { return l > r }),
		mk("gte", "is greater than or equal to", func(l, r float64) bool { return l >= r }),
		mk("lt", "is less than", func(l, r float64) bool { return l < r }),
		mk("lte", "is less than or equal to", func(l, r float64) bool { return l <= r }),
	}
}

var dateLayouts = []string{
	time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04", "2006-01-02",
}

// ToTime converts a time.Time or an ISO-8601 date/datetime string to a time.
// Values without an offset are read as UTC.
func ToTime(v any) (time.Time, error) {
	switch t := v.(type) {
	case time.Time:
		return t, nil
	case string:
		s := strings.TrimSpace(t)
		for _, layout := range dateLayouts {
			if parsed, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
				return parsed, nil
			}
		}
		return time.Time{}, fmt.Errorf("%q is not an ISO-8601 date or datetime", t)
	}
	return time.Time{}, fmt.Errorf("expected a date, got %s", TypeName(v))
}

func dateOperators() []operator {
	mk := func(id, name string, f func(l, r time.Time) bool) operator {
		return operator{id: "dateTime:" + id, name: "Date: " + name, compare: func(l, r any, _ EvalOptions) (bool, error) {
			lt, err := ToTime(l)
			if err != nil {
				return false, err
			}
			rt, err := ToTime(r)
			if err != nil {
				return false, fmt.Errorf("right value: %w", err)
			}
			return f(lt, rt), nil
		}}
	}
	return []operator{
		mk("equals", "equals", func(l, r time.Time) bool { return l.Equal(r) }),
		mk("notEquals", "does not equal", func(l, r time.Time) bool { return !l.Equal(r) }),
		mk("after", "is after", func(l, r time.Time) bool { return l.After(r) }),
		mk("afterOrEquals", "is after or equal to", func(l, r time.Time) bool { return !l.Before(r) }),
		mk("before", "is before", func(l, r time.Time) bool { return l.Before(r) }),
		mk("beforeOrEquals", "is before or equal to", func(l, r time.Time) bool { return !l.After(r) }),
	}
}

func arrayOperators() []operator {
	contains := func(id, name string, want bool) operator {
		return operator{id: "array:" + id, name: "Array: " + name, compare: func(l, r any, o EvalOptions) (bool, error) {
			arr, err := toArray(l, o)
			if err != nil {
				return false, err
			}
			for _, e := range arr {
				if Equal(e, r) || (o.Loose && ToString(e) == ToString(r)) {
					return want, nil
				}
			}
			return !want, nil
		}}
	}
	length := func(id, name string, f func(n, r int) bool) operator {
		return operator{id: "array:" + id, name: "Array: " + name, compare: func(l, r any, o EvalOptions) (bool, error) {
			arr, err := toArray(l, o)
			if err != nil {
				return false, err
			}
			rf, err := ToFloat(r, o.Loose)
			if err != nil {
				return false, fmt.Errorf("right value: %w", err)
			}
			return f(len(arr), int(rf)), nil
		}}
	}
	empty := func(id, name string, want bool) operator {
		return operator{id: "array:" + id, name: "Array: " + name, compare: func(l, _ any, o EvalOptions) (bool, error) {
			arr, err := toArray(l, o)
			return (len(arr) == 0) == want, err
		}}
	}
	return []operator{
		contains("contains", "contains", true),
		contains("notContains", "does not contain", false),
		length("lengthEquals", "length equals", func(n, r int) bool { return n == r }),
		length("lengthNotEquals", "length does not equal", func(n, r int) bool { return n != r }),
		length("lengthGt", "length greater than", func(n, r int) bool { return n > r }),
		length("lengthGte", "length greater than or equal to", func(n, r int) bool { return n >= r }),
		length("lengthLt", "length less than", func(n, r int) bool { return n < r }),
		length("lengthLte", "length less than or equal to", func(n, r int) bool { return n <= r }),
		empty("isEmpty", "is empty", true),
		empty("isNotEmpty", "is not empty", false),
	}
}

// ConditionProperties returns the shared form fields for a condition list:
// combinator, conditions, ignoreCase and looseTypes. show restricts them.
func ConditionProperties(show *nodesdk.DisplayOptions) []nodesdk.Property {
	return []nodesdk.Property{
		{
			Name: "conditions", DisplayName: "Conditions", Type: nodesdk.PropFixedCollection, DisplayOptions: show,
			Description: "Each condition compares a left value with a right value using a typed operator.",
			Children: []nodesdk.Property{
				{Name: "left", DisplayName: "Value 1", Type: nodesdk.PropString},
				{Name: "operator", DisplayName: "Operator", Type: nodesdk.PropOptions, Default: "string:equals", Options: OperatorOptions()},
				{Name: "right", DisplayName: "Value 2", Type: nodesdk.PropString},
			},
		},
		{
			Name: "combinator", DisplayName: "Combine", Type: nodesdk.PropOptions, Default: CombinatorAnd, DisplayOptions: show,
			Options: []nodesdk.Option{{Name: "All conditions (AND)", Value: CombinatorAnd}, {Name: "Any condition (OR)", Value: CombinatorOr}},
		},
		{Name: "ignoreCase", DisplayName: "Ignore case", Type: nodesdk.PropBoolean, Default: false, DisplayOptions: show,
			Description: "Compare strings without regard to letter case."},
		{Name: "looseTypes", DisplayName: "Loose type validation", Type: nodesdk.PropBoolean, Default: false, DisplayOptions: show,
			Description: "Also convert values produced by expressions to the operator's type (for example \"5\" to 5) instead of failing. Typed literals are always converted."},
	}
}

// ExpressionPrefix marks a parameter string as an expression template. It is
// the prefix the engine's expression evaluator resolves.
const ExpressionPrefix = "="

// RawParamSource is an optional capability of a nodesdk.Input: a parameter as
// stored, before expressions are resolved. With it a typed literal (text typed
// into a field, stored without ExpressionPrefix) is told apart from the result
// of an expression. ok is false when nothing is stored for the parameter.
// Inputs without the method treat every value as an expression result.
type RawParamSource interface {
	RawParam(name string, itemIndex int) (value nodesdk.Value, ok bool)
}

// literalTypes returns the value type that a literal left and right value of
// an operator converts to, or "" when the operator does not convert it.
// Strings and dates need no conversion: the operator parses them itself.
func literalTypes(opID string) (left, right string) {
	switch {
	case strings.HasPrefix(opID, "number:"):
		return TypeNumber, TypeNumber
	case strings.HasPrefix(opID, "boolean:"):
		return TypeBoolean, ""
	case strings.HasPrefix(opID, "array:length"):
		return "", TypeNumber
	}
	return "", ""
}

// coerceLiteral converts a literal, a string that was not an expression, to
// typ with the same rules as loose type validation. Other values, and
// expression results, are returned unchanged.
func coerceLiteral(stored, resolved any, typ string) (any, error) {
	s, ok := stored.(string)
	if !ok || typ == "" || strings.HasPrefix(s, ExpressionPrefix) {
		return resolved, nil
	}
	return Coerce(s, typ)
}

// StoredParam returns a parameter as stored, before expressions resolve, when
// the input can report it (see RawParamSource). ok is false otherwise.
func StoredParam(in nodesdk.Input, name string, itemIndex int) (value any, ok bool) {
	src, isSource := in.(RawParamSource)
	if !isSource {
		return nil, false
	}
	return src.RawParam(name, itemIndex)
}

// CoerceLiterals converts the typed literals of conds in place. stored is the
// conditions array as stored (see StoredParam); anything other than an array,
// such as a whole-parameter expression, or nil means no literals, so every
// value stays strictly typed.
func CoerceLiterals(stored any, conds []Condition) error {
	entries, _ := stored.([]any)
	for i := range conds {
		if i >= len(entries) {
			break
		}
		entry, ok := entries[i].(map[string]any)
		if !ok {
			continue
		}
		leftType, rightType := literalTypes(conds[i].Operator)
		name := operatorByID[conds[i].Operator].name
		left, err := coerceLiteral(entry["left"], conds[i].Left, leftType)
		if err != nil {
			return fmt.Errorf("conditions[%d] (%s): value 1: %w", i, name, err)
		}
		right, err := coerceLiteral(entry["right"], conds[i].Right, rightType)
		if err != nil {
			return fmt.Errorf("conditions[%d] (%s): value 2: %w", i, name, err)
		}
		conds[i].Left, conds[i].Right = left, right
	}
	return nil
}

// ReadGroup reads the properties of ConditionProperties for one input item.
// Typed literals are converted to their operator's type here, so an error in
// a literal fails the item whether or not an earlier condition would stop the
// evaluation.
func ReadGroup(ctx context.Context, in nodesdk.Input, itemIndex int) (Group, EvalOptions, error) {
	var g Group
	raw, err := in.Param(ctx, "conditions", itemIndex)
	if err != nil {
		return g, EvalOptions{}, fmt.Errorf("reading conditions: %w", err)
	}
	if g.Conditions, err = ParseConditions(raw); err != nil {
		return g, EvalOptions{}, err
	}
	if stored, ok := StoredParam(in, "conditions", itemIndex); ok {
		if err := CoerceLiterals(stored, g.Conditions); err != nil {
			return g, EvalOptions{}, err
		}
	}
	if g.Combinator, err = nodesdk.ParamString(ctx, in, "combinator", itemIndex); err != nil {
		return g, EvalOptions{}, fmt.Errorf("reading combinator: %w", err)
	}
	var o EvalOptions
	if o.IgnoreCase, err = nodesdk.ParamBool(ctx, in, "ignoreCase", itemIndex); err != nil {
		return g, o, fmt.Errorf("reading ignoreCase: %w", err)
	}
	if o.Loose, err = nodesdk.ParamBool(ctx, in, "looseTypes", itemIndex); err != nil {
		return g, o, fmt.Errorf("reading looseTypes: %w", err)
	}
	return g, o, nil
}
