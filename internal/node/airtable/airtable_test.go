package airtable

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	credairtable "github.com/engi-hq/engi/internal/credtype/airtable"
	"github.com/engi-hq/engi/internal/node/chatkit"
	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

const secretToken = "pat.super-secret"

type recorded struct {
	method, path, query, auth string
	body                      map[string]any
}

type fixture struct {
	mu   sync.Mutex
	reqs []recorded
	srv  *httptest.Server
}

func newFixture(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, body map[string]any)) *fixture {
	t.Helper()
	f := &fixture{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		f.mu.Lock()
		f.reqs = append(f.reqs, recorded{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, auth: r.Header.Get("Authorization"), body: body})
		f.mu.Unlock()
		handler(w, r, body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func reply(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }

func (f *fixture) run(t *testing.T, caller chatkit.Caller, items []map[string]any, params map[string]any) (nodesdk.Output, error) {
	t.Helper()
	n := New(Config{BaseURL: f.srv.URL + "/v0", MaxListResults: 5, PageSize: 2, BatchSize: 10, Caller: caller})
	if _, ok := params["baseId"]; !ok {
		params["baseId"], params["table"] = "appB", "My Table"
	}
	in := nodetest.New(n.Describe(), nodetest.Items(items...), params)
	in.Client = f.srv.Client()
	ty := credairtable.Type()
	in.Credentials = map[string]nodesdk.Credential{credairtable.Name: {Type: credairtable.Name, Data: map[string]any{"token": secretToken}, Auth: ty.Authenticate}}
	return n.Execute(context.Background(), in)
}

func TestDescriptorIsValid(t *testing.T) {
	if err := New(Config{}).Describe().Validate(); err != nil {
		t.Fatal(err)
	}
}

// echoRecords answers a write the way Airtable does: one record per input,
// ids rec1..recN, marking createdRecords when asked.
func echoRecords(created func(i int) bool) func(w http.ResponseWriter, r *http.Request, body map[string]any) {
	return func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		if r.Method == http.MethodDelete {
			var recs []any
			for _, id := range r.URL.Query()["records[]"] {
				recs = append(recs, map[string]any{"id": id, "deleted": true})
			}
			reply(w, map[string]any{"records": recs})
			return
		}
		in, _ := body["records"].([]any)
		var recs, createdIDs []any
		for i, rec := range in {
			m := rec.(map[string]any)
			id := fmt.Sprintf("rec%d", i)
			if v, ok := m["id"].(string); ok {
				id = v
			}
			if created != nil && created(i) {
				createdIDs = append(createdIDs, id)
			}
			recs = append(recs, map[string]any{"id": id, "fields": m["fields"]})
		}
		reply(w, map[string]any{"records": recs, "createdRecords": createdIDs})
	}
}

func TestListRecordsRequestAndPagination(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		page := map[string]any{"records": []any{map[string]any{"id": "r1"}, map[string]any{"id": "r2"}}}
		switch r.URL.Query().Get("offset") {
		case "":
			page["offset"] = "o2"
		case "o2":
			page["offset"] = "o3"
		}
		reply(w, page)
	})
	out, err := f.run(t, chatkit.Caller{}, []map[string]any{{}, {}}, map[string]any{
		"operation": "listRecords", "filterByFormula": "{Qty}>1", "view": "Grid", "returnFields": "Name, Qty",
		"sort": `[{"field":"Name","direction":"desc"},{"field":"Qty"}]`, "limit": 3.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.reqs) != 4 { // 2 pages per input item: 2 records, then 1 of the next page
		t.Fatalf("requests: %d", len(f.reqs))
	}
	r := f.reqs[0]
	q, _ := url.ParseQuery(r.query)
	if r.method != "GET" || r.path != "/v0/appB/My Table" || r.auth != "Bearer "+secretToken {
		t.Errorf("request %+v", r)
	}
	if q.Get("filterByFormula") != "{Qty}>1" || q.Get("view") != "Grid" || strings.Join(q["fields[]"], ",") != "Name,Qty" ||
		q.Get("sort[0][field]") != "Name" || q.Get("sort[0][direction]") != "desc" || q.Get("sort[1][direction]") != "asc" || q.Get("pageSize") != "2" {
		t.Errorf("query %v", q)
	}
	if q2, _ := url.ParseQuery(f.reqs[1].query); q2.Get("offset") != "o2" || q2.Get("pageSize") != "1" {
		t.Errorf("second page query %v", q2)
	}
	if len(out.Ports[0]) != 6 || out.Ports[0][3].PairedItem[0].Item != 1 || out.Ports[0][2].PairedItem[0].Item != 0 {
		t.Errorf("output pairing %v", out.Ports[0])
	}
}

func TestListRecordsCap(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		reply(w, map[string]any{"records": []any{map[string]any{"id": "a"}, map[string]any{"id": "b"}}, "offset": "more"})
	})
	out, err := f.run(t, chatkit.Caller{}, []map[string]any{{}}, map[string]any{"operation": "listRecords", "limit": 100.0})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Ports[0]) != 5 || len(f.reqs) != 3 { // cap 5, page 2 -> 2+2+1
		t.Errorf("got %d records in %d requests", len(out.Ports[0]), len(f.reqs))
	}
	if _, err := f.run(t, chatkit.Caller{}, []map[string]any{{}}, map[string]any{"operation": "listRecords", "sort": `[{"field":"x","direction":"up"}]`}); err == nil {
		t.Error("bad sort direction must fail")
	}
}

