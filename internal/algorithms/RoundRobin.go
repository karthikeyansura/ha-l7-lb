package algorithms

import (
	"errors"
	"net/http"
	"net/url"
	"sync/atomic"

	"github.com/karthikeyansura/ha-l7-lb/internal/repository"
)

// RoundRobin distributes requests sequentially across healthy backends
// using a lock-free atomic counter.
type RoundRobin struct {
	next uint64
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

	nextVal := atomic.AddUint64(&r.next, 1)
	index := (nextVal - 1) % uint64(len(servers))

	return servers[index].ServerURL, nil
}
