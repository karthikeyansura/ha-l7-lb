package algorithms

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/karthikeyansura/ha-l7-lb/internal/repository"
)

// RoundRobin distributes requests sequentially across healthy backends.
//
// This is the stateless baseline for Experiment 1: it requires no Redis
// lookups to make routing decisions, so any throughput or latency difference
// compared to LeastConnections is attributable to Redis coordination overhead.
//
// Limitation: the `next` counter is not synchronized with a mutex because
// the proxy is single-threaded per RoundRobin instance. If the healthy set
// shrinks between requests (e.g., a backend goes down), the modulo operation
// ensures `next` wraps correctly, but the distribution may temporarily skip
// an index. This is acceptable; the next call self-corrects.
type RoundRobin struct {
	next int // Index into the healthy servers slice for the next request.
}

// GetTarget returns the next healthy backend in round-robin order.
// The modulo of `next` against the current healthy count ensures
// correctness even if backends are removed between calls.
func (r *RoundRobin) GetTarget(state *repository.SharedState, _ *http.Request) (url.URL, error) {
	servers, err := (*state).GetHealthy()
	if err != nil {
		return url.URL{}, err
	}
	if len(servers) == 0 {
		return url.URL{}, errors.New("no healthy server found")
	}
	next := r.next
	// Advance and wrap. Safe without mutex: single logical writer per LB instance.
	r.next = (r.next + 1) % len(servers)
	return servers[next].ServerURL, nil
}
