// Load balancer entry point. Wires configuration, routing algorithm, shared
// state, Redis coordination, health checking, metrics collection, and the
// L7 reverse proxy into a single process. A metrics HTTP server runs on
// port+1000. Graceful shutdown on SIGTERM drains connections and flushes
// metrics to disk (important for ECS task stop).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/karthikeyansura/ha-l7-lb/internal/algorithms"
	"github.com/karthikeyansura/ha-l7-lb/internal/config"
	"github.com/karthikeyansura/ha-l7-lb/internal/discovery"
	"github.com/karthikeyansura/ha-l7-lb/internal/health"
	"github.com/karthikeyansura/ha-l7-lb/internal/metrics"
	"github.com/karthikeyansura/ha-l7-lb/internal/proxy"
	"github.com/karthikeyansura/ha-l7-lb/internal/repository"
	"github.com/karthikeyansura/ha-l7-lb/internal/repository/redismanager"
)

var (
	configPath = flag.String("config", "config.yaml", "Path to config file")
	metricsOut = flag.String("metrics-out", "metrics.json", "Output file for metrics")
)

func main() {
	flag.Parse()

	config.Load(*configPath)

	route := config.AppConfig.Route

	var policy algorithms.Rule
	switch route.Policy {
	case "round-robin":
		policy = &algorithms.RoundRobin{}
	case "least-connections":
		policy = &algorithms.LeastConnectionsPolicy{}
	case "weighted":
		policy = &algorithms.Weighted{
			Weights: make(map[url.URL][]int),
		}
	default:
		slog.Error("Unknown policy", "policy", route.Policy)
		return
	}

	if len(route.Backends) == 0 {
		slog.Error("At least one backend must be configured to extract the discovery domain.")
		return
	}

	sharedState := repository.NewInMemory([]url.URL{}, []int{})

	ctx, cancelAll := context.WithCancel(context.Background())
	defer cancelAll()

	// Each DNS watcher resolves independently, enabling heterogeneous backends.
	for _, backend := range route.Backends {
		target, err := url.Parse(backend.Endpoint)
		if err != nil {
			slog.Error("Invalid backend endpoint URL format", "endpoint", backend.Endpoint, "error", err)
			return
		}
		discovery.StartDNSWatcher(ctx, target.Hostname(), target.Hostname(), target.Port(), target.Scheme, backend.Weight, sharedState)
		slog.Info("DNS watcher started", "hostname", target.Hostname(), "weight", backend.Weight)
	}

	// Redis is optional; if unavailable, health state is local-only (degraded mode).
	var updater health.StatusUpdater
	if config.AppConfig.RedisConfig != nil {
		redisConf := config.AppConfig.RedisConfig
		slog.Info(fmt.Sprintf("Connecting to Redis at %s...", redisConf.Addr))

		redisMgr, redisErr := redismanager.NewRedisManager(redisConf.Addr, redisConf.Password, redisConf.DB, sharedState)
		if redisErr != nil {
			slog.Warn(fmt.Sprintf("Redis unavailable, running in degraded mode (local-only health): %v", redisErr))
		} else {
			defer func(redisMgr *redismanager.RedisManager) {
				err := redisMgr.Close()
				if err != nil {
					slog.Error(fmt.Sprintf("Failed to close Redis manager: %v", err))
				}
			}(redisMgr)

			redisMgr.SyncOnStartUp()

			redisMgr.StartPeriodicSync(ctx, 30*time.Second)

			redisMgr.StartRedisWatcher(ctx)

			updater = redisMgr
		}
	} else {
		slog.Warn("No Redis config provided, running in degraded mode (local-only health)")
	}

	collector := metrics.NewCollector(route.Policy)

	// updater may be nil in degraded mode; the proxy nil-checks before use.
	lb := proxy.NewReverseProxy(
		sharedState,
		policy,
		collector,
		updater,
		config.AppConfig.LoadBalancer.Timeout,
		config.AppConfig.LoadBalancer.RetriesEnabled,
	)
	slog.Info("Proxy constructed", "retries_enabled", config.AppConfig.LoadBalancer.RetriesEnabled)

	checker := health.NewChecker(
		sharedState,
		updater,
		config.AppConfig.HealthCheck.Interval,
		config.AppConfig.HealthCheck.Timeout,
	)
	checker.Start(ctx)

	metricsServer := startMetricsServer(collector, sharedState, config.AppConfig.LoadBalancer.Port+1000)

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				healthyCount, _ := sharedState.GetHealthy()
				activeBackends := len(healthyCount)
				collector.RecordTimeSeriesPoint(activeBackends)
			}
		}
	}()

	addr := fmt.Sprintf(":%d", config.AppConfig.LoadBalancer.Port)

	server := &http.Server{Addr: addr, Handler: lb}

	done := make(chan bool, 1)
	setupGracefulShutdown(collector, *metricsOut, server, metricsServer, cancelAll, done)

	slog.Info(fmt.Sprintf("Load balancer starting on %s with %s policy", addr, route.Policy))
	slog.Info(fmt.Sprintf("Metrics available at http://localhost:%d/metrics", config.AppConfig.LoadBalancer.Port+1000))

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}

	<-done
}

