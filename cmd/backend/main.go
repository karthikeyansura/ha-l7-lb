// Backend server for the HA L7 Load Balancer system.
//
// Endpoints: /health (200 OK for health checks), /api/data (primary workload),
// /api/compute (CPU-bound SHA-256), /api/payload (~1MB response), /api/stream
// (chunked transfer). Chaos injection headers (X-Chaos-Error, X-Chaos-Delay)
// allow controlled fault injection on /api/data and /api/compute for retry
// and timeout experiments. A random 5-25ms baseline delay on /api/data
// simulates heterogeneous backend response times.
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

	serverID := fmt.Sprintf("Backend-%s-%d", getLocalIP(), *port)

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "OK")
	})

	http.HandleFunc("/api/data", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[%s] Received %s request from %s", serverID, r.Method, r.RemoteAddr)

		if handleChaos(w, r, serverID) {
			return
		}

		baseLatency := 5 + rand.Intn(20)
		time.Sleep(time.Duration(baseLatency) * time.Millisecond)

		w.Header().Set("X-Backend-ID", serverID)
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `{"server":"%s","status":"ok"}`, serverID)
	})

	// CPU-bound workload: iterates SHA-256 hashing. Accepts ?iterations=N (default 50000).
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

	// Large payload: returns ~1MB JSON response for bandwidth stress testing.
	http.HandleFunc("/api/payload", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend-ID", serverID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

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

	// Chunked streaming: 10 chunks over ~2s, tests long-lived proxy connections.
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

// handleChaos processes X-Chaos-Error and X-Chaos-Delay headers. Returns true
// if a forced error was written (caller must return), false otherwise.
func handleChaos(w http.ResponseWriter, r *http.Request, serverID string) bool {
	if chaos := r.Header.Get("X-Chaos-Error"); chaos != "" {
		code, err := strconv.Atoi(chaos)
		if err == nil && code >= 400 {
			log.Printf("[%s] Chaos: returning %d", serverID, code)
			w.WriteHeader(code)
			_, _ = fmt.Fprintf(w, "Chaos error %d from %s\n", code, serverID)
			return true
		}
	}

	if delay := r.Header.Get("X-Chaos-Delay"); delay != "" {
		ms, err := strconv.Atoi(delay)
		if err == nil && ms > 0 {
			log.Printf("[%s] Chaos: sleeping %dms", serverID, ms)
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}
	}

	return false
}

// getLocalIP returns the first non-loopback IPv4 address for the server ID.
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
