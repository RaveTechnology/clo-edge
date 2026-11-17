// Package provider keeps the edge-specific CLO attachment contract intact.
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/RaveTechnology/clo-edge/internal/edge"
	sdk "github.com/clo-ru/cloapi-go-client/v3"
)

type Client struct{ sdk *sdk.ClientWithResponses }

func New(token string) (*Client, error) {
	return newClient(token, "https://api.clo.ru", &http.Client{Timeout: 15 * time.Second, Transport: &Pacer{Base: &http.Transport{Proxy: nil}, Interval: 200 * time.Millisecond}})
}
func newClient(token, base string, h *http.Client) (*Client, error) {
	if strings.TrimSpace(token) == "" || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("valid token file required")
	}
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("CLO redirect refused") }
	if h.Timeout == 0 {
		h.Timeout = 15 * time.Second
	}
	c, e := sdk.New(token, sdk.WithBaseURL(base), sdk.WithHTTPClient(h), sdk.WithRetry(0, 0), sdk.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	if e != nil {
		return nil, errors.New("cannot initialize CLO client")
	}
	return &Client{sdk: c}, nil
}

type page[T any] struct {
	Count  *int `json:"count"`
	Result []T  `json:"result"`
}

func read[T any](r *http.Response, e error) (T, error) {
	var out T
	if e != nil {
		return out, errors.New("CLO transport failed")
	}
	if r == nil {
		return out, errors.New("missing CLO response")
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		if r.StatusCode == 404 {
			return out, fmt.Errorf("%w: configured resource is missing", edge.ErrUnsafeCloudState)
		}
		return out, fmt.Errorf("CLO read HTTP %d", r.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if e != nil || len(b) > 1<<20 {
		return out, errors.New("invalid CLO response size")
	}
	if e = json.Unmarshal(b, &out); e != nil {
		return out, errors.New("invalid CLO JSON response")
	}
	return out, nil
}
func complete[T any](ctx context.Context, fetch func(int) (*http.Response, error), id func(T) string) ([]T, error) {
	var out []T
	seen := map[string]bool{}
	total := -1
	for offset := 0; offset < 100000; offset += 100 {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		r, e := fetch(offset)
		p, e := read[page[T]](r, e)
		if e != nil {
			return nil, e
		}
		if p.Count == nil || *p.Count < 0 || *p.Count > 100000 || len(p.Result) > 100 || offset+len(p.Result) > *p.Count || (total >= 0 && total != *p.Count) {
			return nil, errors.New("inconsistent CLO inventory count")
		}
		total = *p.Count
		for _, v := range p.Result {
			k := id(v)
			if k == "" || seen[k] {
				return nil, errors.New("duplicate/missing CLO inventory identity")
			}
			seen[k] = true
			out = append(out, v)
		}
		if len(out) == total {
			return out, nil
		}
		if len(p.Result) != 100 {
			return nil, errors.New("incomplete CLO inventory")
		}
	}
	return nil, errors.New("CLO inventory bound exceeded")
}
func (c *Client) Snapshot(ctx context.Context, cfg edge.Config) (edge.View, error) {
	var v edge.View
	r, e := c.sdk.ProjectDetail(ctx, cfg.ProjectID)
	proj, e := read[struct {
		Result *sdk.ProjectDetailSchema `json:"result"`
	}](r, e)
	if e != nil {
		return v, e
	}
	if proj.Result == nil || proj.Result.Id != cfg.ProjectID || proj.Result.Status != "ACTIVE" {
		return v, fmt.Errorf("%w: project is not active or identity changed", edge.ErrUnsafeCloudState)
	}
	servers, e := complete[sdk.ServerSchema](ctx, func(offset int) (*http.Response, error) {
		return c.sdk.ProjectServerList(ctx, cfg.ProjectID, sdk.WithPage(100, offset))
	}, func(v sdk.ServerSchema) string { return v.Id })
	if e != nil {
		return v, e
	}
	addresses, e := complete[sdk.AddressSchema](ctx, func(offset int) (*http.Response, error) {
		return c.sdk.ProjectAddressesList(ctx, cfg.ProjectID, sdk.WithPage(100, offset))
	}, func(v sdk.AddressSchema) string { return v.Id })
	if e != nil {
		return v, e
	}
	present := false
	for _, a := range addresses {
		if a.Id == cfg.AddressID {
			present = true
		}
	}
	if !present {
		return v, fmt.Errorf("%w: configured IPv4 not in project inventory", edge.ErrUnsafeCloudState)
	}
	membership := map[string]bool{}
	for _, s := range servers {
		if s.Project != cfg.ProjectID {
			return v, fmt.Errorf("%w: foreign server in project inventory", edge.ErrUnsafeCloudState)
		}
		membership[s.Id] = true
	}
	v.Servers = map[string]edge.Server{}
	for _, id := range []string{cfg.ControllerServerID, cfg.Edges[0].ID, cfg.Edges[1].ID} {
		if !membership[id] {
			return v, fmt.Errorf("%w: configured server not in project inventory", edge.ErrUnsafeCloudState)
		}
		r, e := c.sdk.ServerDetail(ctx, id)
		s, e := read[sdk.ServerDetailSchema](r, e)
		if e != nil {
			return v, e
		}
		if s.Result == nil {
			return v, errors.New("missing server detail")
		}
		v.Servers[id] = edge.Server{ID: s.Result.Id, ProjectID: s.Result.Project, Status: s.Result.Status, SwitchStatus: s.Result.SwitchStatus}
	}
	r, e = c.sdk.AddressDetail(ctx, cfg.AddressID)
	a, e := read[sdk.AddressDetailSchema](r, e)
	if e != nil {
		return v, e
	}
	if a.Result == nil {
		return v, errors.New("missing IPv4 detail")
	}
	x := a.Result
	v.Address = edge.Address{ID: x.Id, Status: x.Status, Type: x.Type, External: x.External}
	if x.Address != nil {
		v.Address.IP = *x.Address
	}
	if x.Version != nil {
		v.Address.Version = *x.Version
	}
	if x.BandwidthMaxMbps != nil {
		v.Address.Bandwidth = *x.BandwidthMaxMbps
	}
	if x.AttachedTo != nil {
		v.Address.Entity = x.AttachedTo.Entity
		v.Address.Owner = x.AttachedTo.Id
	}
	return v, v.Validate(cfg)
}
func accepted(r *http.Response, e error) error {
	if e != nil {
		return errors.New("CLO mutation outcome uncertain")
	}
	if r == nil {
		return errors.New("CLO mutation response missing")
	}
	defer r.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(r.Body, 4097))
	if readErr != nil || len(body) > 4096 || (len(strings.TrimSpace(string(body))) != 0 && !json.Valid(body)) {
		return errors.New("CLO mutation acknowledgement incomplete or malformed; outcome uncertain")
	}
	if r.StatusCode != 202 {
		return fmt.Errorf("CLO mutation not accepted (HTTP %d); reconcile manually", r.StatusCode)
	}
	return nil
}
func (c *Client) Stop(ctx context.Context, id string) error {
	r, e := c.sdk.ServerStop(ctx, id)
	return accepted(r, e)
}
func (c *Client) Detach(ctx context.Context, id string) error {
	r, e := c.sdk.AddressDetach(ctx, id)
	return accepted(r, e)
}
func (c *Client) Attach(ctx context.Context, address, target string, bandwidth int) error {
	b := sdk.AddressAttachJSONBodyBandwidthMaxMbps(bandwidth)
	r, e := c.sdk.AddressAttach(ctx, address, sdk.AddressAttachJSONRequestBody{Entity: "server", Id: target, BandwidthMaxMbps: &b})
	return accepted(r, e)
}

// Pacer bounds total request pressure. It does not retry a request of any method.
type Pacer struct {
	Base     http.RoundTripper
	Interval time.Duration
	mu       sync.Mutex
	next     time.Time
}

func (p *Pacer) RoundTrip(r *http.Request) (*http.Response, error) {
	p.mu.Lock()
	wait := time.Until(p.next)
	if wait < 0 {
		wait = 0
	}
	p.next = time.Now().Add(wait + p.Interval)
	p.mu.Unlock()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-r.Context().Done():
		return nil, r.Context().Err()
	case <-timer.C:
	}
	return p.Base.RoundTrip(r)
}
