package algorithms

import (
	"errors"
	"math/rand"
	"net/http"
	"net/url"
	"sync"

	"github.com/karthikeyansura/ha-l7-lb/internal/repository"
)

// Weighted distributes requests proportionally according to configured
// backend weights using a decrementing counter pool.
//
// Each backend is assigned [originalWeight, remainingWeight]. On each
// call, a random candidate is selected; if its remainingWeight > 0,
// it is chosen and its counter is decremented. When all counters hit
// zero, the entire pool resets to original values.
//
// Example: backends A(weight=70), B(weight=20), C(weight=10).
// Over 100 requests, approximately 70 go to A, 20 to B, 10 to C.
// The randomized selection order within each epoch prevents bursty
// patterns that a strict sequential approach would produce.
//
// Thread safety: a sync.Mutex protects the Weights map because multiple
// proxy goroutines call GetTarget concurrently and each call mutates
// the remaining counter.
type Weighted struct {
	mu      sync.RWMutex
	Weights map[url.URL][]int // key: backend URL, value: [originalWeight, remainingWeight]
}

// GetTarget selects a backend proportionally to its configured weight.
//
// Algorithm:
//  1. Build candidate list from healthy servers.
//  2. Lazily initialize weight entries for any newly-seen backend.
//  3. Randomly pick from candidates; if its remainingWeight > 0, select it.
//  4. If the picked candidate has remainingWeight == 0, remove it from
//     candidates and retry (inner loop).
//  5. If all candidates are exhausted (all weights depleted), reset all
//     counters to their original values and return the last candidate.
func (wrr *Weighted) GetTarget(state *repository.SharedState, _ *http.Request) (url.URL, error) {
	// Full lock: both reads and writes to the Weights map occur here.
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

	// Build candidate list and lazily register weight counters.
	for _, server := range servers {
		candidates = append(candidates, server.ServerURL)
		if _, ok := wrr.Weights[server.ServerURL]; !ok {
			wrr.Weights[server.ServerURL] = []int{server.Weight, server.Weight}
		}
	}

	// Attempt to find a candidate with remaining weight > 0.
	reset := true
	var candidate url.URL
	for len(candidates) != 0 {
		ri := rand.Intn(len(candidates))
		candidate = candidates[ri]
		if wrr.Weights[candidate][1] != 0 {
			reset = false
			break
		}
		// Remove exhausted candidate from slice (swap-delete pattern).
		candidates = append(candidates[:ri], candidates[ri+1:]...)
	}

	// All weights depleted in this epoch; reset all counters.
	if reset {
		for key, value := range wrr.Weights {
			if value[1] == 0 {
				wrr.Weights[key] = []int{value[0], value[0]}
			}
		}
	}

	// Decrement the selected candidate's remaining weight.
	wrr.Weights[candidate][1]--
	return candidate, nil
}
