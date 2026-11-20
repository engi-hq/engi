package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

const jsonContentType = "application/json"

// writeJSON encodes v before writing anything, so an encoding failure can still
// become a clean 500.
func (a *API) writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encoding response: %w", err)
	}
	w.Header().Set("Content-Type", jsonContentType)
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		a.d.Logger.DebugContext(r.Context(), "writing response failed", "error", err, "requestId", requestIDFrom(r.Context()))
	}
	return nil
}

// readBody reads the (size-limited) request body.
func readBody(r *http.Request) ([]byte, error) {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, err //nolint:wrapcheck // classified as 413
		}
		return nil, badRequest("reading the request body failed")
	}
	return b, nil
}

// requireJSON checks the Content-Type of requests that carry a body.
func requireJSON(r *http.Request) error {
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		return &requestError{status: http.StatusUnsupportedMediaType, detail: "Content-Type must be application/json"}
	}
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil || (mt != jsonContentType && !strings.HasSuffix(mt, "+json")) {
		return &requestError{status: http.StatusUnsupportedMediaType, detail: "Content-Type must be application/json"}
	}
	return nil
}

// decodeJSON reads one JSON document into dst. Unknown fields are rejected so
// typos do not silently do nothing. An empty body is an error unless
// allowEmpty is set.
func decodeJSON(r *http.Request, dst any, allowEmpty bool) error {
	body, err := readBody(r)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		if allowEmpty {
			return nil
		}
		return badRequest("the request body is empty")
	}
	if err := requireJSON(r); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return badRequest("invalid JSON body: %v", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return badRequest("invalid JSON body: unexpected data after the document")
	}
	return nil
}

// allowParams rejects query parameters the route does not know (docs/09 9.3).
func allowParams(q url.Values, allowed ...string) error {
	for k := range q {
		if !slices.Contains(allowed, k) {
			return badRequest("unknown query parameter %q", k)
		}
	}
	return nil
}

// pageOf reads limit and cursor. The limit defaults and is capped by the
// configured pagination settings.
func (a *API) pageOf(q url.Values) (port.Page, error) {
	p := port.Page{Cursor: q.Get("cursor"), Limit: a.d.Server.Pagination.DefaultLimit}
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			return port.Page{}, badRequest("limit must be a positive integer")
		}
		p.Limit = min(n, a.d.Server.Pagination.MaxLimit)
	}
	return p, nil
}

// listDTO is the envelope of every list (docs/09 section 9.3).
type listDTO[T any] struct {
	Data       []T    `json:"data"`
	NextCursor string `json:"nextCursor,omitempty"`
	HasMore    bool   `json:"hasMore"`
}

func listOf[In, Out any](res port.PageResult[In], conv func(In) Out) listDTO[Out] {
	out := listDTO[Out]{Data: make([]Out, len(res.Items)), NextCursor: res.NextCursor, HasMore: res.NextCursor != ""}
	for i, it := range res.Items {
		out.Data[i] = conv(it)
	}
	return out
}

// timeParam parses an optional RFC 3339 query parameter.
func timeParam(q url.Values, name string) (time.Time, error) {
	s := q.Get(name)
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, badRequest("%s must be an RFC 3339 timestamp", name)
	}
	return t, nil
}

// boolParam parses an optional boolean query parameter.
func boolParam(q url.Values, name string) (*bool, error) {
	s := q.Get(name)
	if s == "" {
		return nil, nil //nolint:nilnil // absent parameter
	}
	b, err := strconv.ParseBool(s)
	if err != nil {
		return nil, badRequest("%s must be true or false", name)
	}
	return &b, nil
}

// pathInt parses a numeric path segment.
func pathInt(r *http.Request, name string) (int, error) {
	n, err := strconv.Atoi(r.PathValue(name))
	if err != nil || n < 1 {
		return 0, badRequest("%s must be a positive integer", name)
	}
	return n, nil
}

// idParam reads and validates a typed ID path segment. A malformed ID cannot
// exist, so it reads as not found rather than as a server error.
func idParam[T ~string](r *http.Request, name string, validate func(T) error) (T, error) {
	id := T(r.PathValue(name))
	if err := validate(id); err != nil {
		return "", fmt.Errorf("%s %q: %w", name, id, domain.ErrNotFound)
	}
	return id, nil
}
