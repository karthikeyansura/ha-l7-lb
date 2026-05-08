package repository

import (
	"log/slog"
	"net/url"
	"sync"
	"time"
)

// InMemory is the local implementation of SharedState. Cross-instance
// consistency is achieved via Redis Pub/Sub (see redismanager package).
// A sync.RWMutex protects the servers slice.
type InMemory struct {
	mu      sync.RWMutex
	servers []*ServerState
}

// NewInMemory constructs the pool from URLs and weights. All servers start healthy.
func NewInMemory(servers []url.URL, weights []int) *InMemory {
	serverStates := make([]*ServerState, 0, len(servers))
	for i, server := range servers {
		s := &ServerState{
			ServerURL: server,
			Weight:    weights[i],
			LastCheck: time.Now(),
		}
		s.SetHealthy(true)
		serverStates = append(serverStates, s)
	}
	return &InMemory{
		servers: serverStates,
	}
}

// GetAllServers returns a shallow copy of the server slice.
func (i *InMemory) GetAllServers() ([]*ServerState, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()

	result := make([]*ServerState, len(i.servers))
	copy(result, i.servers)

	return result, nil
}

// GetHealthy returns servers with Healthy == true.
func (i *InMemory) GetHealthy() ([]*ServerState, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()

	healthy := make([]*ServerState, 0)
	for _, s := range i.servers {
		if s.IsHealthy() {
			healthy = append(healthy, s)
		}
	}
	return healthy, nil
}

// MarkHealthy updates the Healthy flag and LastCheck timestamp.
func (i *InMemory) MarkHealthy(serverURL url.URL, healthy bool) {
	i.mu.Lock()
	defer i.mu.Unlock()

	for _, s := range i.servers {
		if s.ServerURL == serverURL {
			s.SetHealthy(healthy)
			s.LastCheck = time.Now()
			return
		}
	}
}

// AddConnections increments the active connection counter for a backend.
func (i *InMemory) AddConnections(serverURL url.URL, connections int64) {
	i.mu.Lock()
	defer i.mu.Unlock()

	for _, s := range i.servers {
		if s.ServerURL == serverURL {
			s.AddConnections(connections)
			return
		}
	}
}

// RemoveConnections decrements the active connection counter.
func (i *InMemory) RemoveConnections(serverURL url.URL, connections int64) {
	i.mu.Lock()
	defer i.mu.Unlock()

	for _, s := range i.servers {
		if s.ServerURL == serverURL {
			s.AddConnections(-connections)
			return
		}
	}
}

// SyncServers reconciles the pool with a newly discovered list of URLs.
func (i *InMemory) SyncServers(activeURLs []url.URL, defaultWeight int) {
	i.mu.Lock()
	defer i.mu.Unlock()

	activeSet := make(map[string]bool, len(activeURLs))
	for _, u := range activeURLs {
		activeSet[u.String()] = true
	}

	newServers := make([]*ServerState, 0, len(activeURLs))
	existingMap := make(map[string]*ServerState)

	for _, s := range i.servers {
		existingMap[s.ServerURL.String()] = s
	}

	for _, u := range activeURLs {
		urlStr := u.String()
		if existing, found := existingMap[urlStr]; found {
			existing.SetDraining(false)
			newServers = append(newServers, existing)
		} else {
			s := &ServerState{
				ServerURL:         u,
				Weight:            defaultWeight,
				LastCheck:         time.Now(),
				ActiveConnections: 0,
			}
			s.SetHealthy(true)
			newServers = append(newServers, s)
		}
	}

	// Drain backends no longer in DNS; drop those with zero connections.
	for _, s := range i.servers {
		if !activeSet[s.ServerURL.String()] {
			if s.GetActiveConnections() > 0 {
				s.SetDraining(true)
				s.SetHealthy(false)
				newServers = append(newServers, s)
			} else if s.IsDraining() {
				slog.Info("Draining backend removed (connections drained to 0)",
					"backend", s.ServerURL.String())
			}
		}
	}

	i.servers = newServers
}

// SyncServersBySource reconciles the pool for a single DNS source,
// preserving servers from other sources.
func (i *InMemory) SyncServersBySource(sourceTag string, activeURLs []url.URL, weight int) {
	i.mu.Lock()
	defer i.mu.Unlock()

	activeSet := make(map[string]bool, len(activeURLs))
	for _, u := range activeURLs {
		activeSet[u.String()] = true
	}

	existingMap := make(map[string]*ServerState)
	for _, s := range i.servers {
		if s.SourceTag == sourceTag {
			existingMap[s.ServerURL.String()] = s
		}
	}

	newServers := make([]*ServerState, 0, len(i.servers))
	for _, s := range i.servers {
		if s.SourceTag != sourceTag {
			newServers = append(newServers, s)
		}
	}

	for _, u := range activeURLs {
		urlStr := u.String()
		if existing, found := existingMap[urlStr]; found {
			existing.SetDraining(false)
			newServers = append(newServers, existing)
		} else {
			s := &ServerState{
				ServerURL:         u,
				Weight:            weight,
				LastCheck:         time.Now(),
				ActiveConnections: 0,
				SourceTag:         sourceTag,
			}
			s.SetHealthy(true)
			newServers = append(newServers, s)
		}
	}

	for _, s := range i.servers {
		if s.SourceTag == sourceTag && !activeSet[s.ServerURL.String()] {
			if s.GetActiveConnections() > 0 {
				s.SetDraining(true)
				s.SetHealthy(false)
				newServers = append(newServers, s)
			} else if s.IsDraining() {
				slog.Info("Draining backend removed (connections drained to 0)",
					"backend", s.ServerURL.String(), "source", sourceTag)
			}
		}
	}

	i.servers = newServers
}
