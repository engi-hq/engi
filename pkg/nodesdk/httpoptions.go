package nodesdk

import "context"

// HTTPCallOptions adjusts one request made through Input.HTTP(). They travel in
// the request context so the HTTPClient interface stays a plain Do.
type HTTPCallOptions struct {
	// NoRedirect returns the first redirect response instead of following it.
	NoRedirect bool
}

type httpCallOptionsKey struct{}

// WithHTTPCallOptions returns a context carrying per-request HTTP options.
func WithHTTPCallOptions(ctx context.Context, o HTTPCallOptions) context.Context {
	return context.WithValue(ctx, httpCallOptionsKey{}, o)
}

// HTTPCallOptionsFrom returns the options set by WithHTTPCallOptions, or the
// zero value.
func HTTPCallOptionsFrom(ctx context.Context) HTTPCallOptions {
	o, _ := ctx.Value(httpCallOptionsKey{}).(HTTPCallOptions)
	return o
}
