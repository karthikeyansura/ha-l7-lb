// Backend server for the HA L7 Load Balancer system.
//
// Provides endpoints:
//   - /health: unconditional 200 OK, used by the LB health checker.
//   - /api/data: primary workload endpoint with chaos injection support.
//   - /api/compute: CPU-bound workload (iterated SHA-256 hashing).
//   - /api/payload: large response body (1MB JSON) for bandwidth stress.
//   - /api/stream: chunked transfer over ~2 seconds for connection hold.
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
	"crypto/sha256"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
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

		if handleChaos(w, r, serverID) {
			return
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

	// CPU-bound workload: iterates SHA-256 hashing to consume CPU cycles.
	// Accepts an optional ?iterations=N query parameter (default 50000).
	// On a 256-CPU Fargate task this takes ~100-300ms, producing measurable
	// latency differences between strong and weak backends.
	//
	// Also honors chaos headers (X-Chaos-Error, X-Chaos-Delay) via handleChaos
	// for Experiment 2 CPU-heavy variant: same retry/failure semantics as
	// /api/data but on a workload that stresses backend CPU.
	http.HandleFunc("/api/compute", func(w http.ResponseWriter, r *http.Request) {
		if handleChaos(w, r, serverID) {
			return
		}

		iterations := 50000
		if v := r.URL.Query().Get("iterations"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500000 {
				iterations = n
			}
		}

		hash := sha256.Sum256([]byte(serverID))
		for i := 0; i < iterations; i++ {
			hash = sha256.Sum256(hash[:])
		}

		w.Header().Set("X-Backend-ID", serverID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `{"server":"%s","iterations":%d,"hash":"%x"}`, serverID, iterations, hash[:8])
	})

	// Large payload workload: returns a ~1MB JSON response body.
	// Exercises the proxy's response streaming path (io.Copy) and verifies
	// that large transfers complete without corruption or timeout.
	http.HandleFunc("/api/payload", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend-ID", serverID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		// ~1MB: 1024 lines of ~1000 chars each.
		_, _ = fmt.Fprintf(w, `{"server":"%s","lines":[`, serverID)
		line := strings.Repeat("x", 990)
		for i := 0; i < 1024; i++ {
			if i > 0 {
				_, _ = fmt.Fprint(w, ",")
			}
			_, _ = fmt.Fprintf(w, `"%s"`, line)
		}
		_, _ = fmt.Fprint(w, "]}")
	})

	// Chunked streaming workload: sends 10 chunks over ~2 seconds.
	// Holds the proxy connection open to test LeastConnections accuracy
	// under sustained concurrent connections. Stays within the 5s proxy timeout.
	http.HandleFunc("/api/stream", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("X-Backend-ID", serverID)
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Transfer-Encoding", "chunked")
		w.WriteHeader(http.StatusOK)

		for i := 0; i < 10; i++ {
			_, _ = fmt.Fprintf(w, "chunk %d from %s\n", i, serverID)
			flusher.Flush()
			time.Sleep(200 * time.Millisecond)
		}
	})

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("%s starting on %s", serverID, addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

// handleChaos honors the X-Chaos-Error and X-Chaos-Delay headers used by
// Experiment 2. Returns true if the request was fully handled (a forced
// error response was written), in which case the caller must return
// immediately. Returns false otherwise — any sleep from X-Chaos-Delay
// has already happened before return, and the caller continues with the
// endpoint's normal workload.
func handleChaos(w http.ResponseWriter, r *http.Request, serverID string) bool {
	// Chaos: forced error code. Checked before any processing.
	if chaos := r.Header.Get("X-Chaos-Error"); chaos != "" {
		code, err := strconv.Atoi(chaos)
		if err == nil && code >= 400 {
			log.Printf("[%s] Chaos: returning %d", serverID, code)
			w.WriteHeader(code)
			_, _ = fmt.Fprintf(w, "Chaos error %d from %s\n", code, serverID)
			return true
		}
	}

	// Chaos: artificial latency. Sleeps before response, may exceed
	// the proxy's timeout and trigger a retry.
	if delay := r.Header.Get("X-Chaos-Delay"); delay != "" {
		ms, err := strconv.Atoi(delay)
		if err == nil && ms > 0 {
			log.Printf("[%s] Chaos: sleeping %dms", serverID, ms)
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}
	}

	return false
}

// getLocalIP returns the first non-loopback, non-link-local IPv4 address
// found on the host. Used to construct a readable server ID.
func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "unknown"
	}
	for _, address := range addrs {
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil && !ipnet.IP.IsLinkLocalUnicast() {
				return ipnet.IP.String()
			}
		}
	}
	return "unknown"
}
