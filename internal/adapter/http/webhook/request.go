package webhook

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/trigger"
)

// secretHeaders never reach execution data: they carry credentials.
var secretHeaders = []string{"authorization", "proxy-authorization", "cookie"}

// buildItem reads the request body (bounded by webhook.maxBodyBytes) and builds
// the trigger item {headers, params, query, body}. On failure it returns the
// HTTP status to answer with.
func (h *Handler) buildItem(w http.ResponseWriter, r *http.Request, route trigger.Route, auth authResult, exec domain.ExecutionID) (domain.Item, int, error) {
	body, binary, status, err := h.readBody(w, r, route.Spec.RawBody, exec)
	if err != nil {
		return domain.Item{}, status, err
	}
	params := make(map[string]any, len(route.Params))
	for k, v := range route.Params {
		params[k] = v
	}
	return domain.Item{JSON: map[string]any{
		"headers": headersOf(r.Header, auth.header), "params": params, "query": queryOf(r.URL.Query()), "body": body,
	}, Binary: binary}, http.StatusOK, nil
}

// readBody reads and parses the body by its content type. File parts of a
// multipart form are streamed to the blob store and returned as binary fields.
func (h *Handler) readBody(w http.ResponseWriter, r *http.Request, raw bool, exec domain.ExecutionID) (body any, binary map[string]domain.BinaryRef, status int, err error) {
	if mt, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); !raw && mt == "multipart/form-data" && h.d.Blobs != nil {
		return h.readUploads(r, params["boundary"], exec)
	}
	body, status, err = h.readTextBody(w, r, raw)
	return body, nil, status, err
}

func (h *Handler) readTextBody(w http.ResponseWriter, r *http.Request, raw bool) (body any, status int, err error) {
	if r.ContentLength > h.cfg.MaxBodyBytes {
		return nil, http.StatusRequestEntityTooLarge, fmt.Errorf("body exceeds %d bytes", h.cfg.MaxBodyBytes)
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.cfg.MaxBodyBytes))
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		return nil, http.StatusRequestEntityTooLarge, fmt.Errorf("body exceeds %d bytes", h.cfg.MaxBodyBytes)
	}
	if err != nil {
		return nil, http.StatusBadRequest, errors.New("reading the request body failed")
	}
	if len(data) == 0 {
		if raw {
			return "", http.StatusOK, nil
		}
		return map[string]any{}, http.StatusOK, nil
	}
	if raw {
		return textOf(data), http.StatusOK, nil
	}
	mt, mparams, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch {
	case mt == "application/json" || strings.HasSuffix(mt, "+json"):
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, http.StatusBadRequest, errors.New("the body is not valid JSON")
		}
		return v, http.StatusOK, nil
	case mt == "application/x-www-form-urlencoded":
		vals, err := url.ParseQuery(string(data))
		if err != nil {
			return nil, http.StatusBadRequest, errors.New("the body is not a valid form")
		}
		return queryOf(vals), http.StatusOK, nil
	case mt == "multipart/form-data":
		return parseMultipart(data, mparams["boundary"])
	default:
		return textOf(data), http.StatusOK, nil
	}
}

// parseMultipart reads the text fields of a multipart form when no blob store
// is configured; file parts are then refused.
func parseMultipart(data []byte, boundary string) (body any, status int, err error) {
	if boundary == "" {
		return nil, http.StatusBadRequest, errors.New("multipart body without a boundary")
	}
	out := map[string]any{}
	mr := multipart.NewReader(bytes.NewReader(data), boundary)
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return out, http.StatusOK, nil
		}
		if err != nil {
			return nil, http.StatusBadRequest, errors.New("the body is not a valid multipart form")
		}
		if part.FileName() != "" {
			return nil, http.StatusUnsupportedMediaType, errors.New("file uploads are not supported")
		}
		val, err := io.ReadAll(part)
		if err != nil {
			return nil, http.StatusBadRequest, errors.New("the body is not a valid multipart form")
		}
		addValue(out, part.FormName(), string(val))
	}
}

// readUploads streams a multipart form: text fields (together bounded by
// webhook.maxBodyBytes) become body fields, file parts become binary fields
// named after the form field, bounded by the blob limits.
func (h *Handler) readUploads(r *http.Request, boundary string, exec domain.ExecutionID) (body any, binary map[string]domain.BinaryRef, status int, err error) {
	if boundary == "" {
		return nil, nil, http.StatusBadRequest, errors.New("multipart body without a boundary")
	}
	invalid := errors.New("the body is not a valid multipart form")
	fields, binary := map[string]any{}, map[string]domain.BinaryRef{}
	budget := h.cfg.MaxBodyBytes
	mr := multipart.NewReader(r.Body, boundary)
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return fields, binary, http.StatusOK, nil
		}
		if err != nil {
			return nil, nil, http.StatusBadRequest, invalid
		}
		if part.FileName() == "" {
			val, err := io.ReadAll(io.LimitReader(part, budget+1))
			budget -= int64(len(val))
			switch {
			case budget < 0:
				return nil, nil, http.StatusRequestEntityTooLarge, fmt.Errorf("form fields exceed %d bytes", h.cfg.MaxBodyBytes)
			case err != nil:
				return nil, nil, http.StatusBadRequest, invalid
			}
			addValue(fields, part.FormName(), string(val))
			continue
		}
		typ := part.Header.Get("Content-Type")
		ref, err := h.d.Blobs.Put(r.Context(), exec, part.FileName(), typ, part)
		switch {
		case errors.Is(err, port.ErrBlobTooLarge):
			return nil, nil, http.StatusRequestEntityTooLarge, errors.New("an uploaded file exceeds the size limit")
		case err != nil:
			return nil, nil, http.StatusInternalServerError, errors.New("storing the uploaded file failed")
		}
		binary[uniqueKey(binary, part.FormName())] = ref
	}
}

// uniqueKey returns name, or name_1, name_2... when taken.
func uniqueKey(m map[string]domain.BinaryRef, name string) string {
	if name == "" {
		name = "file"
	}
	key := name
	for i := 1; ; i++ {
		if _, taken := m[key]; !taken {
			return key
		}
		key = name + "_" + strconv.Itoa(i)
	}
}

// textOf returns the body as a string; bytes that are not UTF-8 are base64.
func textOf(data []byte) string {
	if utf8.Valid(data) {
		return string(data)
	}
	return base64.StdEncoding.EncodeToString(data)
}

// headersOf lowercases names, joins repeated values and drops credentials.
func headersOf(h http.Header, authHeader string) map[string]any {
	out := make(map[string]any, len(h))
	for k, v := range h {
		name := strings.ToLower(k)
		if name == authHeader {
			continue
		}
		skip := false
		for _, s := range secretHeaders {
			skip = skip || name == s
		}
		if !skip {
			out[name] = strings.Join(v, ", ")
		}
	}
	return out
}

// queryOf maps a single value to a string and repeated values to a list.
func queryOf(q url.Values) map[string]any {
	out := make(map[string]any, len(q))
	for k, vs := range q {
		for _, v := range vs {
			addValue(out, k, v)
		}
	}
	return out
}

func addValue(m map[string]any, key, v string) {
	switch cur := m[key].(type) {
	case nil:
		m[key] = v
	case string:
		m[key] = []any{cur, v}
	case []any:
		m[key] = append(cur, v)
	}
}
