package httprequest

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

// defaultBinaryField names the binary field when the parameter is empty.
const defaultBinaryField = "data"

func binaryFieldParam(ctx context.Context, in nodesdk.Input, name string, i int) (string, error) {
	v, err := nodesdk.ParamString(ctx, in, name, i)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", name, err)
	}
	if v = strings.TrimSpace(v); v == "" {
		return defaultBinaryField, nil
	}
	return v, nil
}

// binaryResponse streams the response body into the blob store and returns one
// item holding the reference (plus status and headers with fullResponse).
func binaryResponse(ctx context.Context, in nodesdk.Input, s settings, resp *http.Response) ([]nodesdk.Item, error) {
	typ := resp.Header.Get("Content-Type")
	if _, _, err := mime.ParseMediaType(typ); err != nil {
		typ = "application/octet-stream"
	}
	ref, err := in.Blob().Put(ctx, responseFileName(resp), typ, resp.Body)
	if err != nil {
		return nil, &nodesdk.NodeError{Message: "storing the response as binary data failed", Description: err.Error(), HTTPStatus: resp.StatusCode, Err: err}
	}
	out := map[string]any{}
	if s.fullResponse {
		out["statusCode"] = float64(resp.StatusCode)
		out["statusMessage"] = strings.TrimSpace(strings.TrimPrefix(resp.Status, fmt.Sprint(resp.StatusCode)))
		out["headers"] = headerMap(resp.Header)
	}
	return []nodesdk.Item{{JSON: out, Binary: map[string]nodesdk.BinaryRef{s.outputBinary: ref}}}, nil
}

// responseFileName takes the name from Content-Disposition, else from the last
// path segment of the final URL.
func responseFileName(resp *http.Response) string {
	if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil && params["filename"] != "" {
		return path.Base(strings.ReplaceAll(params["filename"], "\\", "/"))
	}
	if resp.Request != nil && resp.Request.URL != nil {
		if base := path.Base(resp.Request.URL.Path); base != "." && base != "/" {
			return base
		}
	}
	return ""
}

// headerMap lowercases names and joins repeated values.
func headerMap(h http.Header) map[string]any {
	out := make(map[string]any, len(h))
	for k, v := range h {
		out[strings.ToLower(k)] = strings.Join(v, ", ")
	}
	return out
}
