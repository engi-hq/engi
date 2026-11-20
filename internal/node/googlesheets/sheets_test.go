package googlesheets

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/credtype/googleserviceaccount"
	"github.com/engi-hq/engi/internal/credtype/oauth2"
	"github.com/engi-hq/engi/internal/node/chatkit"
	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

const secretToken = "ya29.super-secret"

type recorded struct {
	method, path, query, auth string
	body                      map[string]any
	raw                       string
}

type fixture struct {
	mu   sync.Mutex
	reqs []recorded
	srv  *httptest.Server
}

func newFixture(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *fixture {
	t.Helper()
	f := &fixture{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec := recorded{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, auth: r.Header.Get("Authorization"), raw: string(b)}
		_ = json.Unmarshal(b, &rec.body)
		f.mu.Lock()
		f.reqs = append(f.reqs, rec)
		f.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func reply(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }

func (f *fixture) node(caller chatkit.Caller) *Node {
	return New(Config{BaseURL: f.srv.URL + "/v4", MaxReadRows: 3, Caller: caller, Now: func() time.Time { return time.Unix(1700000000, 0) }})
}

func (f *fixture) run(t *testing.T, caller chatkit.Caller, items []map[string]any, params map[string]any) (nodesdk.Output, error) {
	t.Helper()
	n := f.node(caller)
	in := nodetest.New(n.Describe(), nodetest.Items(items...), params)
	in.Client = f.srv.Client()
	ty := oauth2.Type()
	in.Credentials = map[string]nodesdk.Credential{oauth2.Name: {Type: oauth2.Name, Data: map[string]any{oauth2.FieldAccess: secretToken}, Auth: ty.Authenticate}}
	return n.Execute(context.Background(), in)
}

func TestDescriptorIsValid(t *testing.T) {
	if err := New(Config{}).Describe().Validate(); err != nil {
		t.Fatal(err)
	}
}

func headerThen(rows map[string]any) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ":append"):
			reply(w, map[string]any{"updates": map[string]any{"updatedRange": "Data!A7:C8"}})
		case strings.HasSuffix(r.URL.Path, "values:batchUpdate"):
			reply(w, map[string]any{"totalUpdatedCells": 1})
		case r.URL.Query().Get("majorDimension") == "COLUMNS":
			reply(w, map[string]any{"values": []any{[]any{"id", "a", "b", "c"}}})
		default:
			reply(w, rows)
		}
	}
}

var headerOnly = map[string]any{"range": "Data!A1:C1", "values": []any{[]any{"id", "name", "qty"}}}

func TestAppendRowsMapsHeaderAndBatches(t *testing.T) {
	f := newFixture(t, headerThen(headerOnly))
	out, err := f.run(t, chatkit.Caller{}, []map[string]any{
		{"id": "1", "name": "x", "qty": 5.0}, {"id": "2", "qty": 7.0}, {"id": "3", "name": map[string]any{"k": 1.0}},
	}, map[string]any{"operation": "appendRows", "spreadsheetId": "SID", "sheet": "Data"})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.reqs) != 2 {
		t.Fatalf("want header read + one append, got %d requests", len(f.reqs))
	}
	h, a := f.reqs[0], f.reqs[1]
	if h.method != "GET" || h.path != "/v4/spreadsheets/SID/values/'Data'!1:1" || h.auth != "Bearer "+secretToken {
		t.Errorf("header request %+v", h)
	}
	if a.method != "POST" || a.path != "/v4/spreadsheets/SID/values/'Data'!A1:append" {
		t.Errorf("append request %+v", a)
	}
	q, _ := url.ParseQuery(a.query)
	if q.Get("valueInputOption") != "USER_ENTERED" || q.Get("insertDataOption") != "INSERT_ROWS" {
		t.Errorf("query %q", a.query)
	}
	vals := a.body["values"].([]any)
	if len(vals) != 3 || vals[1].([]any)[1] != "" || vals[1].([]any)[2] != 7.0 || vals[2].([]any)[1] != `{"k":1}` {
		t.Errorf("values %v", vals)
	}
	got := nodetest.JSONs(out.Ports[0])
	if len(got) != 3 || got[0]["rowNumber"] != 7.0 || got[2]["rowNumber"] != 9.0 {
		t.Errorf("output %v", got)
	}
	for i, it := range out.Ports[0] {
		if len(it.PairedItem) != 1 || it.PairedItem[0].Item != i {
			t.Errorf("item %d pairing %v", i, it.PairedItem)
		}
	}
}

