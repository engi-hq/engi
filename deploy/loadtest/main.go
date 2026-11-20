// Command loadtest drives an Engi deployment through its public HTTP surface:
// it creates and activates a webhook workflow whose Code node does a fixed
// amount of work, fires requests at a fixed rate, waits until every accepted
// request has a terminal execution and reports counts, throughput and latency.
// It exits non-zero unless every accepted request ended in exactly one success.
//
//	loadtest -api http://api:8080 -webhook http://webhook:8080 -requests 2000 -rate 20
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type config struct {
	api, webhook, email, password string
	requests, rate, concurrency   int
	work, timeout, poll           time.Duration
	keep                          bool
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	var c config
	flag.StringVar(&c.api, "api", env("LOADTEST_API", "http://localhost:8080"), "API base URL (env LOADTEST_API)")
	flag.StringVar(&c.webhook, "webhook", env("LOADTEST_WEBHOOK", ""), "webhook base URL, default: same as -api (env LOADTEST_WEBHOOK)")
	flag.StringVar(&c.email, "email", env("LOADTEST_EMAIL", "smoke@example.com"), "login email (env LOADTEST_EMAIL)")
	flag.StringVar(&c.password, "password", os.Getenv("LOADTEST_PASSWORD"), "login password (env LOADTEST_PASSWORD)")
	flag.IntVar(&c.requests, "requests", 1000, "number of webhook calls")
	flag.IntVar(&c.rate, "rate", 20, "requests per second (open loop)")
	flag.IntVar(&c.concurrency, "concurrency", 64, "maximum in-flight webhook calls")
	flag.DurationVar(&c.work, "work", 200*time.Millisecond, "busy time of the Code node per execution")
	flag.DurationVar(&c.timeout, "timeout", 10*time.Minute, "how long to wait for executions after the last request")
	flag.DurationVar(&c.poll, "poll", 2*time.Second, "execution polling interval")
	flag.BoolVar(&c.keep, "keep", false, "leave the workflow active afterwards")
	flag.Parse()
	if c.webhook == "" {
		c.webhook = c.api
	}
	if c.password == "" {
		fmt.Fprintln(os.Stderr, "password required (-password or LOADTEST_PASSWORD)")
		os.Exit(2)
	}
	ok, err := run(context.Background(), c)
	if err != nil {
		fmt.Fprintln(os.Stderr, "loadtest:", err)
		os.Exit(2)
	}
	if !ok {
		os.Exit(1)
	}
}

type client struct {
	base   string
	hc     *http.Client
	csrf   string
	cookie string // session cookie, kept by hand: it is Secure and the cluster-internal URL is plain HTTP
}

func (cl *client) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, cl.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if cl.cookie != "" {
		req.Header.Set("Cookie", cl.cookie)
	}
	if cl.csrf != "" {
		req.Header.Set("X-CSRF-Token", cl.csrf)
	}
	resp, err := cl.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	for _, ck := range resp.Cookies() {
		if ck.Name == "engi_session" {
			cl.cookie = ck.Name + "=" + ck.Value
		}
	}
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s %s -> %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

type sent struct {
	at     time.Time
	status int
	err    error
}

type execSummary struct {
	ID         string     `json:"id"`
	Status     string     `json:"status"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt"`
}

var ridRe = regexp.MustCompile(`"outRid":\s*"([^"]+)"`)

