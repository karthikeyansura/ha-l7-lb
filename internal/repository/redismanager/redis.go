// Package redismanager provides distributed state coordination for
// horizontally scaled L7 load balancer instances.
//
// When one LB instance detects a backend failure (via health check or
// proxy timeout), it writes the new status to Redis and publishes a
// notification on a Pub/Sub channel. All other LB instances subscribe
// to this channel and update their local InMemory state immediately,
// eliminating the delay of waiting for their own health check cycle.
//
// This is the core mechanism enabling Experiment 3 (horizontal LB scaling):
// without it, each LB instance would have an independent, potentially
// inconsistent view of backend health.
package redismanager

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/karthikeyansura/ha-l7-lb/internal/repository"
	"github.com/redis/go-redis/v9"
)

const (
	// PubSubChannel is the Redis channel name for health state change events.
	// Message format: "URL|STATUS" where STATUS is "UP" or "DOWN".
	PubSubChannel = "lb-backend-events"

	// KeyPrefix namespaces Redis keys to avoid collisions.
	// Each backend has a key like "backend:http://host:port" storing "UP"/"DOWN".
	KeyPrefix = "backend:"
)

// RedisManager coordinates backend health state across LB instances.
// It implements the health.StatusUpdater interface, allowing the health
// checker and proxy to publish state changes without knowing about Redis.
//
// The client field uses redis.UniversalClient (not ClusterClient) to
// support both single-node Redis (AWS ElastiCache with 1 node) and
// Redis Cluster (3+ nodes) without code changes. The constructor
// auto-detects based on whether the addr string contains commas.
type RedisManager struct {
	client redis.UniversalClient
	pool   repository.SharedState
}

// NewRedisManager establishes a Redis connection and verifies reachability
// via PING with a 5-second timeout. Returns an error if Redis is unreachable;
// the caller decides how to handle it (currently: degrade to local-only health).
//
// Auto-detection logic: if addr contains commas (e.g., "host1:6379,host2:6379"),
// a ClusterClient is created. Otherwise, a single-node Client is used.
// This matters because AWS ElastiCache with num_cache_nodes=1 is not a cluster.
func NewRedisManager(addr, password string, db int, pool repository.SharedState) (*RedisManager, error) {
	addrs := strings.Split(addr, ",")

	// Single-node vs. cluster auto-detection based on address count.
	var client redis.UniversalClient
	if len(addrs) > 1 {
		client = redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:    addrs,
			Password: password,
		})
	} else {
		client = redis.NewClient(&redis.Options{
			Addr:     addrs[0],
			Password: password,
			DB:       db,
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	return &RedisManager{
		client: client,
		pool:   pool,
	}, nil
}

// UpdateBackendStatus persists a backend's health state to Redis and
// broadcasts it to all LB instances via Pub/Sub.
//
// This is a two-phase write:
//  1. SET the key so that newly starting LB instances can read it (SyncOnStartUp).
//  2. PUBLISH to the channel so already-running instances learn immediately.
//
// The 2-second context timeout prevents a slow Redis from blocking the
// proxy goroutine indefinitely. If Redis is unreachable, the local state
// has already been updated; only cross-instance propagation is lost.
func (rm *RedisManager) UpdateBackendStatus(backendURL url.URL, status string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	urlStr := backendURL.String()
	key := KeyPrefix + urlStr

	// Phase 1: persist to key for startup-time reads.
	err := rm.client.Set(ctx, key, status, 0).Err()
	if err != nil {
		return err
	}

	// Phase 2: broadcast for real-time propagation.
	message := fmt.Sprintf("%s|%s", urlStr, status)
	return rm.client.Publish(ctx, PubSubChannel, message).Err()
}

// SyncOnStartUp reads the current health state from Redis for every
// configured backend and applies it to the local InMemory pool.
//
// This handles the case where an LB instance starts (or restarts) while
// backends are already marked DOWN by other instances. Without this,
// the new instance would assume all backends are healthy and route
// traffic to failed servers until its own health checks catch up.
//
// If a backend has no key in Redis (first-ever deployment), it writes
// "UP" as the default, establishing the initial shared state.
func (rm *RedisManager) SyncOnStartUp() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	servers, err := rm.pool.GetAllServers()
	if err != nil {
		slog.Error(fmt.Sprintf("Failed to sync: could not get servers from pool: %v", err))
		return
	}
	for _, backend := range servers {
		key := KeyPrefix + backend.ServerURL.String()
		val, err := rm.client.Get(ctx, key).Result()
		if err == nil {
			healthy := val == "UP"
			rm.pool.MarkHealthy(backend.ServerURL, healthy)
			slog.Info(fmt.Sprintf("Synced %s from Redis: %v", backend.ServerURL.String(), healthy))
		} else if errors.Is(err, redis.Nil) {
			// Key does not exist (first deployment); initialize as UP.
			if err := rm.UpdateBackendStatus(backend.ServerURL, "UP"); err != nil {
				slog.Error("Failed to initialize backend state in Redis",
					"backend", backend.ServerURL.String(),
					"error", err)
			}
		} else {
			// Network error or other Redis failure — skip to prevent state corruption.
			slog.Error("Redis error during sync, skipping backend",
				"backend", backend.ServerURL.String(),
				"error", err)
		}
	}
}

// StartPeriodicSync runs SyncOnStartUp on a background ticker to heal
// any state divergence caused by missed Pub/Sub messages. This enforces
// eventual consistency without relying solely on fire-and-forget Pub/Sub.
func (rm *RedisManager) StartPeriodicSync(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				rm.SyncOnStartUp()
			}
		}
	}()
}

// StartRedisWatcher launches a background goroutine that subscribes to
// the Pub/Sub channel and applies incoming health state changes to
// the local InMemory pool.
//
// Message format is "URL|STATUS" (e.g., "http://backend-1:8080|DOWN").
// Malformed messages (wrong number of pipe-separated fields) are silently
// dropped. URL parse failures are logged and skipped.
//
// This goroutine runs for the lifetime of the process. If the Redis
// connection drops, the go-redis library automatically reconnects.
func (rm *RedisManager) StartRedisWatcher(ctx context.Context) {
	go func() {
		sub := rm.client.Subscribe(ctx, PubSubChannel)
		defer func() {
			_ = sub.Close()
		}()

		ch := sub.Channel()
		slog.Info("Started watching Redis Pub/Sub for changes...")

		for {
			select {
			case <-ctx.Done():
				slog.Info("Redis watcher shutting down")
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				parts := strings.Split(msg.Payload, "|")
				if len(parts) != 2 {
					continue
				}

				serverURL, err := url.Parse(parts[0])
				status := parts[1]
				healthy := status == "UP"
				if err != nil {
					slog.Error(fmt.Sprintf("Error parsing URL '%s' from Redis: %v", parts[0], err))
					continue
				}

				slog.Info(fmt.Sprintf("Redis update received: %s is %s", serverURL.String(), status))
				rm.pool.MarkHealthy(*serverURL, healthy)
			}
		}
	}()
}

// Close terminates the Redis client connection.
func (rm *RedisManager) Close() error {
	return rm.client.Close()
}
