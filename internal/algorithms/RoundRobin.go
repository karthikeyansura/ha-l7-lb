package algorithms

import (
	"errors"
	"net/http"
	"net/url"
	"sync/atomic"

	"github.com/karthikeyansura/ha-l7-lb/internal/repository"
)

// RoundRobin distributes requests sequentially across healthy backends.
//
// This is the stateless baseline for Experiment 1.
type RoundRobin struct {
	next uint64 // Lock-free atomic increment.
}

// GetTarget returns the next healthy backend in round-robin order.
func (r *RoundRobin) GetTarget(state *repository.SharedState, _ *http.Request) (url.URL, error) {
	servers, err := (*state).GetHealthy()
	if err != nil {
		return url.URL{}, err
	}
	if len(servers) == 0 {
		return url.URL{}, errors.New("no healthy server found")
	}

	// Atomically increment and get the next value safely across all goroutines.
	nextVal := atomic.AddUint64(&r.next, 1)

	// Modulo ensures we wrap around correctly. (nextVal - 1) gives us 0-based indexing.
	index := (nextVal - 1) % uint64(len(servers))

	return servers[index].ServerURL, nil
}
