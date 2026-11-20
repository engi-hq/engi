// Package kubernetes elects a leader among replicas with a
// coordination.k8s.io/v1 Lease object, using the in-cluster REST API and the
// pod's service-account token. It is a deliberately small client (get, create,
// update) so the binary does not link client-go.
//
// Protocol (the same one client-go's leaderelection uses): the holder writes
// its identity and a renew time into the Lease; a candidate may take over once
// it has watched the record stay unchanged for the lease duration on its own
// clock, so clock skew between nodes does not matter. Every write carries the
// resourceVersion it read, so two candidates cannot both win a takeover. The
// leader renews every RetryPeriod and steps down when it could not renew for
// RenewDeadline, always before others may take over (RenewDeadline < Duration).
package kubernetes

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/port"
)

const (
	// microTimeLayout is the metav1.MicroTime wire format.
	microTimeLayout = "2006-01-02T15:04:05.000000Z07:00"
	// requestTimeout bounds one API call.
	requestTimeout = 10 * time.Second
	// maxResponseBytes bounds the response bodies read from the API server.
	maxResponseBytes = 1 << 20
	// releaseTimeout bounds the hand-over on shutdown.
	releaseTimeout = 5 * time.Second
)

// Options are the injectable collaborators of a Provider; the zero value uses
// the real file system, clock, host name and an HTTP client trusting CAFile.
type Options struct {
	Logger   *slog.Logger
	Client   *http.Client
	ReadFile func(string) ([]byte, error)
	Now      func() time.Time
	Hostname func() (string, error)
}

// Provider is a port.LeaseProvider on a Kubernetes Lease.
type Provider struct {
	cfg      config.LeaseConfig
	log      *slog.Logger
	client   *http.Client
	readFile func(string) ([]byte, error)
	now      func() time.Time
	identity string
	path     string // /apis/coordination.k8s.io/v1/namespaces/<ns>/leases
}

var _ port.LeaseProvider = (*Provider)(nil)

// New resolves the namespace and identity and prepares the API client.
func New(cfg config.LeaseConfig, opt Options) (*Provider, error) {
	p := &Provider{cfg: cfg, log: opt.Logger, client: opt.Client, readFile: opt.ReadFile, now: opt.Now}
	if p.log == nil {
		p.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if p.readFile == nil {
		p.readFile = os.ReadFile
	}
	if p.now == nil {
		p.now = time.Now
	}
	host := opt.Hostname
	if host == nil {
		host = os.Hostname
	}

	ns := cfg.Namespace
	if ns == "" {
		b, err := p.readFile(cfg.NamespaceFile)
		if err != nil {
			return nil, fmt.Errorf("reading lease namespace: %w", err)
		}
		ns = strings.TrimSpace(string(b))
	}
	if ns == "" {
		return nil, errors.New("lease namespace is empty")
	}
	p.identity = cfg.Identity
	if p.identity == "" {
		h, err := host()
		if err != nil {
			return nil, fmt.Errorf("resolving lease identity: %w", err)
		}
		p.identity = h
	}
	p.path = strings.TrimRight(cfg.APIServer, "/") + "/apis/coordination.k8s.io/v1/namespaces/" + url.PathEscape(ns) + "/leases"

	if p.client == nil {
		c, err := p.httpClient()
		if err != nil {
			return nil, err
		}
		p.client = c
	}
	return p, nil
}

func (p *Provider) httpClient() (*http.Client, error) {
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if p.cfg.CAFile != "" && strings.HasPrefix(p.cfg.APIServer, "https://") {
		pem, err := p.readFile(p.cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("reading lease CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("lease CA file %s holds no certificate", p.cfg.CAFile)
		}
		tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &http.Client{Transport: tr, Timeout: requestTimeout}, nil
}

// record is the part of a Lease this provider reads and writes.
type record struct {
	holder          string
	renewTime       string
	resourceVersion string
	transitions     int
}

type leaseObject struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name            string `json:"name"`
		ResourceVersion string `json:"resourceVersion,omitempty"`
	} `json:"metadata"`
	Spec leaseSpec `json:"spec"`
}

type leaseSpec struct {
	HolderIdentity       *string `json:"holderIdentity"`
	LeaseDurationSeconds int     `json:"leaseDurationSeconds"`
	AcquireTime          string  `json:"acquireTime,omitempty"`
	RenewTime            string  `json:"renewTime,omitempty"`
	LeaseTransitions     int     `json:"leaseTransitions"`
}

// errConflict means the Lease changed (or appeared) since it was read.
var errConflict = errors.New("lease changed concurrently")

// errNotFound means the Lease does not exist yet.
var errNotFound = errors.New("lease not found")

// Run implements port.LeaseProvider. It returns nil once ctx is done.
func (p *Provider) Run(ctx context.Context, cb port.LeaderCallbacks) error {
	for ctx.Err() == nil {
		rec, ok := p.acquire(ctx)
		if !ok {
			return nil
		}
		p.lead(ctx, rec, cb)
	}
	return nil
}

// acquire campaigns until it holds the Lease (true) or ctx is done (false).
func (p *Provider) acquire(ctx context.Context) (record, bool) {
	var observed record
	var observedAt time.Time
	for {
		rec, err := p.tryAcquire(ctx, &observed, &observedAt)
		if err == nil {
			return rec, true
		}
		if ctx.Err() != nil {
			return record{}, false
		}
		if !errors.Is(err, errConflict) && !errors.Is(err, errHeld) {
			p.log.Warn("lease: acquire attempt failed", "error", err)
		}
		if !p.sleep(ctx) {
			return record{}, false
		}
	}
}

// errHeld means another live replica holds the Lease.
var errHeld = errors.New("lease held by another replica")

func (p *Provider) tryAcquire(ctx context.Context, observed *record, observedAt *time.Time) (record, error) {
	cur, err := p.get(ctx)
	if errors.Is(err, errNotFound) {
		return p.create(ctx)
	}
	if err != nil {
		return record{}, err
	}
	if cur.holder != observed.holder || cur.renewTime != observed.renewTime || cur.resourceVersion != observed.resourceVersion {
		*observed, *observedAt = cur, p.now()
	}
	free := cur.holder == "" || cur.holder == p.identity
	if !free && p.now().Sub(*observedAt) <= p.cfg.Duration {
		return record{}, errHeld
	}
	next := cur
	next.holder = p.identity
	if cur.holder != p.identity {
		next.transitions++
	}
	return p.update(ctx, next, cur.holder != p.identity)
}

// lead runs the callbacks while the Lease is renewed, then hands it back.
func (p *Provider) lead(ctx context.Context, rec record, cb port.LeaderCallbacks) {
	leaderCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if cb.OnStartedLeading != nil {
			cb.OnStartedLeading(leaderCtx)
		}
	}()
	p.log.Info("lease: acquired", "identity", p.identity)

	lastRenew := p.now()
	for leaderCtx.Err() == nil && p.sleep(leaderCtx) {
		next, err := p.update(leaderCtx, rec, false)
		if err == nil {
			rec, lastRenew = next, p.now()
			continue
		}
		if leaderCtx.Err() != nil {
			break
		}
		if errors.Is(err, errConflict) || p.now().Sub(lastRenew) >= p.cfg.RenewDeadline {
			p.log.Warn("lease: lost leadership", "error", err)
			rec.holder = "" // not ours any more, nothing to release
			break
		}
		p.log.Warn("lease: renew failed, retrying", "error", err)
	}

	cancel()
	<-done
	if rec.holder != "" {
		p.release(ctx, rec)
	}
	if cb.OnStoppedLeading != nil {
		cb.OnStoppedLeading()
	}
}

