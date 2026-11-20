package api

import (
	"context"
	"net/http"
	"slices"
	"strings"
)

// access says who may call a route.
type access int

const (
	// accessPublic needs no credentials.
	accessPublic access = iota
	// accessUser needs a session or an API key.
	accessUser
)

// handlerFunc is a handler that returns its failure; the router turns it into a
// problem+json response in one place.
type handlerFunc func(http.ResponseWriter, *http.Request) error

type route struct {
	method string
	path   string
	access access
	// stream routes are long-lived: no request deadline and no write deadline.
	stream bool
	h      handlerFunc
}

func (r route) pattern() string { return r.method + " " + r.path }

func v1(path string) string { return BasePath + path }

// routeTable lists every route. It is the single source the router and the
// OpenAPI conformance test read.
func (a *API) routeTable() []route {
	const (
		get    = http.MethodGet
		post   = http.MethodPost
		put    = http.MethodPut
		patch  = http.MethodPatch
		del    = http.MethodDelete
		public = accessPublic
		user   = accessUser
	)
	return []route{
		{get, HealthPath, public, false, a.health},
		{get, ReadyPath, public, false, a.ready},

		{get, v1("/auth/setup"), public, false, a.setupStatus},
		{post, v1("/auth/setup"), public, false, a.setup},
		{post, v1("/auth/login"), public, false, a.login},
		{post, v1("/auth/logout"), public, false, a.logout},
		{get, v1("/me"), user, false, a.me},

		{get, v1("/users"), user, false, a.listUsers},
		{post, v1("/users"), user, false, a.createUser},
		{get, v1("/users/{id}"), user, false, a.getUser},
		{patch, v1("/users/{id}"), user, false, a.updateUser},
		{del, v1("/users/{id}"), user, false, a.deleteUser},

		{get, v1("/api-keys"), user, false, a.listAPIKeys},
		{post, v1("/api-keys"), user, false, a.createAPIKey},
		{del, v1("/api-keys/{id}"), user, false, a.revokeAPIKey},

		{get, v1("/workflows"), user, false, a.listWorkflows},
		{post, v1("/workflows"), user, false, a.createWorkflow},
		{post, v1("/workflows/import"), user, false, a.importWorkflow},
		{get, v1("/workflows/{id}"), user, false, a.getWorkflow},
		{put, v1("/workflows/{id}"), user, false, a.updateWorkflow},
		{del, v1("/workflows/{id}"), user, false, a.deleteWorkflow},
		{get, v1("/workflows/{id}/export"), user, false, a.exportWorkflow},
		{get, v1("/workflows/{id}/versions"), user, false, a.listVersions},
		{get, v1("/workflows/{id}/versions/{version}"), user, false, a.getVersion},
		{post, v1("/workflows/{id}/versions/{version}/restore"), user, false, a.restoreVersion},
		{post, v1("/workflows/{id}/activate"), user, false, a.activateWorkflow},
		{post, v1("/workflows/{id}/deactivate"), user, false, a.deactivateWorkflow},
		{post, v1("/workflows/{id}/test-webhook"), user, false, a.armTestWebhook},
		{post, v1("/workflows/{id}/run"), user, false, a.runWorkflow},

		{get, v1("/tags"), user, false, a.listTags},
		{post, v1("/tags"), user, false, a.createTag},
		{patch, v1("/tags/{id}"), user, false, a.updateTag},
		{del, v1("/tags/{id}"), user, false, a.deleteTag},

		{get, v1("/variables"), user, false, a.listVariables},
		{post, v1("/variables"), user, false, a.createVariable},
		{patch, v1("/variables/{id}"), user, false, a.updateVariable},
		{del, v1("/variables/{id}"), user, false, a.deleteVariable},

		{post, v1("/expressions/preview"), user, false, a.previewExpression},

		{get, v1("/executions"), user, false, a.listExecutions},
		{get, v1("/executions/{id}"), user, false, a.getExecution},
		{del, v1("/executions/{id}"), user, false, a.deleteExecution},
		{post, v1("/executions/{id}/cancel"), user, false, a.cancelExecution},
		{post, v1("/executions/{id}/retry"), user, false, a.retryExecution},
		{get, v1("/executions/{id}/stream"), user, true, a.streamExecution},
		{get, v1("/executions/{id}/binary/{ref}"), user, true, a.getBinary},

		{get, v1("/credentials"), user, false, a.listCredentials},
		{post, v1("/credentials"), user, false, a.createCredential},
		{get, v1("/credentials/{id}"), user, false, a.getCredential},
		{put, v1("/credentials/{id}"), user, false, a.updateCredential},
		{del, v1("/credentials/{id}"), user, false, a.deleteCredential},
		{post, v1("/credentials/{id}/test"), user, false, a.testCredential},
		{post, v1("/credentials/{id}/authorize"), user, false, a.authorizeCredential},
		{get, v1("/credential-types"), user, false, a.listCredentialTypes},
		{get, v1("/credential-types/{name}"), user, false, a.getCredentialType},

		{get, v1("/node-types"), user, false, a.listNodeTypes},
		{get, v1("/node-types/{type}"), user, false, a.getNodeType},
		{get, v1("/plugins"), user, false, a.listPlugins},

		{get, OAuth2CallbackPath, public, false, a.oauth2Callback},
	}
}

// build registers the routes and wraps them in the middleware chain, outermost
// first: request id, access log, recovery, CORS, body limit.
func (a *API) build(routes []route) http.Handler {
	mux := http.NewServeMux()
	allowed := map[string][]string{}
	for _, rt := range routes {
		mux.Handle(rt.pattern(), a.routeHandler(rt))
		allowed[rt.path] = append(allowed[rt.path], rt.method)
	}
	// A path that exists under other methods answers 405 with Allow; anything
	// else is a 404. Both as problem documents.
	paths := http.NewServeMux()
	for path, methods := range allowed {
		allow := strings.Join(append(slices.Sorted(slices.Values(methods)), http.MethodOptions), ", ")
		paths.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Allow", allow)
			a.fail(w, r, &requestError{status: http.StatusMethodNotAllowed, detail: "method " + r.Method + " is not allowed on " + r.URL.Path})
		})
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if h, pattern := paths.Handler(r); pattern != "" {
			h.ServeHTTP(w, r)
			return
		}
		a.fail(w, r, &requestError{status: http.StatusNotFound, detail: "no such route " + r.URL.Path})
	})
	var h http.Handler = mux
	h = a.bodyLimit(h)
	h = a.cors(h)
	h = a.recoverer(h)
	h = a.accessLog(h)
	h = a.requestID(h)
	return h
}

// routeHandler adds the per-route concerns: deadline, authentication, CSRF and
// error rendering.
func (a *API) routeHandler(rt route) http.Handler {
	h := rt.h
	if rt.access == accessUser {
		h = a.authenticated(h)
	}
	pattern := rt.pattern()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if info, ok := r.Context().Value(ctxInfo).(*reqInfo); ok {
			info.route = pattern
		}
		if !rt.stream && a.d.Server.RequestTimeout > 0 {
			ctx, cancel := context.WithTimeout(r.Context(), a.d.Server.RequestTimeout)
			defer cancel()
			r = r.WithContext(ctx)
		}
		if err := h(w, r); err != nil {
			a.fail(w, r, err)
		}
	})
}
