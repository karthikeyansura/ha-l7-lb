// Backend server for the HA L7 Load Balancer system.
//
// Provides two endpoints:
//   - /health: unconditional 200 OK, used by the LB health checker.
//   - /api/data: primary workload endpoint with chaos injection support.
//
// Chaos injection (Experiment 2): the /api/data handler inspects two
// request headers to simulate failure modes:
//   - X-Chaos-Error: if set to an integer >= 400, the backend returns
//     that HTTP status code immediately. Used to test L7 retry behavior.
//   - X-Chaos-Delay: if set to an integer > 0, the backend sleeps for
//     that many milliseconds before responding. Used to test proxy
//     timeout handling and retry on deadline exceeded.
//
// These headers are injected by the ChaosInjectionUser Locust class.
// In production, backends would not have this mechanism; it exists
// solely for controlled fault injection experiments.
//
// Additionally, every /api/data response includes a random 5-25ms
// processing delay to simulate heterogeneous workloads (backends with
// varying response times), which affects how LeastConnections routing
// distributes load compared to RoundRobin.
package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"time"
)

var (
	port = flag.Int("port", 8080, "Backend server port")
)

func main() {
	flag.Parse()

	// Server ID encodes IP and port for traceability in logs and responses.
	serverID := fmt.Sprintf("Backend-%s-%d", getLocalIP(), *port)

	// Health endpoint: unconditional 200. The LB health checker calls this
	// periodically; any non-200 or timeout marks the backend as DOWN.
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "OK")
	})

	// Primary workload endpoint with chaos injection hooks.
	http.HandleFunc("/api/data", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[%s] Received %s request from %s", serverID, r.Method, r.RemoteAddr)

		// Chaos: forced error code. Checked before any processing.
		if chaos := r.Header.Get("X-Chaos-Error"); chaos != "" {
			code, err := strconv.Atoi(chaos)
			if err == nil && code >= 400 {
				log.Printf("[%s] Chaos: returning %d", serverID, code)
				w.WriteHeader(code)
				_, _ = fmt.Fprintf(w, "Chaos error %d from %s\n", code, serverID)
				return
			}
		}

		// Chaos: artificial latency. Sleeps before response, may exceed
		// the proxy's 2-second timeout and trigger a retry.
		if delay := r.Header.Get("X-Chaos-Delay"); delay != "" {
			ms, err := strconv.Atoi(delay)
			if err == nil && ms > 0 {
				log.Printf("[%s] Chaos: sleeping %dms", serverID, ms)
				time.Sleep(time.Duration(ms) * time.Millisecond)
			}
		}

		// Baseline variable latency (5-25ms) simulating real workload variance.
		baseLatency := 5 + rand.Intn(20)
		time.Sleep(time.Duration(baseLatency) * time.Millisecond)

		// X-Backend-ID header enables tracing which backend served a request,
		// useful for verifying routing algorithm distribution in experiments.
		w.Header().Set("X-Backend-ID", serverID)
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `{"server":"%s","status":"ok"}`, serverID)
	})

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("%s starting on %s", serverID, addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

// getLocalIP returns the first non-loopback IPv4 address found on the host.
// Used to construct a readable server ID.
func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "unknown"
	}
	for _, address := range addrs {
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return "unknown"
}
