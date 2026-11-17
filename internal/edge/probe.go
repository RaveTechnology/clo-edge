package edge

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

type Prober struct{ Timeout time.Duration }

func (p *Prober) request(ctx context.Context, u string, tr *http.Transport, identity string) bool {
	defer tr.CloseIdleConnections()
	h := http.Client{Transport: tr, Timeout: p.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if e != nil {
		return false
	}
	resp, e := h.Do(req)
	if e != nil {
		return false
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if e != nil || len(b) > 4096 || resp.StatusCode != 200 {
		return false
	}
	return identity == "" || string(b) == identity+"\n"
}
func (p *Prober) private(ctx context.Context, u, id string) bool {
	return p.request(ctx, u, &http.Transport{DisableKeepAlives: true}, id)
}
func (p *Prober) public(ctx context.Context, c Config, dial string) bool {
	var roots *x509.CertPool
	if c.CAFile != "" {
		b, e := os.ReadFile(c.CAFile)
		if e != nil {
			return false
		}
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(b) {
			return false
		}
	}
	tr := &http.Transport{DisableKeepAlives: true, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: c.PublicHost}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: p.Timeout}).DialContext(ctx, network, dial)
	}}
	return p.request(ctx, "https://"+c.PublicHost+c.HealthPath, tr, "")
}
func (p *Prober) Observe(ctx context.Context, c Config) Observation {
	var out Observation
	out.Private = map[string]bool{}
	var wg sync.WaitGroup
	var mu sync.Mutex
	wg.Go(func() {
		v := p.public(ctx, c, net.JoinHostPort(c.PublicIP, "443"))
		mu.Lock()
		out.Public = v
		mu.Unlock()
	})
	for _, edge := range c.Edges {
		wg.Go(func() {
			v := p.private(ctx, "http://"+net.JoinHostPort(edge.PrivateIP, "8405")+"/healthz", edge.ID)
			mu.Lock()
			out.Private[edge.ID] = v
			mu.Unlock()
		})
	}
	wg.Wait()
	out.At = time.Now()
	return out
}
