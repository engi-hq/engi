package app

import (
	"net/http"
	"strings"
)

// apiPrefixes are the paths that always belong to the REST API handler; every
// other path goes to the UI when one is mounted.
var apiPrefixes = []string{"/api/", "/healthz", "/readyz", "/oauth2/"}

// withUI serves the editor for every path the API does not own.
func withUI(api, ui http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, p := range apiPrefixes {
			if strings.HasPrefix(r.URL.Path, p) {
				api.ServeHTTP(w, r)
				return
			}
		}
		ui.ServeHTTP(w, r)
	})
}
