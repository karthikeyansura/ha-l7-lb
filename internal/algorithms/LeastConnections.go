package algorithms

import (
	"errors"
	"math"
	"math/rand"
	"net/http"
	"net/url"

	"github.com/karthikeyansura/ha-l7-lb/internal/repository"
)

// LeastConnectionsPolicy selects the healthy backend with the fewest
// active (in-flight) connections.
//
// This is the stateful algorithm for Experiment 1: it reads
// ActiveConnections from each ServerState, which is maintained via
// atomic increments/decrements in the proxy's AddConnections/RemoveConnections
// calls. In a multi-LB deployment, these counters are local to each instance,
// meaning the routing decision is based on each LB's own view of load.
//
// Tie-breaking: when multiple backends share the minimum connection count,
// one is selected uniformly at random. This prevents deterministic pile-on
// where all LB instances simultaneously route to the same backend.
type LeastConnectionsPolicy struct{}

// GetTarget iterates all healthy backends, finds those with the minimum
// connection count, and selects randomly among tied candidates.
//
// The scan initializes minConns to math.MaxInt64, guaranteeing that the
// first healthy server becomes a candidate regardless of its count.
func (lc *LeastConnectionsPolicy) GetTarget(state *repository.SharedState, _ *http.Request) (url.URL, error) {
	servers, err := (*state).GetHealthy()
	if err != nil {
		return url.URL{}, err
	}
	if len(servers) == 0 {
		return url.URL{}, errors.New("no server found")
	}

	var candidates []*repository.ServerState
	minConns := int64(math.MaxInt64)

	for _, srv := range servers {
		// Atomic read; no mutex required for the connection count.
		conns := srv.GetActiveConnections()

		if conns < minConns {
			// New minimum: reset candidate set to just this server.
			minConns = conns
			candidates = []*repository.ServerState{srv}
		} else if conns == minConns {
			// Tie: add to candidate set for random selection below.
			candidates = append(candidates, srv)
		}
	}

	if len(candidates) == 0 {
		return url.URL{}, errors.New("no server found")
	}

	// Uniform random tie-break prevents thundering herd across LB instances.
	index := rand.Intn(len(candidates))
	return candidates[index].ServerURL, nil
}