// release hands the Lease back so another replica need not wait for expiry.
func (p *Provider) release(ctx context.Context, rec record) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	rec.holder = ""
	if _, err := p.update(rctx, rec, false); err != nil {
		p.log.Warn("lease: releasing failed (it expires on its own)", "error", err)
	}
}

func (p *Provider) sleep(ctx context.Context) bool {
	t := time.NewTimer(p.cfg.RetryPeriod)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (p *Provider) get(ctx context.Context) (record, error) {
	var obj leaseObject
	if err := p.do(ctx, http.MethodGet, p.path+"/"+url.PathEscape(p.cfg.Name), nil, &obj); err != nil {
		return record{}, err
	}
	rec := record{resourceVersion: obj.Metadata.ResourceVersion, renewTime: obj.Spec.RenewTime, transitions: obj.Spec.LeaseTransitions}
	if obj.Spec.HolderIdentity != nil {
		rec.holder = *obj.Spec.HolderIdentity
	}
	return rec, nil
}

func (p *Provider) body(rec record, acquire bool) leaseObject {
	now := p.now().UTC().Format(microTimeLayout)
	var obj leaseObject
	obj.APIVersion, obj.Kind = "coordination.k8s.io/v1", "Lease"
	obj.Metadata.Name, obj.Metadata.ResourceVersion = p.cfg.Name, rec.resourceVersion
	holder := rec.holder
	obj.Spec = leaseSpec{
		HolderIdentity:       &holder,
		LeaseDurationSeconds: int((p.cfg.Duration + time.Second - 1) / time.Second),
		RenewTime:            now,
		LeaseTransitions:     rec.transitions,
	}
	if acquire {
		obj.Spec.AcquireTime = now
	}
	return obj
}

func (p *Provider) create(ctx context.Context) (record, error) {
	rec := record{holder: p.identity}
	obj := p.body(rec, true)
	var out leaseObject
	if err := p.do(ctx, http.MethodPost, p.path, obj, &out); err != nil {
		return record{}, err
	}
	rec.resourceVersion, rec.renewTime = out.Metadata.ResourceVersion, out.Spec.RenewTime
	return rec, nil
}

// update writes rec (with its resourceVersion) and returns the stored record.
func (p *Provider) update(ctx context.Context, rec record, acquire bool) (record, error) {
	obj := p.body(rec, acquire)
	var out leaseObject
	if err := p.do(ctx, http.MethodPut, p.path+"/"+url.PathEscape(p.cfg.Name), obj, &out); err != nil {
		return record{}, err
	}
	rec.resourceVersion, rec.renewTime = out.Metadata.ResourceVersion, out.Spec.RenewTime
	return rec, nil
}

// do performs one API call; 404 maps to errNotFound and 409 to errConflict.
func (p *Provider) do(ctx context.Context, method, target string, in, out any) error {
	var rd io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("encoding lease: %w", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		return fmt.Errorf("building lease request: %w", err)
	}
	tok, err := p.readFile(p.cfg.TokenFile)
	if err != nil {
		return fmt.Errorf("reading service-account token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("calling kubernetes API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("reading kubernetes API response: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return errNotFound
	case resp.StatusCode == http.StatusConflict:
		return errConflict
	case resp.StatusCode >= 300:
		return fmt.Errorf("kubernetes API %s %s: status %d", method, target, resp.StatusCode)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decoding lease: %w", err)
		}
	}
	return nil
}
