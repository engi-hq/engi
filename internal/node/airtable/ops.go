package airtable

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

type runner struct {
	cfg Config
	in  nodesdk.Input
	c   *client
}

// target is the table a record is sent to.
type target struct{ base, table string }

func (t target) path() string { return "/" + url.PathEscape(t.base) + "/" + url.PathEscape(t.table) }

func (r *runner) target(ctx context.Context, i int) (target, error) {
	base, err := nodesdk.ParamString(ctx, r.in, "baseId", i)
	if err != nil {
		return target{}, err
	}
	table, err := nodesdk.ParamString(ctx, r.in, "table", i)
	if err != nil {
		return target{}, err
	}
	if strings.TrimSpace(base) == "" || strings.TrimSpace(table) == "" {
		return target{}, errors.New("baseId and table must not be empty")
	}
	return target{strings.TrimSpace(base), strings.TrimSpace(table)}, nil
}

// perItem runs list and get, one or more requests per input item.
func (r *runner) perItem(ctx context.Context, op string, items []nodesdk.Item) (nodesdk.Output, error) {
	var out []nodesdk.Item
	for i := range items {
		recs, err := r.one(ctx, op, i)
		if err != nil {
			return nodesdk.Output{}, nodekit.ItemFailure(i, err)
		}
		for _, rec := range recs {
			out = append(out, nodekit.Paired(nodesdk.Item{JSON: rec}, i))
		}
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

func (r *runner) one(ctx context.Context, op string, i int) ([]map[string]any, error) {
	t, err := r.target(ctx, i)
	if err != nil {
		return nil, err
	}
	if op == opGet {
		id, err := r.recordID(ctx, i)
		if err != nil {
			return nil, err
		}
		rec, err := r.c.call(ctx, http.MethodGet, t.path()+"/"+url.PathEscape(id), nil, nil)
		if err != nil {
			return nil, err
		}
		return []map[string]any{rec}, nil
	}
	return r.list(ctx, t, i)
}

func (r *runner) recordID(ctx context.Context, i int) (string, error) {
	id, err := nodesdk.ParamString(ctx, r.in, "recordId", i)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(id) == "" {
		return "", errors.New("recordId must not be empty")
	}
	return strings.TrimSpace(id), nil
}

// list follows Airtable's offset cursor until the records run out or the
// limit (never above nodes.airtable.maxListResults) is reached.
func (r *runner) list(ctx context.Context, t target, i int) ([]map[string]any, error) {
	limit, err := nodesdk.ParamInt(ctx, r.in, "limit", i)
	if err != nil {
		return nil, err
	}
	if limit < 0 {
		return nil, errors.New("limit must not be negative")
	}
	if limit == 0 || limit > r.cfg.MaxListResults {
		limit = r.cfg.MaxListResults
	}
	q := url.Values{}
	if f, err := nodesdk.ParamString(ctx, r.in, "filterByFormula", i); err != nil {
		return nil, err
	} else if f != "" {
		q.Set("filterByFormula", f)
	}
	if v, err := nodesdk.ParamString(ctx, r.in, "view", i); err != nil {
		return nil, err
	} else if v != "" {
		q.Set("view", v)
	}
	fields, err := nodekit.ParamStringList(ctx, r.in, "returnFields", i)
	if err != nil {
		return nil, err
	}
	for _, f := range fields {
		q.Add("fields[]", f)
	}
	if err := r.addSort(ctx, q, i); err != nil {
		return nil, err
	}
	var out []map[string]any
	for {
		q.Set("pageSize", strconv.Itoa(min(r.cfg.PageSize, limit-len(out))))
		body, err := r.c.call(ctx, http.MethodGet, t.path(), q, nil)
		if err != nil {
			return nil, err
		}
		page, _ := body["records"].([]any)
		for _, p := range page {
			if m, ok := p.(map[string]any); ok && len(out) < limit {
				out = append(out, m)
			}
		}
		offset, _ := body["offset"].(string)
		if offset == "" || len(out) >= limit {
			return out, nil
		}
		q.Set("offset", offset)
	}
}

// addSort reads the sort parameter: an array of {field, direction}.
func (r *runner) addSort(ctx context.Context, q url.Values, i int) error {
	v, err := r.in.Param(ctx, "sort", i)
	if err != nil {
		return fmt.Errorf("reading parameter %q: %w", "sort", err)
	}
	if s, ok := v.(string); ok {
		if strings.TrimSpace(s) == "" {
			return nil
		}
		var arr []any
		if err := json.Unmarshal([]byte(s), &arr); err != nil {
			return fmt.Errorf("sort is not a JSON array: %w", err)
		}
		v = arr
	}
	if v == nil {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return fmt.Errorf("sort must be an array, got %s", nodekit.TypeName(v))
	}
	for k, e := range arr {
		m, _ := e.(map[string]any)
		field, _ := m["field"].(string)
		if field == "" {
			return fmt.Errorf("sort[%d] needs a field", k)
		}
		dir, _ := m["direction"].(string)
		if dir == "" {
			dir = "asc"
		}
		if dir != "asc" && dir != "desc" {
			return fmt.Errorf("sort[%d] direction must be asc or desc", k)
		}
		q.Set(fmt.Sprintf("sort[%d][field]", k), field)
		q.Set(fmt.Sprintf("sort[%d][direction]", k), dir)
	}
	return nil
}

// pending is one record waiting for its batch.
type pending struct {
	item   int
	record map[string]any // create, update, upsert body entry
	id     string         // delete
}

// batched groups consecutive items that share a table into requests of at
// most BatchSize records, and maps every returned record back to its item.
func (r *runner) batched(ctx context.Context, op string, items []nodesdk.Item) (nodesdk.Output, error) {
	var out []nodesdk.Item
	var cur target
	var batch []pending
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		res, err := r.send(ctx, op, cur, batch)
		first := batch[0].item
		batch = nil
		if err != nil {
			return nodekit.ItemFailure(first, err)
		}
		out = append(out, res...)
		return nil
	}
	for i := range items {
		t, err := r.target(ctx, i)
		if err != nil {
			return nodesdk.Output{}, nodekit.ItemFailure(i, err)
		}
		p, err := r.pending(ctx, op, i, items[i])
		if err != nil {
			return nodesdk.Output{}, nodekit.ItemFailure(i, err)
		}
		if len(batch) > 0 && (t != cur || len(batch) >= r.cfg.BatchSize) {
			if err := flush(); err != nil {
				return nodesdk.Output{}, err
			}
		}
		cur = t
		batch = append(batch, p)
	}
	if err := flush(); err != nil {
		return nodesdk.Output{}, err
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

func (r *runner) pending(ctx context.Context, op string, i int, item nodesdk.Item) (pending, error) {
	if op == opDelete {
		id, err := r.recordID(ctx, i)
		return pending{item: i, id: id}, err
	}
	fields, err := objectParam(ctx, r.in, "fields", i)
	if err != nil {
		return pending{}, err
	}
	if fields == nil {
		fields = item.JSON
	}
	rec := map[string]any{"fields": fields}
	if op == opUpdate {
		id, err := r.recordID(ctx, i)
		if err != nil {
			return pending{}, err
		}
		rec["id"] = id
	}
	return pending{item: i, record: rec}, nil
}

// send performs one batch request and pairs each result with its item.
func (r *runner) send(ctx context.Context, op string, t target, batch []pending) ([]nodesdk.Item, error) {
	first := batch[0].item
	var body map[string]any
	var err error
	switch op {
	case opDelete:
		q := url.Values{}
		for _, p := range batch {
			q.Add("records[]", p.id)
		}
		body, err = r.c.call(ctx, http.MethodDelete, t.path(), q, nil)
	default:
		payload := map[string]any{"records": records(batch)}
		typecast, perr := nodesdk.ParamBool(ctx, r.in, "typecast", first)
		if perr != nil {
			return nil, perr
		}
		if typecast {
			payload["typecast"] = true
		}
		method := http.MethodPost
		switch op {
		case opUpdate:
			method = http.MethodPatch
		case opUpsert:
			method = http.MethodPatch
			merge, merr := nodekit.ParamStringList(ctx, r.in, "mergeOn", first)
			if merr != nil {
				return nil, merr
			}
			if len(merge) == 0 {
				return nil, errors.New("mergeOn needs at least one field name")
			}
			payload["performUpsert"] = map[string]any{"fieldsToMergeOn": merge}
		}
		body, err = r.c.call(ctx, method, t.path(), nil, payload)
	}
	if err != nil {
		return nil, err
	}
	got, _ := body["records"].([]any)
	if len(got) != len(batch) {
		return nil, &nodesdk.NodeError{Message: fmt.Sprintf("airtable: sent %d records, got %d back", len(batch), len(got))}
	}
	created := map[string]bool{}
	if ids, ok := body["createdRecords"].([]any); ok {
		for _, id := range ids {
			if s, ok := id.(string); ok {
				created[s] = true
			}
		}
	}
	out := make([]nodesdk.Item, len(batch))
	for k, g := range got {
		rec, _ := g.(map[string]any)
		if rec == nil {
			return nil, &nodesdk.NodeError{Message: "airtable: unexpected record in response"}
		}
		if op == opUpsert {
			id, _ := rec["id"].(string)
			rec["created"] = created[id]
		}
		out[k] = nodekit.Paired(nodesdk.Item{JSON: rec}, batch[k].item)
	}
	return out, nil
}

func records(batch []pending) []any {
	out := make([]any, len(batch))
	for k, p := range batch {
		out[k] = p.record
	}
	return out
}

// objectParam reads an object given as a map or as JSON text. Empty is nil.
func objectParam(ctx context.Context, in nodesdk.Input, name string, i int) (map[string]any, error) {
	v, err := in.Param(ctx, name, i)
	if err != nil {
		return nil, fmt.Errorf("reading parameter %q: %w", name, err)
	}
	switch t := v.(type) {
	case nil:
		return nil, nil
	case map[string]any:
		return t, nil
	case string:
		if strings.TrimSpace(t) == "" {
			return nil, nil
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(t), &m); err != nil {
			return nil, fmt.Errorf("%s is not a JSON object: %w", name, err)
		}
		return m, nil
	}
	return nil, fmt.Errorf("%s must be an object, got %s", name, nodekit.TypeName(v))
}
