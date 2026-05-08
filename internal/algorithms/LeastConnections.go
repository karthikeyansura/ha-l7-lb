package algorithms

import (
	"errors"
	"math/rand"
	"net/http"
	"net/url"

	"github.com/karthikeyansura/ha-l7-lb/internal/repository"
)

// LeastConnectionsPolicy selects a healthy backend via Power of Two Choices:
// pick two random backends, route to the one with fewer active connections.
// Provides near-optimal distribution even with local-only connection counts
// in a multi-LB deployment.
type LeastConnectionsPolicy struct{}

// GetTarget selects a backend using Power of Two Choices.
func (lc *LeastConnectionsPolicy) GetTarget(state *repository.SharedState, _ *http.Request) (url.URL, error) {
	servers, err := (*state).GetHealthy()
	if err != nil {
		return url.URL{}, err
	}
	if len(servers) == 0 {
		return url.URL{}, errors.New("no server found")
	}
	if len(servers) == 1 {
		return servers[0].ServerURL, nil
	}
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
