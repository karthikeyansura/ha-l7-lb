package algorithms

import (
	"errors"
	"math/rand"
	"net/http"
	"net/url"
	"sync"

	"github.com/karthikeyansura/ha-l7-lb/internal/repository"
)

// Weighted distributes requests proportionally using a decrementing counter
// pool. Each backend holds [originalWeight, remainingWeight]; a random
// candidate is selected and its counter decremented. When all counters
// deplete, the pool resets. A sync.Mutex protects concurrent access.
type Weighted struct {
	mu      sync.RWMutex
	Weights map[url.URL][]int // [originalWeight, remainingWeight]
}

// GetTarget selects a backend proportionally to its configured weight.
func (wrr *Weighted) GetTarget(state *repository.SharedState, _ *http.Request) (url.URL, error) {
	wrr.mu.Lock()
	defer wrr.mu.Unlock()

	var candidates []url.URL
	servers, err := (*state).GetHealthy()
	if err != nil {
		return url.URL{}, err
	}
	if len(servers) == 0 {
		return url.URL{}, errors.New("no healthy servers available")
	}

	for _, server := range servers {
		candidates = append(candidates, server.ServerURL)
		if _, ok := wrr.Weights[server.ServerURL]; !ok {
			weight := server.Weight
			if weight <= 0 {
				weight = 1 // safe fallback for invalid weights
			}
			wrr.Weights[server.ServerURL] = []int{weight, weight}
		}
	}

	reset := true
	var candidate url.URL
	for len(candidates) != 0 {
		ri := rand.Intn(len(candidates))
		candidate = candidates[ri]
		if wrr.Weights[candidate][1] > 0 {
			reset = false
			break
		}
		candidates = append(candidates[:ri], candidates[ri+1:]...)
	}

	if reset {
		for key, value := range wrr.Weights {
			if value[1] == 0 {
				wrr.Weights[key] = []int{value[0], value[0]}
			}
		}
	}

	wrr.Weights[candidate][1]--
	return candidate, nil
}
