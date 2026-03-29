package algorithms

import (
	"errors"
	"math/rand"
	"net/http"
	"net/url"

	"github.com/karthikeyansura/ha-l7-lb/internal/repository"
)

// LeastConnectionsPolicy selects a healthy backend using the Power of Two Choices
// algorithm. Instead of scanning all backends to find the absolute minimum
// (which is inefficient and relies on perfectly synchronized global state),
// it picks two distinct random backends and routes the request to the one
// with fewer active connections.
//
// This is the stateful algorithm for Experiment 1: it reads ActiveConnections
// from each ServerState. This randomized approach provides near-optimal load
// distribution even when each LB instance only has a local view of connection
// counts in a horizontally scaled multi-LB deployment.
type LeastConnectionsPolicy struct{}

// GetTarget selects a backend using the Power of Two Choices algorithm.
// It returns an error if no healthy servers are available.
func (lc *LeastConnectionsPolicy) GetTarget(state *repository.SharedState, _ *http.Request) (url.URL, error) {
	servers, err := (*state).GetHealthy()
	if err != nil {
		return url.URL{}, err
	}
	if len(servers) == 0 {
		return url.URL{}, errors.New("no server found")
	}

	// Single backend: no choice needed.
	if len(servers) == 1 {
		return servers[0].ServerURL, nil
	}

	// Power of Two Choices: pick two distinct random backends and select
	// the one with fewer active connections. This provides near-optimal
	// load distribution even with local-only counters across LB instances.
	i := rand.Intn(len(servers))
	j := rand.Intn(len(servers) - 1)
	if j >= i {
		j++
	}

	a, b := servers[i], servers[j]
	if a.GetActiveConnections() <= b.GetActiveConnections() {
		return a.ServerURL, nil
	}
	return b.ServerURL, nil
}
