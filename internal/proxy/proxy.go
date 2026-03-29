// Package proxy implements the Layer 7 reverse proxy with automatic
// retry logic for idempotent HTTP methods.
//
// Request lifecycle:
//  1. Buffer the request body (required for retries; the body stream
//     is consumed on the first read and must be replayed).
//  2. Check that at least one healthy backend exists (503 if not).
//  3. Select a backend via the configured algorithm.
//  4. Increment the backend's active connection counter.
//  5. Forward the request with a 2-second timeout.
//  6. On success: record metrics, return response to client.
//  7. On failure for idempotent methods (GET, PUT, DELETE):
//     a. Mark the failed backend as DOWN locally and via Redis.
//     b. Select a different backend from the remaining healthy set.
//     c. Retry once on the new backend.
//  8. On failure for non-idempotent methods (POST, PATCH):
//     fall through to a 504 Gateway Timeout. No retry is attempted
//     because re-executing a non-idempotent request could produce
//     duplicate side effects (e.g., double order creation).
//
// This retry-on-idempotent design is the core feature evaluated in
// Experiment 2 (Failure Isolation and Retry Efficacy under Chaos).
package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/karthikeyansura/ha-l7-lb/internal/algorithms"
	"github.com/karthikeyansura/ha-l7-lb/internal/health"
	"github.com/karthikeyansura/ha-l7-lb/internal/metrics"
	"github.com/karthikeyansura/ha-l7-lb/internal/repository"
)

const maxBodySize = 10 << 20 // 10 MB payload limit to prevent OOM on buffered retries

// ReverseProxy implements http.Handler. Each incoming request is routed
// to a backend, proxied, and optionally retried on failure.
type ReverseProxy struct {
	pool      repository.SharedState // Backend server pool (InMemory, synced via Redis).
	algo      algorithms.Rule        // Pluggable routing algorithm.
	collector *metrics.Collector     // Request-level metrics accumulator.
	updater   health.StatusUpdater   // Redis-backed health state propagator.
	transport http.RoundTripper      // HTTP transport for backend requests.
	timeout   time.Duration          // Backend request timeout from config.
}

// NewReverseProxy constructs a proxy wired to all subsystems.
func NewReverseProxy(pool repository.SharedState, algorithm algorithms.Rule, collector *metrics.Collector, updater health.StatusUpdater, timeout time.Duration) *ReverseProxy {
	return &ReverseProxy{
		pool:      pool,
		algo:      algorithm,
		collector: collector,
		updater:   updater,
		timeout:   timeout,
		transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 20,
			IdleConnTimeout:     90 * time.Second,
		},
	}
}

// ServeHTTP is the main request handler invoked by the HTTP server.
//
// Body buffering: the entire request body is read into memory before
// forwarding. This is necessary because io.ReadCloser is single-use;
// a retry requires replaying the body from the buffer. The trade-off
// is increased memory usage proportional to request body size.
func (lb *ReverseProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Buffer request body for potential retry replay.
	var bodyBytes []byte
	var err error
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)
		bodyBytes, err = io.ReadAll(r.Body)
		if err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "Failed to read request body", http.StatusInternalServerError)
			return
		}
		err := r.Body.Close()
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}

	// resetBody replaces the consumed body with a fresh reader from the buffer
	// and restores ContentLength so backends receive the correct header.
	resetBody := func(req *http.Request) {
		if bodyBytes != nil {
			req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			req.ContentLength = int64(len(bodyBytes))
		} else {
			req.Body = http.NoBody
			req.ContentLength = 0
		}
	}

	startTime := time.Now()

	// Early exit if no backends are available.
	healthyCheck, _ := lb.pool.GetHealthy()
	if len(healthyCheck) == 0 {
		http.Error(w, "No healthy backends", http.StatusServiceUnavailable)
		return
	}

	// Select backend via configured algorithm (round-robin, least-connections, weighted).
	backendURL, err := lb.algo.GetTarget(&lb.pool, r)
	if err != nil {
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}

	// Track active connection for LeastConnections routing accuracy.
	lb.pool.AddConnections(backendURL, 1)

	// First attempt.
	resetBody(r)
	err = lb.proxyRequest(w, r, &backendURL)
	lb.pool.RemoveConnections(backendURL, 1)

	if err == nil {
		lb.collector.RecordRequest(backendURL.String(), time.Since(startTime), true, false, false)
		return
	}

	slog.Error(fmt.Sprintf("Request to %s failed: %v", backendURL.String(), err))

	// Client-side disconnects are not backend failures; do not retry or mark DOWN.
	if isClientDisconnect(err) {
		lb.collector.RecordRequest(backendURL.String(), time.Since(startTime), false, false, false)
		return
	}

	// Retry logic: only for HTTP methods that are safe to re-execute.
	// GET, PUT, DELETE are idempotent per RFC 7231. POST, PATCH are not.
	if isIdempotent(r.Method) {

		// Debounce: only mark DOWN and propagate if the backend is still
		// considered healthy. Under concurrent failures, the first goroutine
		// to reach this point handles the update; subsequent ones skip it.
		alreadyDown := false
		if servers, sErr := lb.pool.GetAllServers(); sErr == nil {
			for _, s := range servers {
				if s.ServerURL == backendURL && !s.IsHealthy() {
					alreadyDown = true
					break
				}
			}
		}

		if !alreadyDown {
			lb.pool.MarkHealthy(backendURL, false)

			go func(u string) {
				if lb.updater != nil {
					serverURL, _ := url.Parse(u)
					err := lb.updater.UpdateBackendStatus(*serverURL, "DOWN")
					if err != nil {
						slog.Error(fmt.Sprintf("Failed to update Redis for %s: %v", u, err))
					}
				}
			}(backendURL.String())
		}

		// Re-fetch healthy backends to reflect the just-marked-DOWN state.
		freshHealthy, _ := lb.pool.GetHealthy()
		newBackendURL := lb.selectDifferent(freshHealthy, &backendURL, r)
		if newBackendURL != nil {
			slog.Info(fmt.Sprintf("Retrying idempotent request on %s...", newBackendURL))

			lb.pool.AddConnections(*newBackendURL, 1)
			defer lb.pool.RemoveConnections(*newBackendURL, 1)

			resetBody(r)
			retryStart := time.Now()

			err = lb.proxyRequest(w, r, newBackendURL)

			if err == nil {
				// Record as retried=true so the retry rate metric is accurate.
				lb.collector.RecordRequest(newBackendURL.String(), time.Since(retryStart), true, false, true)
				return
			}
			slog.Error(fmt.Sprintf("Retry on %s also failed: %v", newBackendURL.String(), err))
		}
	}

	// Both attempts failed, or method is non-idempotent.
	lb.collector.RecordRequest(backendURL.String(), time.Since(startTime), false, true, false)
	http.Error(w, "Gateway Timeout", http.StatusGatewayTimeout)
}

