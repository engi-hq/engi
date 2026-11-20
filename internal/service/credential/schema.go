package credential

import (
	"fmt"
	"slices"
	"strings"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Sentinel replaces secret values in anything returned to callers. Sending it
// back on update means "keep the stored value".
const Sentinel = "__ENGI_SECRET__"

// visible reports whether a field applies given the current values.
func visible(f nodesdk.CredentialField, data map[string]any) bool {
	d := f.DisplayOptions
	if d == nil {
		return true
	}
	matches := func(conds map[string][]any) []bool {
		var out []bool
		for name, vals := range conds {
			cur := data[name]
			out = append(out, slices.ContainsFunc(vals, func(v any) bool { return fmt.Sprint(v) == fmt.Sprint(cur) }))
		}
		return out
	}
	for _, ok := range matches(d.Show) {
		if !ok {
			return false
		}
	}
	return !slices.Contains(matches(d.Hide), true)
}

// normalize validates data against the type schema, applies defaults and
// returns the cleaned map. Managed fields are rejected as input. The
// returned error is a *domain.ValidationError.
func normalize(t nodesdk.CredentialType, in map[string]any) (map[string]any, error) {
	ve := &domain.ValidationError{}
	out := make(map[string]any, len(t.Fields))
	for k := range in {
		f, ok := t.Field(k)
		switch {
		case !ok:
			ve.Issues = append(ve.Issues, domain.Issue{Path: "data." + k, Message: "unknown field"})
		case f.Managed:
			ve.Issues = append(ve.Issues, domain.Issue{Path: "data." + k, Message: "is managed by the system and cannot be set"})
		}
	}
	// Defaults first so displayOptions see them.
	merged := make(map[string]any, len(in))
	for _, f := range t.Fields {
		if f.Managed {
			continue
		}
		if v, ok := in[f.Name]; ok {
			merged[f.Name] = v
		} else if f.Default != nil {
			merged[f.Name] = f.Default
		}
	}
	for _, f := range t.Fields {
		if f.Managed || !visible(f, merged) {
			continue
		}
		v, present := merged[f.Name]
		if !present || v == nil || v == "" {
			if f.Required {
				ve.Issues = append(ve.Issues, domain.Issue{Path: "data." + f.Name, Message: "is required"})
			}
			continue
		}
		if s, ok := v.(string); ok && s == Sentinel {
			ve.Issues = append(ve.Issues, domain.Issue{Path: "data." + f.Name, Message: "has no stored value to keep"})
			continue
		}
		if msg := checkType(f, v); msg != "" {
			ve.Issues = append(ve.Issues, domain.Issue{Path: "data." + f.Name, Message: msg})
			continue
		}
		out[f.Name] = v
	}
	if len(ve.Issues) > 0 {
		return nil, ve
	}
	return out, nil
}

func checkType(f nodesdk.CredentialField, v any) string {
	switch f.Type {
	case nodesdk.PropString, nodesdk.PropDateTime, nodesdk.PropCode:
		if _, ok := v.(string); !ok {
			return "must be a string"
		}
	case nodesdk.PropNumber:
		switch v.(type) {
		case float64, float32, int, int32, int64:
		default:
			return "must be a number"
		}
	case nodesdk.PropBoolean:
		if _, ok := v.(bool); !ok {
			return "must be a boolean"
		}
	case nodesdk.PropOptions:
		if !slices.ContainsFunc(f.Options, func(o nodesdk.Option) bool { return fmt.Sprint(o.Value) == fmt.Sprint(v) }) {
			names := make([]string, len(f.Options))
			for i, o := range f.Options {
				names[i] = fmt.Sprint(o.Value)
			}
			return "must be one of " + strings.Join(names, ", ")
		}
	}
	return ""
}