func TestAppendRejectsUnknownColumnWithItemIndex(t *testing.T) {
	f := newFixture(t, headerThen(headerOnly))
	_, err := f.run(t, chatkit.Caller{}, []map[string]any{{"id": "1"}, {"bogus": "x"}},
		map[string]any{"operation": "appendRows", "spreadsheetId": "SID", "sheet": "Data"})
	var ne *nodesdk.NodeError
	if !errors.As(err, &ne) || ne.ItemIndex == nil || *ne.ItemIndex != 1 || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("got %v", err)
	}
	// Ignoring extra columns turns the same input into a row of blanks.
	f2 := newFixture(t, headerThen(headerOnly))
	if _, err := f2.run(t, chatkit.Caller{}, []map[string]any{{"bogus": "x"}},
		map[string]any{"operation": "appendRows", "spreadsheetId": "SID", "sheet": "Data", "extraColumns": "ignore"}); err != nil {
		t.Fatal(err)
	}
}

func TestReadRowsHeaderMapping(t *testing.T) {
	data := map[string]any{"range": "Data!A1:C9", "values": []any{
		[]any{"id", "name", "", "name"},
		[]any{"1", "ann"},
		[]any{},
		[]any{"3", "cy", "skipped", "dup"},
		[]any{"4"}, []any{"5"},
	}}
	f := newFixture(t, headerThen(data))
	out, err := f.run(t, chatkit.Caller{}, []map[string]any{{}, {}},
		map[string]any{"operation": "readRows", "spreadsheetId": "https://docs.google.com/spreadsheets/d/SID/edit#gid=0", "sheet": "O'Neil", "valueRenderOption": "UNFORMATTED_VALUE"})
	if err != nil {
		t.Fatal(err)
	}
	r := f.reqs[0]
	if r.path != "/v4/spreadsheets/SID/values/'O''Neil'!1:4" { // header + MaxReadRows(3)
		t.Errorf("path %q", r.path)
	}
	if q, _ := url.ParseQuery(r.query); q.Get("valueRenderOption") != "UNFORMATTED_VALUE" {
		t.Errorf("query %q", r.query)
	}
	// MaxReadRows=3 non-empty rows per item; the empty row is skipped but keeps its number.
	want := []map[string]any{
		{"id": "1", "name": "ann", "rowNumber": 2.0},
		{"id": "3", "name": "cy", "rowNumber": 4.0},
		{"id": "4", "name": "", "rowNumber": 5.0},
	}
	got := nodetest.JSONs(out.Ports[0])
	if len(got) != 6 {
		t.Fatalf("want 3 rows for each of 2 items, got %d", len(got))
	}
	for i, w := range want {
		for k, v := range w {
			if got[i][k] != v {
				t.Errorf("row %d %s = %v, want %v", i, k, got[i][k], v)
			}
		}
	}
	if got[0]["name"] != "ann" || len(got[0]) != 3 {
		t.Errorf("empty header skipped, duplicate ignored: %v", got[0])
	}
	if out.Ports[0][3].PairedItem[0].Item != 1 {
		t.Errorf("second item's rows must pair with item 1")
	}
}

func TestReadRowsExplicitRangeAndNoRowNumber(t *testing.T) {
	f := newFixture(t, headerThen(map[string]any{"range": "Data!B3:C4", "values": []any{[]any{"a", "b"}, []any{"1", "2"}}}))
	out, err := f.run(t, chatkit.Caller{}, []map[string]any{{}},
		map[string]any{"operation": "readRows", "spreadsheetId": "SID", "sheet": "Data", "range": "B3:C4", "rowNumberField": ""})
	if err != nil {
		t.Fatal(err)
	}
	if f.reqs[0].path != "/v4/spreadsheets/SID/values/'Data'!B3:C4" {
		t.Errorf("path %q", f.reqs[0].path)
	}
	if got := nodetest.JSONs(out.Ports[0]); len(got) != 1 || len(got[0]) != 2 || got[0]["b"] != "2" {
		t.Errorf("got %v", got)
	}
}

