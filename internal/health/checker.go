package health

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/karthikeyansura/ha-l7-lb/internal/repository"
)

// Checker performs periodic active health checks against all registered
// backends via HTTP GET to /health. Only state transitions are reported
// to the StatusUpdater to avoid redundant writes.
type Checker struct {
	pool     *repository.InMemory
	updater  StatusUpdater
	interval time.Duration
	timeout  time.Duration
	client   *http.Client
	checking atomic.Bool
}

// NewChecker constructs a Checker with the given probe interval and timeout.
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

// Start runs an initial check, then repeats on a fixed interval until ctx is cancelled.
func (hc *Checker) Start(ctx context.Context) {
	hc.checkAll()
	go func() {
		ticker := time.NewTicker(hc.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				slog.Info("Health checker shutting down")
				return
			case <-ticker.C:
				hc.checkAll()
			}
		}
	}()
	slog.Info("Health checker started", "interval", hc.interval)
}

// checkAll probes every registered backend concurrently.
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

// checkBackend probes a single backend's /health endpoint. A 200 OK within
// the timeout is considered healthy; any other outcome marks it DOWN.
// Draining backends are skipped.
func (hc *Checker) checkBackend(backend *repository.ServerState) {
	if backend.IsDraining() {
		return
	}
	serverURL := backend.ServerURL.String() + "/health"
	resp, err := hc.client.Get(serverURL)

	var isHealthy bool
	if err != nil {
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

	if backend.IsHealthy() != isHealthy {
		slog.Info("Health Check", "backend", backend.ServerURL, "status", newStatus)

		hc.pool.MarkHealthy(backend.ServerURL, isHealthy)

		if hc.updater != nil {
			if err := hc.updater.UpdateBackendStatus(backend.ServerURL, newStatus); err != nil {
				slog.Error("Failed to update state", "backend", backend.ServerURL, "error", err)
			}
		}
	}
}