func run(ctx context.Context, c config) (bool, error) {
	api := &client{base: strings.TrimRight(c.api, "/"), hc: &http.Client{Timeout: 30 * time.Second}}
	var login struct {
		CSRF string `json:"csrfToken"`
	}
	if err := api.do(ctx, "POST", "/api/v1/auth/login", map[string]string{"email": c.email, "password": c.password}, &login); err != nil {
		return false, fmt.Errorf("login: %w", err)
	}
	api.csrf = login.CSRF

	path := fmt.Sprintf("load-%d", time.Now().UnixNano())
	code := fmt.Sprintf("const end = Date.now() + %d; while (Date.now() < end) {}\nreturn { outRid: $json.body.rid, workMs: %d };", c.work.Milliseconds(), c.work.Milliseconds())
	wf := map[string]any{"name": "loadtest " + path,
		"nodes": []any{
			map[string]any{"name": "Hook", "type": "engi.trigger.webhook", "position": []int{0, 0},
				"parameters": map[string]any{"httpMethod": "POST", "path": path, "responseMode": "immediately"}},
			map[string]any{"name": "Work", "type": "engi.code.javascript", "position": []int{1, 0},
				"parameters": map[string]any{"mode": "runOnceForEachItem", "jsCode": code}}},
		"connections": []any{map[string]any{"from": map[string]any{"node": "Hook"}, "to": map[string]any{"node": "Work"}}}}
	var created struct {
		ID string `json:"id"`
	}
	if err := api.do(ctx, "POST", "/api/v1/workflows", wf, &created); err != nil {
		return false, fmt.Errorf("creating workflow: %w", err)
	}
	if err := api.do(ctx, "POST", "/api/v1/workflows/"+created.ID+"/activate", map[string]any{}, nil); err != nil {
		return false, fmt.Errorf("activating workflow: %w", err)
	}
	if !c.keep {
		defer func() {
			_ = api.do(context.Background(), "POST", "/api/v1/workflows/"+created.ID+"/deactivate", map[string]any{}, nil)
		}()
	}
	fmt.Printf("workflow %s active at /hook/%s; %d requests at %d/s, work %s\n", created.ID, path, c.requests, c.rate, c.work)

	// Wait for the webhook route to be live on the webhook pod(s).
	hc := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: c.concurrency}}
	hookURL := strings.TrimRight(c.webhook, "/") + "/hook/" + path
	post := func(rid string) (int, error) {
		resp, err := hc.Post(hookURL, "application/json", strings.NewReader(`{"rid":"`+rid+`"}`))
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, nil
	}
	probeStart := time.Now()
	probeRid := "probe"
	for {
		st, err := post(probeRid)
		if err == nil && st/100 == 2 {
			break
		}
		if time.Since(probeStart) > time.Minute {
			return false, fmt.Errorf("webhook never became ready (last status %d, err %v)", st, err)
		}
		time.Sleep(time.Second)
	}

	// Fire.
	results := make([]sent, c.requests)
	sem := make(chan struct{}, c.concurrency)
	var wg sync.WaitGroup
	start := time.Now()
	tick := time.NewTicker(time.Second / time.Duration(c.rate))
	defer tick.Stop()
	for i := 0; i < c.requests; i++ {
		<-tick.C
		sem <- struct{}{}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i].at = time.Now()
			results[i].status, results[i].err = post(rid(i))
		}(i)
	}
	wg.Wait()
	sendDur := time.Since(start)

	accepted, rejected, neterr := 0, 0, 0
	status5xx := 0
	for _, r := range results {
		switch {
		case r.err != nil:
			neterr++
		case r.status/100 == 2:
			accepted++
		default:
			rejected++
			if r.status >= 500 {
				status5xx++
			}
		}
	}
	fmt.Printf("sent %d in %s: accepted=%d rejected=%d (5xx=%d) network-errors=%d\n", c.requests, sendDur.Round(time.Millisecond), accepted, rejected, status5xx, neterr)

	// Wait for terminal executions: count per status until accepted+1 (probe) are terminal.
	want := accepted + 1
	deadline := time.Now().Add(c.timeout)
	var execs []execSummary
	for {
		var err error
		execs, err = listAll(ctx, api, created.ID)
		if err != nil {
			fmt.Println("poll error (retrying):", err)
		} else {
			term, counts := 0, map[string]int{}
			for _, e := range execs {
				counts[e.Status]++
				if e.FinishedAt != nil {
					term++
				}
			}
			fmt.Printf("  %s  executions=%d terminal=%d %v\n", time.Since(start).Round(time.Second), len(execs), term, counts)
			if term >= want && len(execs) == term {
				break
			}
		}
		if time.Now().After(deadline) {
			fmt.Println("timeout waiting for executions")
			break
		}
		time.Sleep(c.poll)
	}

	// Detail pass: map execution -> request id.
	type detail struct {
		sum execSummary
		rid string
	}
	details := make([]detail, len(execs))
	var dwg sync.WaitGroup
	dsem := make(chan struct{}, 16)
	for i, e := range execs {
		dwg.Add(1)
		dsem <- struct{}{}
		go func(i int, e execSummary) {
			defer dwg.Done()
			defer func() { <-dsem }()
			raw := json.RawMessage{}
			if err := api.do(ctx, "GET", "/api/v1/executions/"+e.ID, nil, &raw); err == nil {
				if m := ridRe.FindSubmatch(raw); m != nil {
					details[i].rid = string(m[1])
				}
			}
			details[i].sum = e
		}(i, e)
	}
	dwg.Wait()

	byRid := map[string][]detail{}
	statuses := map[string]int{}
	for _, d := range details {
		statuses[d.sum.Status]++
		if d.rid != "" {
			byRid[d.rid] = append(byRid[d.rid], d)
		}
	}
	var lat []float64
	var last time.Time
	var first = time.Now()
	missing, dups, notSuccess := 0, 0, 0
	var problems []string
	for i, r := range results {
		if r.err != nil || r.status/100 != 2 {
			continue
		}
		ds := byRid[rid(i)]
		succ := 0
		for _, d := range ds {
			if d.sum.Status == "success" {
				succ++
				if d.sum.FinishedAt != nil {
					lat = append(lat, d.sum.FinishedAt.Sub(r.at).Seconds())
					if d.sum.FinishedAt.After(last) {
						last = *d.sum.FinishedAt
					}
				}
			}
		}
		if r.at.Before(first) {
			first = r.at
		}
		switch {
		case len(ds) == 0:
			missing++
			problems = append(problems, fmt.Sprintf("%s: no execution", rid(i)))
		case len(ds) > 1:
			dups++
			ids := []string{}
			for _, d := range ds {
				ids = append(ids, d.sum.ID+"="+d.sum.Status)
			}
			problems = append(problems, fmt.Sprintf("%s: %d executions %v", rid(i), len(ds), ids))
		case succ != 1:
			notSuccess++
			problems = append(problems, fmt.Sprintf("%s: %s (%s)", rid(i), ds[0].sum.Status, ds[0].sum.ID))
		}
	}
	// Executions whose request was not counted as accepted (the client saw a
	// network error but the server had already queued it) are reported, not failed.
	acceptedRid := map[string]bool{"probe": true}
	for i, r := range results {
		if r.err == nil && r.status/100 == 2 {
			acceptedRid[rid(i)] = true
		}
	}
	orphans := 0
	for k := range byRid {
		if !acceptedRid[k] {
			orphans++
		}
	}
	sort.Float64s(lat)
	pct := func(p float64) float64 {
		if len(lat) == 0 {
			return 0
		}
		return lat[min(len(lat)-1, int(p*float64(len(lat))))]
	}
	var thr float64
	if span := last.Sub(first).Seconds(); span > 0 {
		thr = float64(len(lat)) / span
	}
	fmt.Println("---- result ----")
	fmt.Printf("requests=%d accepted=%d rejected=%d network-errors=%d\n", c.requests, accepted, rejected, neterr)
	fmt.Printf("executions=%d by status=%v\n", len(execs), statuses)
	fmt.Printf("success(unique requests)=%d missing=%d duplicates=%d not-success=%d\n", len(lat)-0, missing, dups, notSuccess)
	fmt.Printf("executions for requests the client did not see accepted (network error after queueing)=%d\n", orphans)
	fmt.Printf("throughput=%.1f exec/s (first request to last finish)\n", thr)
	fmt.Printf("latency p50=%.2fs p95=%.2fs p99=%.2fs max=%.2fs\n", pct(.5), pct(.95), pct(.99), pct(1))
	for i, p := range problems {
		if i == 20 {
			fmt.Printf("... %d more\n", len(problems)-20)
			break
		}
		fmt.Println("PROBLEM", p)
	}
	good := accepted > 0 && missing == 0 && dups == 0 && notSuccess == 0
	if good {
		fmt.Println("PASS")
	} else {
		fmt.Println("FAIL")
	}
	return good, nil
}

func rid(i int) string { return fmt.Sprintf("r%06d", i) }

func listAll(ctx context.Context, api *client, wf string) ([]execSummary, error) {
	var all []execSummary
	cursor := ""
	for {
		var page struct {
			Data       []execSummary `json:"data"`
			NextCursor string        `json:"nextCursor"`
		}
		p := "/api/v1/executions?limit=500&workflowId=" + wf
		if cursor != "" {
			p += "&cursor=" + url.QueryEscape(cursor)
		}
		if err := api.do(ctx, "GET", p, nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Data...)
		if page.NextCursor == "" {
			return all, nil
		}
		cursor = page.NextCursor
	}
}