func TestUpdateRows(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		items  []map[string]any
		check  func(t *testing.T, f *fixture, out []map[string]any)
	}{
		{"by row number", map[string]any{"matchBy": "rowNumber", "rowNumber": 5.0, "valueInputOption": "RAW"},
			[]map[string]any{{"name": "zed", "qty": 9.0, "rowNumber": 99.0}},
			func(t *testing.T, f *fixture, out []map[string]any) {
				b := f.reqs[len(f.reqs)-1]
				if b.method != "POST" || b.path != "/v4/spreadsheets/SID/values:batchUpdate" || b.body["valueInputOption"] != "RAW" {
					t.Errorf("request %+v", b)
				}
				data := b.body["data"].([]any)
				if len(data) != 2 || data[0].(map[string]any)["range"] != "'Data'!B5" || data[1].(map[string]any)["range"] != "'Data'!C5" {
					t.Errorf("data %v", data)
				}
				if out[0]["rowNumber"] != 5.0 || out[0]["name"] != "zed" {
					t.Errorf("out %v", out)
				}
			}},
		{"by key column", map[string]any{"matchBy": "column", "matchColumn": "id"},
			[]map[string]any{{"id": "b", "qty": 1.0}},
			func(t *testing.T, f *fixture, out []map[string]any) {
				data := f.reqs[len(f.reqs)-1].body["data"].([]any)
				// keys id,a,b,c => "b" is row 3; id and qty are both written.
				if data[0].(map[string]any)["range"] != "'Data'!A3" || data[1].(map[string]any)["range"] != "'Data'!C3" || out[0]["rowNumber"] != 3.0 {
					t.Errorf("data %v out %v", data, out)
				}
				if f.reqs[1].path != "/v4/spreadsheets/SID/values/'Data'!A:A" {
					t.Errorf("key column request %q", f.reqs[1].path)
				}
			}},
		{"not found skips", map[string]any{"matchBy": "column", "matchColumn": "id", "matchValue": "zzz", "ifNotFound": "skip"},
			[]map[string]any{{"qty": 1.0}},
			func(t *testing.T, f *fixture, out []map[string]any) {
				if len(f.reqs) != 2 || out[0]["qty"] != 1.0 || out[0]["rowNumber"] != nil {
					t.Errorf("reqs %d out %v", len(f.reqs), out)
				}
			}},
		{"not found appends", map[string]any{"matchBy": "column", "matchColumn": "id", "ifNotFound": "append"},
			[]map[string]any{{"id": "new", "qty": 4.0}, {"id": "new", "qty": 5.0}},
			func(t *testing.T, f *fixture, out []map[string]any) {
				// First item appends (updatedRange A7), second finds the freshly appended key at row 7.
				if f.reqs[2].method != "POST" || !strings.HasSuffix(f.reqs[2].path, ":append") {
					t.Errorf("want append, got %+v", f.reqs[2])
				}
				last := f.reqs[len(f.reqs)-1].body["data"].([]any)
				if out[0]["rowNumber"] != 7.0 || out[1]["rowNumber"] != 7.0 || last[0].(map[string]any)["range"] != "'Data'!A7" {
					t.Errorf("out %v last %v", out, last)
				}
			}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, headerThen(headerOnly2))
			params := map[string]any{"operation": "updateRows", "spreadsheetId": "SID", "sheet": "Data"}
			for k, v := range tc.params {
				params[k] = v
			}
			out, err := f.run(t, chatkit.Caller{}, tc.items, params)
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, f, nodetest.JSONs(out.Ports[0]))
		})
	}
}

var headerOnly2 = map[string]any{"range": "Data!A1:C1", "values": []any{[]any{"id", "name", "qty"}}}

