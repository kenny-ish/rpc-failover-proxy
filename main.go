package main

import (
	"flag"
	"log"
	"net/http"
	"strings"
	"time"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8545", "listen address")
	ups := flag.String("upstreams", "", "comma-separated upstream RPC URLs, in priority order")
	deny := flag.String("deny", "", "comma-separated JSON-RPC methods to reject")
	cooldown := flag.Duration("cooldown", 30*time.Second, "how long a failed upstream is skipped")
	timeout := flag.Duration("timeout", 10*time.Second, "per-upstream request timeout")
	flag.Parse()
	if *ups == "" {
		log.Fatal("-upstreams is required")
	}
	p := NewProxy(strings.Split(*ups, ","), strings.Split(*deny, ","), *cooldown, *timeout)
	log.Printf("proxying %s -> %s", *listen, *ups)
	log.Fatal(http.ListenAndServe(*listen, p))
}
