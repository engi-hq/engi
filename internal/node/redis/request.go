package redis

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// request is one validated command for one input item.
type request struct {
	op      string
	key     string
	value   string
	ttl     time.Duration
	amount  int64
	pattern string
	field   string
	fields  map[string]any
	right   bool
	channel string
}

// parse reads and validates the parameters of item i.
func parse(ctx context.Context, in nodesdk.Input, op string, i int) (request, error) {
	r := request{op: op}
	str := func(name string) (string, error) { return nodesdk.ParamString(ctx, in, name, i) }
	var err error
	switch op {
	case opGet, opDelete, opIncr, opDecr, opSet, opHashGet, opHashSet, opListPush, opListPop:
		if r.key, err = str("key"); err != nil {
			return r, err
		}
		if r.key == "" {
			return r, errors.New("key must not be empty")
		}
	case opKeys, opPublish:
	default:
		return r, fmt.Errorf("unknown operation %q", op)
	}
	switch op {
	case opSet, opListPush:
		raw, err := in.Param(ctx, "value", i)
		if err != nil {
			return r, fmt.Errorf("reading parameter %q: %w", "value", err)
		}
		r.value = nodekit.ToString(raw)
		if op == opSet {
			secs, err := nodekit.ParamFloat(ctx, in, "ttl", i)
			if err != nil {
				return r, err
			}
			if secs < 0 || math.IsNaN(secs) {
				return r, errors.New("ttl must not be negative")
			}
			r.ttl = time.Duration(secs * float64(time.Second))
		}
	case opIncr, opDecr:
		amount, err := nodekit.ParamFloat(ctx, in, "amount", i)
		if err != nil {
			return r, err
		}
		if amount != math.Trunc(amount) {
			return r, errors.New("amount must be a whole number")
		}
		r.amount = int64(amount)
	case opKeys:
		if r.pattern, err = str("pattern"); err != nil {
			return r, err
		}
		if r.pattern == "" {
			r.pattern = "*"
		}
	case opHashGet:
		r.field, err = str("field")
	case opHashSet:
		if r.fields, err = nodekit.ParamMap(ctx, in, "fields", i); err == nil && len(r.fields) == 0 {
			err = errors.New("fields must hold at least one field")
		}
	case opPublish:
		if r.channel, err = str("channel"); err != nil {
			return r, err
		}
		if r.channel == "" {
			return r, errors.New("channel must not be empty")
		}
		var raw any
		if raw, err = in.Param(ctx, "message", i); err == nil {
			r.value = nodekit.ToString(raw)
		}
	}
	if err != nil {
		return r, err
	}
	if op == opListPush || op == opListPop {
		side, err := str("side")
		if err != nil {
			return r, err
		}
		if !slices.Contains([]string{sideLeft, sideRight}, side) {
			return r, fmt.Errorf("unknown list end %q", side)
		}
		r.right = side == sideRight
	}
	return r, nil
}

// hashValue renders a hash field value the way a string value is stored.
func hashValue(v any) string { return nodekit.ToString(v) }
