package domain

import (
	"strings"
	"time"
)

// pathParamPrefix marks a dynamic path segment: "orders/:id".
const pathParamPrefix = ":"

// WebhookRegistration routes HTTP calls to the webhook trigger node of an
// active workflow. Method and Shape are unique across all registrations.
type WebhookRegistration struct {
	// Method is the upper-case HTTP method.
	Method string
	// Path is the route pattern without leading or trailing slash. A segment
	// starting with ":" captures that part of the request path.
	Path       string
	WorkflowID WorkflowID
	// Node is the name of the webhook trigger node.
	Node      string
	CreatedAt time.Time
}

// NormalizeWebhookPath trims slashes around a request or pattern path.
func NormalizeWebhookPath(p string) string { return strings.Trim(p, "/") }

// Segments returns the number of path segments.
func (r WebhookRegistration) Segments() int { return len(strings.Split(r.Path, "/")) }

// Dynamic reports whether the pattern has parameter segments.
func (r WebhookRegistration) Dynamic() bool { return strings.Contains(r.Shape(), pathParamPrefix) }

// Shape is the pattern with parameter names replaced by ":", so that
// "orders/:id" and "orders/:order" are the same route.
func (r WebhookRegistration) Shape() string {
	parts := strings.Split(r.Path, "/")
	for i, p := range parts {
		if strings.HasPrefix(p, pathParamPrefix) {
			parts[i] = pathParamPrefix
		}
	}
	return strings.Join(parts, "/")
}

// Match reports whether path (already normalized) is served by the pattern and
// returns the captured parameters.
func (r WebhookRegistration) Match(path string) (map[string]string, bool) {
	pat, got := strings.Split(r.Path, "/"), strings.Split(path, "/")
	if len(pat) != len(got) {
		return nil, false
	}
	params := map[string]string{}
	for i, p := range pat {
		switch {
		case strings.HasPrefix(p, pathParamPrefix) && len(p) > 1:
			if got[i] == "" {
				return nil, false
			}
			params[p[1:]] = got[i]
		case p != got[i]:
			return nil, false
		}
	}
	return params, true
}

// ArmedTestWebhook is a test webhook waiting for one call on the test URL. It
// is shared by every process, so any webhook pod can serve the call. Method
// and Shape are unique among armed tests: arming again replaces the earlier one.
type ArmedTestWebhook struct {
	WorkflowID WorkflowID
	// Node is the name of the webhook trigger node the call starts.
	Node string
	// Method is the upper-case HTTP method.
	Method string
	// Path is the route pattern as written, without leading or trailing slash.
	Path string
	// ExpiresAt is the first instant the test no longer answers.
	ExpiresAt time.Time
}

// Route returns the registration view of the test's pattern, for matching.
func (a ArmedTestWebhook) Route() WebhookRegistration {
	return WebhookRegistration{Method: a.Method, Path: a.Path, WorkflowID: a.WorkflowID, Node: a.Node}
}

// Shape is the pattern with parameter names replaced by ":".
func (a ArmedTestWebhook) Shape() string { return a.Route().Shape() }
