package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func upstreamServer(status int, body string, hits *int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
}

func call(t *testing.T, p *Proxy, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

const req = `{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}`

func TestFailoverAndCooldown(t *testing.T) {
	var badHits, goodHits int
	bad := upstreamServer(503, "down", &badHits)
	good := upstreamServer(200, `{"jsonrpc":"2.0","id":1,"result":"0x10"}`, &goodHits)
	defer bad.Close()
	defer good.Close()

	p := NewProxy([]string{bad.URL, good.URL}, nil, time.Minute, time.Second)
	rec, out := call(t, p, req)
	if rec.Code != 200 || out["result"] != "0x10" || rec.Header().Get("X-Upstream") != good.URL {
		t.Fatalf("expected failover to good upstream, got %d %v", rec.Code, out)
	}
	call(t, p, req)
	if badHits != 1 || goodHits != 2 {
		t.Fatalf("bad upstream should be on cooldown: bad=%d good=%d", badHits, goodHits)
	}
}

func TestCooldownExpires(t *testing.T) {
	var badHits, goodHits int
	bad := upstreamServer(429, "slow down", &badHits)
	good := upstreamServer(200, `{"result":"0x1"}`, &goodHits)
	defer bad.Close()
	defer good.Close()
	p := NewProxy([]string{bad.URL, good.URL}, nil, time.Second, time.Second)
	clock := time.Now()
	p.now = func() time.Time { return clock }
	call(t, p, req)
	clock = clock.Add(2 * time.Second)
	call(t, p, req)
	if badHits != 2 {
		t.Fatalf("bad upstream should be retried after cooldown, hits=%d", badHits)
	}
}

func TestDenylistIncludingBatch(t *testing.T) {
	var hits int
	up := upstreamServer(200, `{}`, &hits)
	defer up.Close()
	p := NewProxy([]string{up.URL}, []string{"eth_sendRawTransaction"}, time.Minute, time.Second)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`[{"method":"eth_chainId"},{"method":"eth_sendRawTransaction"}]`)))
	if !strings.Contains(rec.Body.String(), "not allowed") || hits != 0 {
		t.Fatalf("denied method must not be forwarded: %s hits=%d", rec.Body.String(), hits)
	}
}

func TestAllUpstreamsDown(t *testing.T) {
	var hits int
	bad := upstreamServer(500, "", &hits)
	defer bad.Close()
	p := NewProxy([]string{bad.URL}, nil, time.Minute, time.Second)
	rec, _ := call(t, p, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code %d", rec.Code)
	}
}