func TestUpdateNotFoundFails(t *testing.T) {
	f := newFixture(t, headerThen(headerOnly2))
	_, err := f.run(t, chatkit.Caller{}, []map[string]any{{"qty": 1.0}},
		map[string]any{"operation": "updateRows", "spreadsheetId": "SID", "sheet": "Data", "matchBy": "column", "matchColumn": "id", "matchValue": "zzz"})
	var ne *nodesdk.NodeError
	if !errors.As(err, &ne) || ne.ItemIndex == nil || *ne.ItemIndex != 0 || !strings.Contains(err.Error(), "no row matches") {
		t.Fatalf("got %v", err)
	}
	_, err = f.run(t, chatkit.Caller{}, []map[string]any{{"qty": 1.0}},
		map[string]any{"operation": "updateRows", "spreadsheetId": "SID", "sheet": "Data", "matchBy": "rowNumber", "rowNumber": 1.0})
	if err == nil || !strings.Contains(err.Error(), "greater than the header row") {
		t.Fatalf("got %v", err)
	}
}

func TestClearCreateAndList(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ":clear"):
			reply(w, map[string]any{"clearedRange": "Data!A2:C9"})
		case strings.HasSuffix(r.URL.Path, ":batchUpdate"):
			reply(w, map[string]any{"replies": []any{map[string]any{"addSheet": map[string]any{"properties": map[string]any{"sheetId": 42.0, "title": "New"}}}}})
		default:
			reply(w, map[string]any{"sheets": []any{
				map[string]any{"properties": map[string]any{"sheetId": 0.0, "title": "A"}},
				map[string]any{"properties": map[string]any{"sheetId": 1.0, "title": "B"}},
			}})
		}
	})
	out, err := f.run(t, chatkit.Caller{}, []map[string]any{{}}, map[string]any{"operation": "clearRange", "spreadsheetId": "SID", "sheet": "Data", "range": "A2:C9"})
	if err != nil || nodetest.JSONs(out.Ports[0])[0]["clearedRange"] != "Data!A2:C9" {
		t.Fatalf("clear %v %v", out, err)
	}
	if r := f.reqs[0]; r.method != "POST" || r.path != "/v4/spreadsheets/SID/values/'Data'!A2:C9:clear" {
		t.Errorf("clear request %+v", r)
	}
	out, err = f.run(t, chatkit.Caller{}, []map[string]any{{}}, map[string]any{"operation": "clearRange", "spreadsheetId": "SID", "sheet": "Data"})
	if err != nil || f.reqs[1].path != "/v4/spreadsheets/SID/values/'Data':clear" {
		t.Errorf("whole-sheet clear %v %q", err, f.reqs[1].path)
	}
	out, err = f.run(t, chatkit.Caller{}, []map[string]any{{}}, map[string]any{"operation": "createSheet", "spreadsheetId": "SID", "title": "New"})
	if err != nil || nodetest.JSONs(out.Ports[0])[0]["sheetId"] != 42.0 {
		t.Fatalf("create %v %v", out, err)
	}
	req := f.reqs[2].body["requests"].([]any)[0].(map[string]any)["addSheet"].(map[string]any)["properties"].(map[string]any)
	if f.reqs[2].path != "/v4/spreadsheets/SID:batchUpdate" || req["title"] != "New" {
		t.Errorf("create request %+v", f.reqs[2])
	}
	out, err = f.run(t, chatkit.Caller{}, []map[string]any{{}, {}}, map[string]any{"operation": "listSheets", "spreadsheetId": "SID"})
	if err != nil || len(out.Ports[0]) != 4 || out.Ports[0][2].PairedItem[0].Item != 1 {
		t.Fatalf("list %v %v", out, err)
	}
	if f.reqs[3].method != "GET" || f.reqs[3].query != "fields=sheets.properties" {
		t.Errorf("list request %+v", f.reqs[3])
	}
}

func TestAPIErrorBecomesNodeError(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		reply(w, map[string]any{"error": map[string]any{"code": 403, "status": "PERMISSION_DENIED", "message": "caller " + secretToken + " lacks access"}})
	})
	_, err := f.run(t, chatkit.Caller{}, []map[string]any{{}, {}}, map[string]any{"operation": "listSheets", "spreadsheetId": "SID"})
	var ne *nodesdk.NodeError
	if !errors.As(err, &ne) || ne.HTTPStatus != 403 || ne.ItemIndex == nil || *ne.ItemIndex != 0 {
		t.Fatalf("got %#v", err)
	}
	if strings.Contains(err.Error(), secretToken) || !strings.Contains(err.Error(), "PERMISSION_DENIED") {
		t.Errorf("message %q", err)
	}
}

