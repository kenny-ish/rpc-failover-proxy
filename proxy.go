package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

type upstream struct {
	URL       string    `json:"url"`
	Errors    int       `json:"errors"`
	DownUntil time.Time `json:"-"`
}

type Proxy struct {
	mu        sync.Mutex
	upstreams []*upstream
	deny      map[string]bool
	cooldown  time.Duration
	client    *http.Client
	now       func() time.Time
}

func NewProxy(urls []string, deny []string, cooldown, timeout time.Duration) *Proxy {
	p := &Proxy{deny: map[string]bool{}, cooldown: cooldown, client: &http.Client{Timeout: timeout}, now: time.Now}
	for _, u := range urls {
		p.upstreams = append(p.upstreams, &upstream{URL: u})
	}
	for _, m := range deny {
		if m != "" {
			p.deny[m] = true
		}
	}
	return p
}

// methods extracts method names from a single or batch JSON-RPC request.
func methods(body []byte) ([]string, error) {
	type req struct {
		Method string `json:"method"`
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var batch []req
		if err := json.Unmarshal(trimmed, &batch); err != nil {
			return nil, err
		}
		out := make([]string, len(batch))
		for i, r := range batch {
			out[i] = r.Method
		}
		return out, nil
	}
	var r req
	if err := json.Unmarshal(trimmed, &r); err != nil {
		return nil, err
	}
	return []string{r.Method}, nil
}

func (p *Proxy) candidates() []*upstream {
	p.mu.Lock()
	defer p.mu.Unlock()
	var up, down []*upstream
	for _, u := range p.upstreams {
		if p.now().Before(u.DownUntil) {
			down = append(down, u)
		} else {
			up = append(up, u)
		}
	}
	return append(up, down...) // try cooled-down ones last rather than failing outright
}

func (p *Proxy) markDown(u *upstream) {
	p.mu.Lock()
	u.Errors++
	u.DownUntil = p.now().Add(p.cooldown)
	p.mu.Unlock()
}

func rpcError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"jsonrpc":"2.0","id":null,"error":{"code":%d,"message":%q}}`, code, msg)
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/health" {
		p.health(w)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST JSON-RPC only", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
	if err != nil {
		rpcError(w, -32700, "read error")
		return
	}
	ms, err := methods(body)
	if err != nil {
		rpcError(w, -32700, "parse error")
		return
	}
	for _, m := range ms {
		if p.deny[m] {
			rpcError(w, -32601, "method "+m+" is not allowed by this proxy")
			return
		}
	}
	for _, u := range p.candidates() {
		resp, err := p.client.Post(u.URL, "application/json", bytes.NewReader(body))
		if err != nil {
			p.markDown(u)
			continue
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			p.markDown(u)
			continue
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Upstream", u.URL)
		w.WriteHeader(resp.StatusCode)
		w.Write(data)
		return
	}
	w.WriteHeader(http.StatusBadGateway)
	rpcError(w, -32000, "all upstreams failed")
}

func (p *Proxy) health(w http.ResponseWriter) {
	p.mu.Lock()
	defer p.mu.Unlock()
	type status struct {
		URL          string `json:"url"`
		Healthy      bool   `json:"healthy"`
		Errors       int    `json:"errors"`
		CooldownSecs int    `json:"cooldown_seconds"`
	}
	out := make([]status, len(p.upstreams))
	for i, u := range p.upstreams {
		left := u.DownUntil.Sub(p.now())
		if left < 0 {
			left = 0
		}
		out[i] = status{u.URL, left == 0, u.Errors, int(left.Seconds())}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
