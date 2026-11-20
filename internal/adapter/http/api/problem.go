package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/service/credential"
	"github.com/engi-hq/engi/internal/service/execution"
)

// problemContentType is the media type of every error document (RFC 9457).
const problemContentType = "application/problem+json"

// bearerChallenge is sent with 401 responses.
const bearerChallenge = //nolint:gosec // G101: an HTTP challenge, not a credential
`Bearer realm="engi"`

// requestError is a client mistake detected by the adapter itself (bad JSON,
// unknown query parameter, wrong media type, ...).
type requestError struct {
	status int
	detail string
	issues []issueDTO
}

func (e *requestError) Error() string { return e.detail }

func badRequest(format string, args ...any) *requestError {
	return &requestError{status: http.StatusBadRequest, detail: fmt.Sprintf(format, args...)}
}

// errPanic is what a recovered panic is reported as; the cause is only logged.
var errPanic = errors.New("internal error")

// issueDTO is one entry of a problem's `errors` array.
type issueDTO struct {
	Pointer string `json:"pointer"`
	Message string `json:"message"`
}

// problemDTO is an RFC 9457 problem document.
type problemDTO struct {
	Type      string     `json:"type"`
	Title     string     `json:"title"`
	Status    int        `json:"status"`
	Detail    string     `json:"detail,omitempty"`
	Instance  string     `json:"instance,omitempty"`
	Errors    []issueDTO `json:"errors,omitempty"`
	RequestID string     `json:"requestId,omitempty"`
}

// verdict is how one domain error is presented: status, a stable slug for the
// problem type, the detail text and optional extras.
type verdict struct {
	status     int
	slug       string
	detail     string
	issues     []issueDTO
	retryAfter time.Duration
	challenge  bool
}

// classify is the single place where errors become HTTP statuses (docs/09
// section 9.4). Unknown errors are internal: the caller sees no details.
func classify(err error) verdict {
	var (
		req  *requestError
		val  *domain.ValidationError
		rate *domain.RateLimitedError
		expr *domain.ExpressionError
		mbe  *http.MaxBytesError
	)
	switch {
	case errors.As(err, &req):
		return verdict{status: req.status, slug: slugFor(req.status), detail: req.detail, issues: req.issues}
	case errors.As(err, &mbe):
		return verdict{status: http.StatusRequestEntityTooLarge, slug: "payload-too-large",
			detail: "request body exceeds " + strconv.FormatInt(mbe.Limit, 10) + " bytes"}
	case errors.As(err, &val):
		return verdict{status: http.StatusUnprocessableEntity, slug: "validation", detail: firstIssue(val), issues: issuesOf(val)}
	case errors.As(err, &expr):
		return verdict{status: http.StatusUnprocessableEntity, slug: "expression-error", detail: expr.Message}
	case errors.As(err, &rate):
		return verdict{status: http.StatusTooManyRequests, slug: "rate-limited", detail: "too many attempts", retryAfter: rate.RetryAfter}
	case errors.Is(err, domain.ErrUnauthenticated):
		return verdict{status: http.StatusUnauthorized, slug: "unauthenticated", detail: "authentication required or credentials invalid", challenge: true}
	case errors.Is(err, domain.ErrForbidden):
		return verdict{status: http.StatusForbidden, slug: "forbidden", detail: err.Error()}
	case errors.Is(err, domain.ErrNotFound):
		return verdict{status: http.StatusNotFound, slug: "not-found", detail: err.Error()}
	case errors.Is(err, domain.ErrInvalidCursor):
		return verdict{status: http.StatusBadRequest, slug: "bad-request", detail: "invalid cursor"}
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, domain.ErrDuplicate),
		errors.Is(err, domain.ErrSetupDone), errors.Is(err, domain.ErrNeedsReauth),
		errors.Is(err, execution.ErrNotCancelable), errors.Is(err, execution.ErrNotRetryable), errors.Is(err, execution.ErrNotDeletable):
		return verdict{status: http.StatusConflict, slug: "conflict", detail: err.Error()}
	case errors.Is(err, domain.ErrNoTest):
		return verdict{status: http.StatusUnprocessableEntity, slug: "not-testable", detail: "this credential type has no test"}
	case errors.Is(err, credential.ErrInvalidState):
		return verdict{status: http.StatusBadRequest, slug: "bad-request", detail: "the authorization state is invalid or has expired"}
	case errors.Is(err, ErrNotImplemented):
		return verdict{status: http.StatusNotImplemented, slug: "not-implemented", detail: err.Error()}
	case errors.Is(err, context.DeadlineExceeded):
		return verdict{status: http.StatusServiceUnavailable, slug: "timeout", detail: "the request took too long"}
	}
	return verdict{status: http.StatusInternalServerError, slug: "internal", detail: "internal error"}
}

func slugFor(status int) string {
	return strings.ReplaceAll(strings.ToLower(http.StatusText(status)), " ", "-")
}

func firstIssue(v *domain.ValidationError) string {
	if len(v.Issues) == 0 {
		return "validation failed"
	}
	if len(v.Issues) == 1 {
		return v.Issues[0].Path + ": " + v.Issues[0].Message
	}
	return v.Issues[0].Path + ": " + v.Issues[0].Message + " (and " + strconv.Itoa(len(v.Issues)-1) + " more)"
}

var indexSegment = regexp.MustCompile(`\[(\d+)\]`)

// pointerOf turns a domain path such as "nodes[2].name" into the JSON pointer
// "/nodes/2/name". Name-keyed segments ("nodes[Fetch]") are kept verbatim.
func pointerOf(path string) string {
	p := indexSegment.ReplaceAllString(path, ".$1")
	p = strings.ReplaceAll(p, "[", ".")
	p = strings.ReplaceAll(p, "]", "")
	p = strings.ReplaceAll(p, ".", "/")
	return "/" + strings.TrimPrefix(p, "/")
}

func issuesOf(v *domain.ValidationError) []issueDTO {
	out := make([]issueDTO, len(v.Issues))
	for i, is := range v.Issues {
		out[i] = issueDTO{Pointer: pointerOf(is.Path), Message: is.Message}
	}
	return out
}

// fail writes the problem document for err. Server errors are logged here, once,
// with the cause; the response carries only the request ID.
func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	v := classify(err)
	if v.status >= http.StatusInternalServerError && v.status != http.StatusNotImplemented && v.status != http.StatusServiceUnavailable {
		a.d.Logger.ErrorContext(r.Context(), "request failed", "error", err, "method", r.Method, "path", r.URL.Path,
			"requestId", requestIDFrom(r.Context()))
	}
	p := problemDTO{
		Type: a.d.Server.ProblemTypeBase + v.slug, Title: http.StatusText(v.status), Status: v.status,
		Detail: v.detail, Instance: r.URL.Path, Errors: v.issues, RequestID: requestIDFrom(r.Context()),
	}
	body, mErr := json.Marshal(p)
	if mErr != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", problemContentType)
	if v.challenge {
		h.Set("WWW-Authenticate", bearerChallenge)
	}
	if v.retryAfter > 0 {
		h.Set("Retry-After", strconv.Itoa(int((v.retryAfter+time.Second-1)/time.Second)))
	}
	w.WriteHeader(v.status)
	_, _ = w.Write(body)
}
