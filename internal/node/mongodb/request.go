package mongodb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// request is one validated command for one input item.
type request struct {
	op         string
	collection string
	filter     bson.D
	projection bson.D
	sort       bson.D
	limit      int64
	update     any // bson.D or a pipeline ([]bson.D)
	upsert     bool
	after      bool
	multiple   bool
	documents  []any
	pipeline   []bson.D
}

// parse reads and validates the parameters of item i. maxResults caps find
// and aggregate output.
func parse(ctx context.Context, in nodesdk.Input, op string, i, maxResults int) (request, error) {
	r := request{op: op}
	switch op {
	case opFind, opFindOneUpdate, opInsert, opUpdate, opDelete, opAggregate:
	default:
		return r, fmt.Errorf("unknown operation %q", op)
	}
	var err error
	if r.collection, err = nodesdk.ParamString(ctx, in, "collection", i); err != nil {
		return r, err
	}
	if r.collection == "" {
		return r, errors.New("collection must not be empty")
	}
	doc := func(name string) (bson.D, error) {
		raw, err := in.Param(ctx, name, i)
		if err != nil {
			return nil, fmt.Errorf("reading parameter %q: %w", name, err)
		}
		d, err := document(raw)
		if err != nil {
			return nil, fmt.Errorf("parameter %q: %w", name, err)
		}
		return d, nil
	}
	switch op {
	case opFind, opFindOneUpdate, opUpdate, opDelete:
		if r.filter, err = doc("filter"); err != nil {
			return r, err
		}
		if (op == opUpdate || op == opDelete) && len(r.filter) == 0 {
			return r, fmt.Errorf("%s needs a non-empty filter", op)
		}
	}
	switch op {
	case opFind, opFindOneUpdate:
		if r.projection, err = doc("projection"); err != nil {
			return r, err
		}
		if r.sort, err = doc("sort"); err != nil {
			return r, err
		}
	}
	switch op {
	case opFind:
		lim, err := nodekit.ParamFloat(ctx, in, "limit", i)
		if err != nil {
			return r, err
		}
		if lim < 0 || lim != math.Trunc(lim) {
			return r, errors.New("limit must be a whole number, 0 or more")
		}
		r.limit = int64(maxResults)
		if lim > 0 && int64(lim) < r.limit {
			r.limit = int64(lim)
		}
	case opFindOneUpdate, opUpdate:
		if r.update, err = updateSpec(ctx, in, i); err != nil {
			return r, err
		}
		if r.upsert, err = nodesdk.ParamBool(ctx, in, "upsert", i); err != nil {
			return r, err
		}
		if op == opFindOneUpdate {
			ret, err := nodesdk.ParamString(ctx, in, "returnDocument", i)
			if err != nil {
				return r, err
			}
			if ret != returnAfter && ret != returnBefore {
				return r, fmt.Errorf("unknown returnDocument %q", ret)
			}
			r.after = ret == returnAfter
		}
	case opAggregate:
		raw, err := in.Param(ctx, "pipeline", i)
		if err != nil {
			return r, fmt.Errorf("reading parameter %q: %w", "pipeline", err)
		}
		if r.pipeline, err = stages(raw); err != nil {
			return r, fmt.Errorf("parameter %q: %w", "pipeline", err)
		}
		if len(r.pipeline) == 0 {
			return r, errors.New("pipeline needs at least one stage")
		}
		r.limit = int64(maxResults)
	case opInsert:
		if r.documents, err = insertDocs(ctx, in, i); err != nil {
			return r, err
		}
	}
	if op == opUpdate || op == opDelete {
		if r.multiple, err = nodesdk.ParamBool(ctx, in, "multiple", i); err != nil {
			return r, err
		}
	}
	return r, nil
}

func updateSpec(ctx context.Context, in nodesdk.Input, i int) (any, error) {
	raw, err := in.Param(ctx, "update", i)
	if err != nil {
		return nil, fmt.Errorf("reading parameter %q: %w", "update", err)
	}
	if text, ok := raw.(string); ok && bytes.HasPrefix(bytes.TrimSpace([]byte(text)), []byte("[")) {
		st, err := stages(raw)
		if err != nil {
			return nil, fmt.Errorf("parameter %q: %w", "update", err)
		}
		if len(st) == 0 {
			return nil, errors.New("update pipeline needs at least one stage")
		}
		return st, nil
	}
	if _, ok := raw.([]any); ok {
		st, err := stages(raw)
		if err != nil || len(st) == 0 {
			return nil, errors.New("update pipeline needs at least one stage")
		}
		return st, nil
	}
	d, err := document(raw)
	if err != nil {
		return nil, fmt.Errorf("parameter %q: %w", "update", err)
	}
	if len(d) == 0 {
		return nil, errors.New("update must not be empty")
	}
	return d, nil
}

func insertDocs(ctx context.Context, in nodesdk.Input, i int) ([]any, error) {
	raw, err := in.Param(ctx, "documents", i)
	if err != nil {
		return nil, fmt.Errorf("reading parameter %q: %w", "documents", err)
	}
	if isBlank(raw) {
		raw = in.Items(0)[i].JSON
	}
	if text, ok := raw.(string); ok && bytes.HasPrefix(bytes.TrimSpace([]byte(text)), []byte("[")) {
		st, err := stages(raw)
		if err != nil {
			return nil, fmt.Errorf("parameter %q: %w", "documents", err)
		}
		return stagesToAny(st), nil
	}
	if list, ok := raw.([]any); ok {
		st, err := stages(list)
		if err != nil {
			return nil, fmt.Errorf("parameter %q: %w", "documents", err)
		}
		return stagesToAny(st), nil
	}
	d, err := document(raw)
	if err != nil {
		return nil, fmt.Errorf("parameter %q: %w", "documents", err)
	}
	if len(d) == 0 {
		return nil, errors.New("there is no document to insert")
	}
	return []any{d}, nil
}

func stagesToAny(st []bson.D) []any {
	out := make([]any, len(st))
	for i, d := range st {
		out[i] = d
	}
	return out
}

func isBlank(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return len(bytes.TrimSpace([]byte(t))) == 0
	case map[string]any:
		return len(t) == 0
	case []any:
		return len(t) == 0
	}
	return false
}

// document converts a JSON parameter (text or object) to an ordered BSON
// document. A blank value is an empty document. Extended JSON is understood.
func document(v any) (bson.D, error) {
	if isBlank(v) {
		return bson.D{}, nil
	}
	raw, err := rawJSON(v)
	if err != nil {
		return nil, err
	}
	var d bson.D
	if err := bson.UnmarshalExtJSON(raw, false, &d); err != nil {
		return nil, fmt.Errorf("not a valid JSON object: %w", err)
	}
	return d, nil
}

// stages converts a JSON array (text or list) to ordered BSON documents.
func stages(v any) ([]bson.D, error) {
	if isBlank(v) {
		return nil, nil
	}
	raw, err := rawJSON(v)
	if err != nil {
		return nil, err
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, errors.New("must be a JSON array of objects")
	}
	out := make([]bson.D, len(parts))
	for k, p := range parts {
		if err := bson.UnmarshalExtJSON(p, false, &out[k]); err != nil {
			return nil, fmt.Errorf("element %d is not a valid JSON object: %w", k, err)
		}
	}
	return out, nil
}

// rawJSON returns the JSON text of a parameter, keeping the key order of text.
func rawJSON(v any) ([]byte, error) {
	if s, ok := v.(string); ok {
		return []byte(s), nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encoding value: %w", err)
	}
	return raw, nil
}
