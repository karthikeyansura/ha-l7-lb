package health

import (
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/karthikeyansura/ha-l7-lb/internal/repository"
)

// Checker performs periodic active health checks against all registered
// backends by sending HTTP GET requests to their /health endpoint.
//
// On each tick, checkAll launches one goroutine per backend for concurrent
// probing. Only state transitions (healthy -> unhealthy or vice versa) are
// reported to the StatusUpdater to avoid redundant Redis writes.
//
// The Checker uses a concrete *InMemory rather than the SharedState
// interface because it needs GetAllServers (including unhealthy backends)
// to detect recovery, and the InMemory type is always the local store.
type Checker struct {
	pool     *repository.InMemory
	updater  StatusUpdater // Typically RedisManager; propagates changes to all LB instances.
	interval time.Duration // Time between full probe cycles.
	timeout  time.Duration // HTTP client timeout for each /health request.
	client   *http.Client
	checking atomic.Bool
}

// NewChecker constructs a Checker. The interval and timeout are read from
// config.yaml's health_check section.
func NewChecker(pool *repository.InMemory, updater StatusUpdater, interval, timeout time.Duration) *Checker {
	return &Checker{
		pool:     pool,
		updater:  updater,
		interval: interval,
		timeout:  timeout,
		client: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// Start runs an initial health check immediately, then launches a
// background goroutine that repeats on a fixed interval.
// This method does not block; the caller should invoke it with `go`.
func (hc *Checker) Start() {
	ticker := time.NewTicker(hc.interval)
	// Immediate first check so backends are validated before traffic arrives.
	hc.checkAll()
	go func() {
		for range ticker.C {
			hc.checkAll()
		}
	}()
	slog.Info("Health checker started", "interval", hc.interval)
}

// checkAll probes every registered backend concurrently.
// Each probe runs in its own goroutine to avoid a single slow backend
// delaying the entire cycle.
func (hc *Checker) checkAll() {
	if !hc.checking.CompareAndSwap(false, true) {
		return // previous wave still running — skip to prevent overlap
	}
	defer hc.checking.Store(false)

	backends, _ := hc.pool.GetAllServers()
	sem := make(chan struct{}, 10) // max 10 concurrent health probes
	var wg sync.WaitGroup

	for _, backend := range backends {
		wg.Add(1)
		sem <- struct{}{}
		go func(b *repository.ServerState) {
			defer wg.Done()
			defer func() { <-sem }()
			hc.checkBackend(b)
		}(backend)
	}
	wg.Wait()
}

// checkBackend performs a single HTTP GET to {backend}/health.
// A backend is considered healthy if and only if the response status is 200 OK
// within the configured timeout. Any other outcome (error, non-200 status,
// timeout) marks the backend as DOWN.
//
// State changes only: if the probed status matches the current Healthy flag,
// no update is published. This minimizes Redis write frequency.
func (hc *Checker) checkBackend(backend *repository.ServerState) {
	serverURL := backend.ServerURL.String() + "/health"
	resp, err := hc.client.Get(serverURL)

	var isHealthy bool
	if err != nil {
		// Connection refused, DNS failure, timeout — treat as unhealthy.
		isHealthy = false
	} else {
		defer func() {
			_ = resp.Body.Close()
		}()
		isHealthy = resp.StatusCode == http.StatusOK
	}

	newStatus := "DOWN"
	if isHealthy {
		newStatus = "UP"
	}

	// Update only on state transition.
	if backend.IsHealthy() != isHealthy {
		slog.Info("Health Check", "backend", backend.ServerURL, "status", newStatus)

		// Always update local state.
		hc.pool.MarkHealthy(backend.ServerURL, isHealthy)

		// Propagate to other LB instances via Redis if available.
		if hc.updater != nil {
			if err := hc.updater.UpdateBackendStatus(backend.ServerURL, newStatus); err != nil {
				slog.Error("Failed to update state", "backend", backend.ServerURL, "error", err)
			}
		}
	}
}