func TestRateLimitRetriesWithRetryAfter(t *testing.T) {
	calls := 0
	f := newFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		reply(w, map[string]any{"sheets": []any{}})
	})
	var slept []time.Duration
	caller := chatkit.Caller{Limits: chatkit.Limits{MaxRetryWait: 5 * time.Second, MaxRetries: 2},
		Sleep: func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }}
	if _, err := f.run(t, caller, []map[string]any{{}}, map[string]any{"operation": "listSheets", "spreadsheetId": "SID"}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(slept) != 1 || slept[0] != 3*time.Second {
		t.Errorf("calls %d slept %v", calls, slept)
	}
	// A wait above the cap fails instead of blocking.
	f2 := newFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	_, err := f2.run(t, caller, []map[string]any{{}}, map[string]any{"operation": "listSheets", "spreadsheetId": "SID"})
	if !errors.Is(err, chatkit.ErrRateLimited) {
		t.Errorf("got %v", err)
	}
}

func TestServiceAccountToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	var tokenForm url.Values
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			reply(w, map[string]any{"access_token": "minted-token"})
			return
		}
		reply(w, map[string]any{"sheets": []any{}})
	})
	n := f.node(chatkit.Caller{})
	in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{}), map[string]any{"authentication": "serviceAccount", "operation": "listSheets", "spreadsheetId": "SID"})
	in.Client = f.srv.Client()
	in.Credentials = map[string]nodesdk.Credential{googleserviceaccount.Name: {Type: googleserviceaccount.Name, Data: map[string]any{
		googleserviceaccount.FieldEmail: "sa@x.iam", googleserviceaccount.FieldPrivateKey: pemKey, googleserviceaccount.FieldTokenURL: f.srv.URL + "/token", googleserviceaccount.FieldSubject: "boss@x.com",
	}}}
	if _, err := n.Execute(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	for _, rq := range f.reqs {
		if rq.path == "/token" {
			tokenForm, _ = url.ParseQuery(rq.raw)
		}
	}
	if tokenForm.Get("grant_type") != jwtGrantType {
		t.Errorf("form %v", tokenForm)
	}
	parts := strings.Split(tokenForm.Get("assertion"), ".")
	if len(parts) != 3 {
		t.Fatalf("assertion %q", tokenForm.Get("assertion"))
	}
	if last := f.reqs[len(f.reqs)-1]; last.auth != "Bearer minted-token" {
		t.Errorf("sheets request auth %q", last.auth)
	}
	claims := decodePart(t, parts[1])
	if claims["iss"] != "sa@x.iam" || claims["sub"] != "boss@x.com" || claims["scope"] != sheetsScope || claims["exp"].(float64)-claims["iat"].(float64) != 3600 {
		t.Errorf("claims %v", claims)
	}
	// A bad key fails before any request, without echoing the key.
	in.Credentials[googleserviceaccount.Name].Data[googleserviceaccount.FieldPrivateKey] = "not pem"
	if _, err := n.Execute(context.Background(), in); err == nil || strings.Contains(err.Error(), "not pem") {
		t.Errorf("got %v", err)
	}
}

func decodePart(t *testing.T, s string) map[string]any {
	t.Helper()
	b, err := base64URL(s)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestColumnLettersAndRowStart(t *testing.T) {
	for in, want := range map[int]string{0: "A", 25: "Z", 26: "AA", 27: "AB", 701: "ZZ", 702: "AAA"} {
		if got := columnLetters(in); got != want {
			t.Errorf("columnLetters(%d) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]int{"Data!A5:C9": 5, "'My Sheet'!B12": 12, "Data!A:C": 1, "A3:B4": 3, "Data!$A$7:$B$9": 7} {
		if got := rowStart(in); got != want {
			t.Errorf("rowStart(%q) = %d, want %d", in, got, want)
		}
	}
}

func base64URL(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }
