package api

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/engi-hq/engi/internal/domain"
)

// requestIDHeader carries the request ID in both directions.
const requestIDHeader = "X-Request-ID"

// maxRequestIDLength bounds a client-supplied request ID.
const maxRequestIDLength = 128

type ctxKey int

const (
	ctxRequestID ctxKey = iota
	ctxPrincipal
	ctxInfo
)

// reqInfo lets inner handlers report facts (the caller) back to the access log,
// which sits outside them.
type reqInfo struct {
	user  domain.UserID
	route string // the matched route pattern, for metrics
}

// unmatchedRoute is the metrics label of a request no route matched.
const unmatchedRoute = "unmatched"

func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(ctxRequestID).(string)
	return id
}

// validRequestID accepts a client-supplied ID only when it is short and made of
// safe characters, so it can go into logs and headers unescaped.
func validRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDLength {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return true
}

func (a *API) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if !validRequestID(id) {
			var err error
			if id, err = a.d.IDs.New(requestIDPrefix); err != nil {
				id = strconv.FormatInt(time.Now().UnixNano(), 36)
			}
		}
		w.Header().Set(requestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxRequestID, id)))
	})
}

// recorder captures what a handler wrote for the access log. It passes Flush and
// ResponseController calls through, which streaming needs.
type recorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (r *recorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err //nolint:wrapcheck // transparent writer
}

func (r *recorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *recorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijacking is not supported")
	}
	return h.Hijack() //nolint:wrapcheck // transparent writer
}

func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// accessLog writes one structured line per request. The query string is left
// out on purpose: the OAuth2 callback carries a code and a state in it.
func (a *API) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &recorder{ResponseWriter: w}
		info := &reqInfo{}
		next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), ctxInfo, info)))
		route := info.route
		if route == "" {
			route = unmatchedRoute
		}
		if a.d.Metrics != nil {
			a.d.Metrics.HTTPRequest(route, rec.status, time.Since(start))
		}
		attrs := []any{
			"method", r.Method, "path", r.URL.Path, "status", rec.status, "bytes", rec.bytes,
			"duration", time.Since(start).Round(time.Microsecond).String(),
			"requestId", requestIDFrom(r.Context()), "remote", a.clientAddr(r),
		}
		if info.user != "" {
			attrs = append(attrs, "user", info.user)
		}
		level := a.d.Logger.InfoContext
		if rec.status >= http.StatusInternalServerError {
			level = a.d.Logger.ErrorContext
		}
		level(r.Context(), "http request", attrs...)
	})
}

// recoverer turns a panic in a handler into a 500 problem and a logged stack.
func (a *API) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer a.recoverPanic(w, r)
		next.ServeHTTP(w, r)
	})
}

// recoverPanic must be deferred directly: recover only works there.
func (a *API) recoverPanic(w http.ResponseWriter, r *http.Request) {
	v := recover()
	if v == nil {
		return
	}
	if v == http.ErrAbortHandler { //nolint:errorlint // ErrAbortHandler is a sentinel panic value
		panic(v)
	}
	a.d.Logger.ErrorContext(r.Context(), "panic in http handler",
		"panic", v, "stack", string(debug.Stack()), "requestId", requestIDFrom(r.Context()))
	if rec, ok := w.(*recorder); !ok || rec.status == 0 {
		a.fail(w, r, errPanic)
	}
}

// bodyLimit caps request bodies; reading past the cap fails with
// *http.MaxBytesError, which becomes a 413 problem.
func (a *API) bodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Body != http.NoBody {
			r.Body = http.MaxBytesReader(w, r.Body, a.d.Server.MaxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// cors implements the origin allow-list. With no configured origins it does
// nothing. Preflights are answered here, before routing and authentication.
func (a *API) cors(next http.Handler) http.Handler {
	c := a.d.Server.CORS
	if len(c.AllowedOrigins) == 0 {
		return next
	}
	wildcard := slices.Contains(c.AllowedOrigins, "*")
	methods := strings.Join(c.AllowedMethods, ", ")
	headers := strings.Join(c.AllowedHeaders, ", ")
	maxAge := strconv.Itoa(int(c.MaxAge.Seconds()))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Origin")
		if !wildcard && !slices.Contains(c.AllowedOrigins, origin) {
			next.ServeHTTP(w, r)
			return
		}
		if wildcard {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}
		if c.AllowCredentials {
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			w.Header().Add("Vary", "Access-Control-Request-Method")
			w.Header().Set("Access-Control-Allow-Methods", methods)
			w.Header().Set("Access-Control-Allow-Headers", headers)
			if c.MaxAge > 0 {
				w.Header().Set("Access-Control-Max-Age", maxAge)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Access-Control-Expose-Headers", requestIDHeader+", ETag, Location, Retry-After")
		next.ServeHTTP(w, r)
	})
}

// clientAddr is the caller's address. Behind a trusted proxy the last
// X-Forwarded-For entry is the one that proxy appended; earlier entries are
// client-controlled and ignored.
func (a *API) clientAddr(r *http.Request) string {
	if a.d.Server.TrustForwardedFor {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if last := strings.TrimSpace(parts[len(parts)-1]); last != "" {
				return last
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
