// Package repository defines the shared state abstraction for backend
// server coordination across load balancer instances.
package repository

import "net/url"

// SharedState is the contract for all backend pool operations.
// All methods must be safe for concurrent use.
type SharedState interface {
	// GetAllServers returns all backends regardless of health status.
	GetAllServers() ([]*ServerState, error)

	// GetHealthy returns only backends where Healthy == true.
	GetHealthy() ([]*ServerState, error)

	// MarkHealthy sets the health flag for a specific backend.
	MarkHealthy(backendURL url.URL, healthy bool)

	// AddConnections increments the active connection counter.
	AddConnections(serverURL url.URL, connections int64)

	// RemoveConnections decrements the active connection counter.
	RemoveConnections(serverURL url.URL, connections int64)

	// SyncServers reconciles the pool with newly discovered DNS IPs.
	SyncServers(activeURLs []url.URL, defaultWeight int)

	// SyncServersBySource reconciles only the servers matching sourceTag,
	// preserving other sources' servers.
	SyncServersBySource(sourceTag string, activeURLs []url.URL, weight int)
}
