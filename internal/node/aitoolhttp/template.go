package aitoolhttp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/engi-hq/engi/internal/node/nodekit"
)

// placeholder matches {name}. It cannot match JSON braces, which are followed
// by a quote, a bracket or whitespace.
var placeholder = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// placeholders lists the distinct names used in s, sorted.
func placeholders(s string) []string {
	seen := map[string]struct{}{}
	for _, m := range placeholder.FindAllStringSubmatch(s, -1) {
		seen[m[1]] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// text renders an argument as text: strings as is, everything else as JSON.
func text(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nodekit.ToString(v)
	}
	return string(b)
}

// interpolate replaces each {name} with escape(text(args[name])). A name the
// model did not supply becomes empty.
func interpolate(s string, args map[string]any, escape func(string) string) string {
	return placeholder.ReplaceAllStringFunc(s, func(m string) string {
		v, ok := args[m[1:len(m)-1]]
		if !ok || v == nil {
			return ""
		}
		return escape(text(v))
	})
}

func verbatim(s string) string { return s }

// expandURL substitutes into the part of the URL after scheme and authority;
// the model may not choose the host. Path values are path-escaped, query
// values query-escaped.
func expandURL(tmpl string, args map[string]any) (string, error) {
	u, err := url.Parse(strings.ReplaceAll(strings.ReplaceAll(tmpl, "{", "_"), "}", "_"))
	if err != nil {
		return "", fmt.Errorf("invalid url %q: %w", tmpl, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("url %q must use http or https", tmpl)
	}
	if u.Host == "" {
		return "", fmt.Errorf("url %q has no host", tmpl)
	}
	authorityEnd := strings.Index(tmpl, "://") + len("://")
	if rest := tmpl[authorityEnd:]; rest != "" {
		end := strings.IndexAny(rest, "/?#")
		if end < 0 {
			end = len(rest)
		}
		if placeholder.MatchString(rest[:end]) {
			return "", fmt.Errorf("url %q: placeholders are not allowed in the scheme or host", tmpl)
		}
		authorityEnd += end
	}
	head, tail := tmpl[:authorityEnd], tmpl[authorityEnd:]
	path, query, hasQuery := strings.Cut(tail, "?")
	out := head + interpolate(path, args, url.PathEscape)
	if hasQuery {
		out += "?" + interpolate(query, args, url.QueryEscape)
	}
	return out, nil
}

// expandJSON substitutes into a JSON template. A string that is exactly
// {name} becomes the typed argument; other strings are interpolated.
func expandJSON(tmpl any, args map[string]any) any {
	switch t := tmpl.(type) {
	case string:
		if m := placeholder.FindStringSubmatch(t); len(m) > 0 && m[0] == t {
			return args[m[1]]
		}
		return interpolate(t, args, verbatim)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, v := range t {
			out[interpolate(k, args, verbatim)] = expandJSON(v, args)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, v := range t {
			out[i] = expandJSON(v, args)
		}
		return out
	}
	return tmpl
}