func TestGetRecord(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		reply(w, map[string]any{"id": "rec9", "createdTime": "t", "fields": map[string]any{"Name": "n"}})
	})
	out, err := f.run(t, chatkit.Caller{}, []map[string]any{{}}, map[string]any{"operation": "getRecord", "recordId": "rec9"})
	if err != nil {
		t.Fatal(err)
	}
	if r := f.reqs[0]; r.method != "GET" || r.path != "/v0/appB/My Table/rec9" {
		t.Errorf("request %+v", r)
	}
	if got := nodetest.JSONs(out.Ports[0]); len(got) != 1 || got[0]["id"] != "rec9" {
		t.Errorf("got %v", got)
	}
}

func items(n int) []map[string]any {
	out := make([]map[string]any, n)
	for i := range out {
		out[i] = map[string]any{"Name": fmt.Sprintf("n%d", i)}
	}
	return out
}

func TestCreateBatchesOfTen(t *testing.T) {
	f := newFixture(t, echoRecords(nil))
	out, err := f.run(t, chatkit.Caller{}, items(23), map[string]any{"operation": "createRecords", "typecast": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.reqs) != 3 {
		t.Fatalf("23 records must be 3 requests, got %d", len(f.reqs))
	}
	sizes := []int{}
	for _, r := range f.reqs {
		if r.method != "POST" || r.path != "/v0/appB/My Table" || r.body["typecast"] != true {
			t.Errorf("request %+v", r)
		}
		sizes = append(sizes, len(r.body["records"].([]any)))
	}
	if fmt.Sprint(sizes) != "[10 10 3]" {
		t.Errorf("batch sizes %v", sizes)
	}
	first := f.reqs[0].body["records"].([]any)[0].(map[string]any)["fields"].(map[string]any)
	if first["Name"] != "n0" {
		t.Errorf("fields default to the item JSON: %v", first)
	}
	if len(out.Ports[0]) != 23 {
		t.Fatalf("outputs %d", len(out.Ports[0]))
	}
	for i, it := range out.Ports[0] {
		if it.PairedItem[0].Item != i {
			t.Fatalf("output %d pairs with %d", i, it.PairedItem[0].Item)
		}
	}
}

func TestCreateWithFieldsParam(t *testing.T) {
	f := newFixture(t, echoRecords(nil))
	if _, err := f.run(t, chatkit.Caller{}, items(1), map[string]any{"operation": "createRecords", "fields": `{"A":1}`}); err != nil {
		t.Fatal(err)
	}
	got := f.reqs[0].body["records"].([]any)[0].(map[string]any)["fields"].(map[string]any)
	if len(got) != 1 || got["A"] != 1.0 {
		t.Errorf("fields %v", got)
	}
	if _, ok := f.reqs[0].body["typecast"]; ok {
		t.Error("typecast must be omitted when false")
	}
}

func TestUpdateUpsertDelete(t *testing.T) {
	t.Run("update", func(t *testing.T) {
		f := newFixture(t, echoRecords(nil))
		_, err := f.run(t, chatkit.Caller{}, items(1), map[string]any{"operation": "updateRecords", "recordId": "recX"})
		if err != nil {
			t.Fatal(err)
		}
		rec := f.reqs[0].body["records"].([]any)[0].(map[string]any)
		if f.reqs[0].method != "PATCH" || rec["id"] != "recX" {
			t.Errorf("request %+v", f.reqs[0])
		}
	})
	t.Run("upsert", func(t *testing.T) {
		f := newFixture(t, echoRecords(func(i int) bool { return i == 1 }))
		out, err := f.run(t, chatkit.Caller{}, items(3), map[string]any{"operation": "upsertRecords", "mergeOn": "Name, Email"})
		if err != nil {
			t.Fatal(err)
		}
		up := f.reqs[0].body["performUpsert"].(map[string]any)["fieldsToMergeOn"].([]any)
		if f.reqs[0].method != "PATCH" || len(up) != 2 || up[1] != "Email" {
			t.Errorf("request %+v", f.reqs[0])
		}
		got := nodetest.JSONs(out.Ports[0])
		if got[0]["created"] != false || got[1]["created"] != true {
			t.Errorf("created flags %v", got)
		}
		if _, err := f.run(t, chatkit.Caller{}, items(1), map[string]any{"operation": "upsertRecords", "mergeOn": ""}); err == nil {
			t.Error("upsert without mergeOn must fail")
		}
	})
	t.Run("delete batches", func(t *testing.T) {
		f := newFixture(t, echoRecords(nil))
		ids := []string{}
		n := New(Config{BaseURL: f.srv.URL + "/v0", MaxListResults: 5, PageSize: 2, BatchSize: 2})
		in := nodetest.New(n.Describe(), nodetest.Items(items(5)...), map[string]any{"operation": "deleteRecords", "baseId": "appB", "table": "T"})
		in.ParamFunc = func(name string, i int) (any, bool) {
			if name == "recordId" {
				id := fmt.Sprintf("rec%d", i)
				ids = append(ids, id)
				return id, true
			}
			return nil, false
		}
		in.Client = f.srv.Client()
		ty := credairtable.Type()
		in.Credentials = map[string]nodesdk.Credential{credairtable.Name: {Type: credairtable.Name, Data: map[string]any{"token": secretToken}, Auth: ty.Authenticate}}
		out, err := n.Execute(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		if len(f.reqs) != 3 || f.reqs[0].method != "DELETE" {
			t.Fatalf("requests %+v", f.reqs)
		}
		if q, _ := url.ParseQuery(f.reqs[0].query); strings.Join(q["records[]"], ",") != "rec0,rec1" {
			t.Errorf("query %q", f.reqs[0].query)
		}
		if got := nodetest.JSONs(out.Ports[0]); len(got) != 5 || got[4]["id"] != "rec4" || got[4]["deleted"] != true || out.Ports[0][4].PairedItem[0].Item != 4 {
			t.Errorf("out %v", got)
		}
	})
}

func TestBatchBreaksWhenTableChanges(t *testing.T) {
	f := newFixture(t, echoRecords(nil))
	n := New(Config{BaseURL: f.srv.URL + "/v0", BatchSize: 10})
	in := nodetest.New(n.Describe(), nodetest.Items(items(3)...), map[string]any{"operation": "createRecords", "baseId": "appB"})
	in.ParamFunc = func(name string, i int) (any, bool) {
		if name == "table" {
			return []string{"T1", "T1", "T2"}[i], true
		}
		return nil, false
	}
	in.Client = f.srv.Client()
	ty := credairtable.Type()
	in.Credentials = map[string]nodesdk.Credential{credairtable.Name: {Type: credairtable.Name, Data: map[string]any{"token": secretToken}, Auth: ty.Authenticate}}
	if _, err := n.Execute(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if len(f.reqs) != 2 || f.reqs[0].path != "/v0/appB/T1" || f.reqs[1].path != "/v0/appB/T2" {
		t.Errorf("requests %+v", f.reqs)
	}
}

func TestAPIErrorsCarryStatusAndItemIndex(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want string
	}{
		"object": {`{"error":{"type":"INVALID_REQUEST_UNKNOWN","message":"bad ` + secretToken + `"}}`, "INVALID_REQUEST_UNKNOWN"},
		"string": {`{"error":"NOT_FOUND"}`, "NOT_FOUND"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = io.WriteString(w, tc.body)
			})
			_, err := f.run(t, chatkit.Caller{}, items(15), map[string]any{"operation": "createRecords"})
			var ne *nodesdk.NodeError
			if !errors.As(err, &ne) || ne.HTTPStatus != 422 || ne.ItemIndex == nil || *ne.ItemIndex != 0 || !errors.Is(err, ErrAirtable) {
				t.Fatalf("got %#v", err)
			}
			if !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), secretToken) {
				t.Errorf("message %q", err)
			}
		})
	}
	// The second batch failing reports the first item of that batch.
	calls := 0
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request, b map[string]any) {
		calls++
		if calls == 2 {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"error":"FORBIDDEN"}`)
			return
		}
		echoRecords(nil)(w, r, b)
	})
	_, err := f.run(t, chatkit.Caller{}, items(15), map[string]any{"operation": "createRecords"})
	var ne *nodesdk.NodeError
	if !errors.As(err, &ne) || ne.ItemIndex == nil || *ne.ItemIndex != 10 || ne.HTTPStatus != 403 {
		t.Fatalf("got %#v", err)
	}
}

func TestRateLimitRetry(t *testing.T) {
	calls := 0
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request, b map[string]any) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		echoRecords(nil)(w, r, b)
	})
	var slept []time.Duration
	caller := chatkit.Caller{Limits: chatkit.Limits{MaxRetryWait: 5 * time.Second, MaxRetries: 1},
		Sleep: func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }}
	if _, err := f.run(t, caller, items(1), map[string]any{"operation": "createRecords"}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(slept) != 1 || slept[0] != 2*time.Second {
		t.Errorf("calls %d slept %v", calls, slept)
	}
	// Too long a wait, and retries running out, both fail with ErrRateLimited.
	f2 := newFixture(t, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	if _, err := f2.run(t, caller, items(1), map[string]any{"operation": "createRecords"}); !errors.Is(err, chatkit.ErrRateLimited) {
		t.Errorf("got %v", err)
	}
	f3 := newFixture(t, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	if _, err := f3.run(t, caller, items(1), map[string]any{"operation": "createRecords"}); !errors.Is(err, chatkit.ErrRateLimited) {
		t.Errorf("got %v", err)
	}
}

func TestEmptyInput(t *testing.T) {
	f := newFixture(t, echoRecords(nil))
	out, err := f.run(t, chatkit.Caller{}, nil, map[string]any{"operation": "createRecords"})
	if err != nil || len(out.Ports) != 1 || len(f.reqs) != 0 {
		t.Errorf("out %v err %v reqs %d", out, err, len(f.reqs))
	}
}
