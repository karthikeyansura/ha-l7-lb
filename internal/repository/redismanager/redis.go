// Package redismanager provides distributed state coordination for
// horizontally scaled LB instances via Redis SET/GET and Pub/Sub.
// Enables real-time cross-instance health propagation so backends
// marked DOWN by one LB are immediately visible to all others.
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
	PubSubChannel = "lb-backend-events" // Message format: "URL|STATUS"
	KeyPrefix     = "backend:"           // Per-backend health key namespace.
)

// RedisManager coordinates health state across LB instances. Uses
// redis.UniversalClient to support both single-node and cluster mode.
type RedisManager struct {
	client redis.UniversalClient
	pool   repository.SharedState
}

// NewRedisManager connects to Redis, auto-detecting single-node vs. cluster
// mode by comma presence in addr. Returns error if PING fails.
func NewRedisManager(addr, password string, db int, pool repository.SharedState) (*RedisManager, error) {
	addrs := strings.Split(addr, ",")
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

// UpdateBackendStatus persists a backend's health to Redis (SET) and
// broadcasts it via Pub/Sub. A 2s timeout prevents blocking the proxy.
func (rm *RedisManager) UpdateBackendStatus(backendURL url.URL, status string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	urlStr := backendURL.String()
	key := KeyPrefix + urlStr

	err := rm.client.Set(ctx, key, status, 0).Err()
	if err != nil {
		return err
	}

	message := fmt.Sprintf("%s|%s", urlStr, status)
	return rm.client.Publish(ctx, PubSubChannel, message).Err()
}

// SyncOnStartUp reads health state from Redis for every backend and
// applies it locally. Handles LB restarts where backends were already
// marked DOWN by other instances.
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
			if err := rm.UpdateBackendStatus(backend.ServerURL, "UP"); err != nil {
				slog.Error("Failed to initialize backend state in Redis",
					"backend", backend.ServerURL.String(),
					"error", err)
			}
		} else {
			slog.Error("Redis error during sync, skipping backend",
				"backend", backend.ServerURL.String(),
				"error", err)
		}
	}
}

// StartPeriodicSync runs SyncOnStartUp on a ticker to heal state divergence.
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

// StartRedisWatcher subscribes to the Pub/Sub channel and applies
// incoming health state changes to the local pool. Runs for the
// lifetime of the process; go-redis handles automatic reconnection.
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

// Close terminates the Redis connection.
func (rm *RedisManager) Close() error {
	return rm.client.Close()
}
