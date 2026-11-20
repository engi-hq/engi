package credential_test

import (
	"encoding/json"
	"net/http"
	"strconv"
)

func itoa(n int32) string { return strconv.Itoa(int(n)) }

func writeJSON(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }
