package repository

import (
	"net/url"
	"sync/atomic"
	"time"
)

// ServerState represents the runtime state of a single backend server.
// Healthy and Draining use atomic.Bool for lock-free reads; ActiveConnections
// uses atomic operations for concurrent access from proxy goroutines.
type ServerState struct {
	ServerURL         url.URL
	Weight            int
	Healthy           atomic.Bool
	LastCheck         time.Time
	ActiveConnections int64       `redis:"active_connections"`
	Draining          atomic.Bool
	SourceTag         string
}

func (s *ServerState) IsHealthy() bool {
	return s.Healthy.Load()
}

func (s *ServerState) SetHealthy(healthy bool) {
	s.Healthy.Store(healthy)
}

// GetActiveConnections returns the in-flight request count via atomic load.
func (s *ServerState) GetActiveConnections() int64 {
	return atomic.LoadInt64(&s.ActiveConnections)
}

func (s *ServerState) AddConnections(connections int64) {
	atomic.AddInt64(&s.ActiveConnections, connections)
}

func (s *ServerState) IsDraining() bool {
	return s.Draining.Load()
}

func (s *ServerState) SetDraining(draining bool) {
	s.Draining.Store(draining)
}
