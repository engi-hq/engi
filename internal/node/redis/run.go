package redis

import (
	"context"
	"errors"
	"fmt"
	"sort"

	goredis "github.com/redis/go-redis/v9"
)

// scanBatch is the COUNT hint of SCAN; it only tunes round trips.
const scanBatch = 500

// run executes one request and returns the item JSON.
func run(ctx context.Context, rdb goredis.Cmdable, r request, maxKeys int) (map[string]any, error) {
	switch r.op {
	case opGet:
		v, err := rdb.Get(ctx, r.key).Result()
		return valueResult(r.key, v, err)
	case opSet:
		if err := rdb.Set(ctx, r.key, r.value, r.ttl).Err(); err != nil {
			return nil, fmt.Errorf("set: %w", err)
		}
		return map[string]any{"key": r.key, "ok": true}, nil
	case opDelete:
		n, err := rdb.Del(ctx, r.key).Result()
		return count("deleted", r.key, n, err)
	case opIncr, opDecr:
		amount := r.amount
		if r.op == opDecr {
			amount = -amount
		}
		n, err := rdb.IncrBy(ctx, r.key, amount).Result()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", r.op, err)
		}
		return map[string]any{"key": r.key, "value": float64(n)}, nil
	case opKeys:
		return scan(ctx, rdb, r.pattern, maxKeys)
	case opHashGet:
		return hashGet(ctx, rdb, r)
	case opHashSet:
		return hashSet(ctx, rdb, r)
	case opListPush:
		var n int64
		var err error
		if r.right {
			n, err = rdb.RPush(ctx, r.key, r.value).Result()
		} else {
			n, err = rdb.LPush(ctx, r.key, r.value).Result()
		}
		return count("length", r.key, n, err)
	case opListPop:
		var v string
		var err error
		if r.right {
			v, err = rdb.RPop(ctx, r.key).Result()
		} else {
			v, err = rdb.LPop(ctx, r.key).Result()
		}
		return valueResult(r.key, v, err)
	case opPublish:
		n, err := rdb.Publish(ctx, r.channel, r.value).Result()
		if err != nil {
			return nil, fmt.Errorf("publish: %w", err)
		}
		return map[string]any{"channel": r.channel, "receivers": float64(n)}, nil
	}
	return nil, fmt.Errorf("unknown operation %q", r.op)
}

// valueResult maps a string reply; a missing key or empty list yields null.
func valueResult(key, v string, err error) (map[string]any, error) {
	switch {
	case errors.Is(err, goredis.Nil):
		return map[string]any{"key": key, "value": nil}, nil
	case err != nil:
		return nil, fmt.Errorf("reading %q: %w", key, err)
	}
	return map[string]any{"key": key, "value": v}, nil
}

func count(name, key string, n int64, err error) (map[string]any, error) {
	if err != nil {
		return nil, fmt.Errorf("%s %q: %w", name, key, err)
	}
	return map[string]any{"key": key, name: float64(n)}, nil
}

func hashGet(ctx context.Context, rdb goredis.Cmdable, r request) (map[string]any, error) {
	if r.field != "" {
		v, err := rdb.HGet(ctx, r.key, r.field).Result()
		res, err := valueResult(r.key, v, err)
		if res != nil {
			res["field"] = r.field
		}
		return res, err
	}
	all, err := rdb.HGetAll(ctx, r.key).Result()
	if err != nil {
		return nil, fmt.Errorf("reading hash %q: %w", r.key, err)
	}
	m := make(map[string]any, len(all))
	for k, v := range all {
		m[k] = v
	}
	return map[string]any{"key": r.key, "value": m}, nil
}

func hashSet(ctx context.Context, rdb goredis.Cmdable, r request) (map[string]any, error) {
	names := make([]string, 0, len(r.fields))
	for k := range r.fields {
		names = append(names, k)
	}
	sort.Strings(names)
	args := make([]any, 0, 2*len(names))
	for _, k := range names {
		args = append(args, k, hashValue(r.fields[k]))
	}
	if err := rdb.HSet(ctx, r.key, args...).Err(); err != nil {
		return nil, fmt.Errorf("writing hash %q: %w", r.key, err)
	}
	return map[string]any{"key": r.key, "fieldsSet": float64(len(names))}, nil
}

// scan collects up to maxKeys keys matching pattern and flags truncation.
func scan(ctx context.Context, rdb goredis.Cmdable, pattern string, maxKeys int) (map[string]any, error) {
	keys := []any{}
	truncated := false
	it := rdb.Scan(ctx, 0, pattern, scanBatch).Iterator()
	for it.Next(ctx) {
		if len(keys) >= maxKeys {
			truncated = true
			break
		}
		keys = append(keys, it.Val())
	}
	if err := it.Err(); err != nil {
		return nil, fmt.Errorf("listing keys: %w", err)
	}
	return map[string]any{"pattern": pattern, "keys": keys, "truncated": truncated}, nil
}