// proxyRequest forwards a single HTTP request to the destination backend.
// A 2-second context timeout prevents slow backends from holding proxy
// goroutines indefinitely. On timeout, a typed TimeoutError is returned
// so callers can distinguish timeouts from connection failures.
func (lb *ReverseProxy) proxyRequest(w http.ResponseWriter, r *http.Request, destURL *url.URL) error {
	ctx, cancel := context.WithTimeout(r.Context(), lb.timeout)
	defer cancel()

	// Clone request with the timeout context.
	outReq := r.WithContext(ctx)

	// Rewrite URL to target the selected backend.
	outReq.URL.Scheme = destURL.Scheme
	outReq.URL.Host = destURL.Host
	outReq.Host = destURL.Host
	outReq.RequestURI = "" // Required by http.Transport: must not be set on client requests.

	resp, err := lb.transport.RoundTrip(outReq)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return TimeoutError{URL: destURL.String()}
		}
		return err
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			return
		}
	}(resp.Body)

	// Treat 5xx responses as backend errors to trigger retry logic.
	// RoundTrip succeeds for 5xx (it's a valid HTTP response), but the
	// proxy should retry idempotent requests on another backend.
	if resp.StatusCode >= 500 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return BackendError{URL: destURL.String(), StatusCode: resp.StatusCode}
	}

	// Stream response headers and body back to the client.
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, err = io.Copy(w, resp.Body)
	if err != nil {
		return err
	}

	return nil
}

// selectDifferent picks a retry target from the healthy backends, excluding
// the one that just failed. Instead of creating an ephemeral pool (which
// would reset connection counters and break least-connections), this selects
// directly from the existing ServerState pointers, preserving live state.
func (lb *ReverseProxy) selectDifferent(backends []*repository.ServerState, exclude *url.URL, _ *http.Request) *url.URL {
	var candidates []*repository.ServerState
	for _, b := range backends {
		if b.ServerURL.String() != exclude.String() && b.IsHealthy() {
			candidates = append(candidates, b)
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	// Pick the candidate with the fewest active connections.
	// This respects real counters for least-connections, and is a
	// reasonable choice for round-robin/weighted as well.
	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.GetActiveConnections() < best.GetActiveConnections() {
			best = c
		}
	}

	u := best.ServerURL
	return &u
}

// isIdempotent returns true for methods safe to retry (GET, PUT, DELETE).
func isIdempotent(method string) bool {
	return method == "GET" || method == "PUT" || method == "DELETE"
}

// isClientDisconnect returns true if the error was caused by the client
// disconnecting (cancelled context, broken pipe, connection reset).
// These are not backend failures and should not trigger DOWN events.
func isClientDisconnect(err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	errMsg := err.Error()
	return strings.Contains(errMsg, "broken pipe") ||
		strings.Contains(errMsg, "connection reset by peer")
}

// TimeoutError indicates a backend request exceeded the 2-second deadline.
type TimeoutError struct {
	URL string
}

// BackendError indicates the backend returned a server error (5xx).
type BackendError struct {
	URL        string
	StatusCode int
}

func (e BackendError) Error() string {
	return fmt.Sprintf("backend %s returned %d", e.URL, e.StatusCode)
}

func (e TimeoutError) Error() string {
	return fmt.Sprintf("timeout calling %s", e.URL)
}

// copyHeaders transfers all response headers from the backend to the client.
func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}