// startMetricsServer exposes /metrics, /metrics/timeseries, /metrics/export,
// and /health/backends on the given port.
func startMetricsServer(collector *metrics.Collector, pool repository.SharedState, port int) *http.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		summary := collector.GetSummary()
		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(summary)
		if err != nil {
			http.Error(w, err.Error(), 500)
		}
	})

	mux.HandleFunc("/metrics/timeseries", func(w http.ResponseWriter, r *http.Request) {
		data := collector.GetTimeSeriesData()
		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(data)
		if err != nil {
			http.Error(w, err.Error(), 500)
		}
	})

	mux.HandleFunc("/metrics/export", func(w http.ResponseWriter, r *http.Request) {
		tmpFile := "/tmp/metrics_export.csv"
		err := collector.ExportCSV(tmpFile)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		http.ServeFile(w, r, tmpFile)
	})

	mux.HandleFunc("/health/backends", func(w http.ResponseWriter, r *http.Request) {
		backends, _ := pool.GetAllServers()

		type BackendStatus struct {
			URL       string `json:"url"`
			Healthy   bool   `json:"healthy"`
			LastCheck string `json:"last_check"`
		}

		statuses := make([]BackendStatus, len(backends))
		for i, b := range backends {
			statuses[i] = BackendStatus{
				URL:       b.ServerURL.String(),
				Healthy:   b.IsHealthy(),
				LastCheck: b.LastCheck.Format(time.RFC3339),
			}
		}

		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(statuses)
		if err != nil {
			return
		}
	})

	addr := fmt.Sprintf(":%d", port)
	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error(fmt.Sprintf("Metrics server error: %v", err))
		}
	}()
	return srv
}

// setupGracefulShutdown intercepts SIGINT/SIGTERM to drain connections
// and flush metrics before exit (ECS sends SIGTERM on task stop).
func setupGracefulShutdown(
	collector *metrics.Collector,
	outputFile string,
	server *http.Server,
	metricsServer *http.Server,
	cancelAll context.CancelFunc,
	done chan bool,
) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-c
		slog.Info("Shutting down gracefully...")

		cancelAll()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Error(fmt.Sprintf("HTTP server shutdown error: %v", err))
		}
		if err := metricsServer.Shutdown(shutdownCtx); err != nil {
			slog.Error(fmt.Sprintf("Metrics server shutdown error: %v", err))
		}

		summary := collector.GetSummary()
		data, err := json.MarshalIndent(summary, "", "  ")
		if err == nil {
			if err := os.WriteFile(outputFile, data, 0644); err != nil {
				slog.Error(fmt.Sprintf("Metrics cannot be saved to %s", outputFile))
			} else {
				slog.Info(fmt.Sprintf("Metrics saved to %s", outputFile))
			}
		}

		csvFile := outputFile + ".csv"
		if err := collector.ExportCSV(csvFile); err == nil {
			slog.Info(fmt.Sprintf("Time-series data saved to %s", csvFile))
		}

		done <- true
	}()
}
