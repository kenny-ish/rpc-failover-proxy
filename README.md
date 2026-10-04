# rpc-failover-proxy

A local JSON-RPC proxy in front of several upstream providers. Clients use one URL, and the proxy
moves to the next provider when one fails.

```bash
go run . -listen :8545 -upstreams https://ethereum-rpc.publicnode.com,https://eth.drpc.org,https://1rpc.io/eth
curl -s localhost:8545 -H 'content-type: application/json' -d '{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}'
curl -s localhost:8545/health
```

Requests go to the first healthy upstream. On a network error, a timeout, HTTP 429 or a 5xx response,
that upstream is put on cooldown (`-cooldown`, 30 s by default) and the same request is retried on
the next one, so the client still gets one successful response.

- Batch requests are passed through unchanged.
- `-deny eth_sendRawTransaction,eth_sign` answers the listed methods with a JSON-RPC error. That's
  useful for a read-only service that should never be able to send transactions.
- `/health` shows the state, error count and remaining cooldown of each upstream.

It only uses the standard library. The tests run `httptest` servers that fail on purpose.

```bash
go test ./...
```
