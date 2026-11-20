package kafka

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// The saved parameters of a listener are not evaluated, so they are read from
// the raw map with the descriptor defaults applied here.

func paramString(p map[string]any, name, def string) string {
	if s, ok := p[name].(string); ok && s != "" {
		return s
	}
	return def
}

func paramInt(p map[string]any, name string, def int) (int, error) {
	switch v := p[name].(type) {
	case nil:
		return def, nil
	case float64:
		if v != math.Trunc(v) {
			return 0, fmt.Errorf("%s must be a whole number", name)
		}
		return int(v), nil
	case int:
		return v, nil
	case string:
		if v == "" {
			return def, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("%s %q is not a number", name, v)
		}
		return n, nil
	}
	return 0, fmt.Errorf("%s must be a number", name)
}

// splitList accepts "a, b" or ["a", "b"].
func splitList(v any) []string {
	var raw []string
	switch t := v.(type) {
	case string:
		raw = strings.Split(t, ",")
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok {
				raw = append(raw, s)
			}
		}
	}
	var out []string
	for _, s := range raw {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
