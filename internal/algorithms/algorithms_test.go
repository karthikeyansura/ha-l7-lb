package algorithms

import (
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/karthikeyansura/ha-l7-lb/internal/repository"
)

func mustURL(raw string) url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return *u
}

func newPool(urls []url.URL, weights []int) repository.SharedState {
	var s repository.SharedState = repository.NewInMemory(urls, weights)
	return s
}

func threeBackendPool() repository.SharedState {
	return newPool(
		[]url.URL{
			mustURL("http://a:8080"),
			mustURL("http://b:8080"),
			mustURL("http://c:8080"),
		},
		[]int{10, 10, 10},
	)
}

// --- RoundRobin ---

func TestRoundRobin_CyclesThroughBackends(t *testing.T) {
	pool := threeBackendPool()
	rr := &RoundRobin{}
	req, _ := http.NewRequest("GET", "/", nil)

	seen := map[string]int{}
	for i := 0; i < 9; i++ {
		target, err := rr.GetTarget(&pool, req)
		if err != nil {
			t.Fatal(err)
		}
		seen[target.String()]++
	}

	// After 9 requests across 3 backends, each should get exactly 3
	for u, count := range seen {
		if count != 3 {
			t.Errorf("expected 3 requests to %s, got %d", u, count)
		}
	}
}

func TestRoundRobin_NoHealthyServers(t *testing.T) {
	pool := newPool([]url.URL{}, []int{})
	rr := &RoundRobin{}
	req, _ := http.NewRequest("GET", "/", nil)

	_, err := rr.GetTarget(&pool, req)
	if err == nil {
		t.Error("expected error when no healthy servers")
	}
}

func TestRoundRobin_ConcurrentSafety(t *testing.T) {
	pool := threeBackendPool()
	rr := &RoundRobin{}
	req, _ := http.NewRequest("GET", "/", nil)

	var wg sync.WaitGroup
	var errCount atomic.Int64
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := rr.GetTarget(&pool, req)
			if err != nil {
				errCount.Add(1)
			}
		}()
	}
	wg.Wait()

	if errCount.Load() > 0 {
		t.Errorf("expected 0 errors from concurrent GetTarget, got %d", errCount.Load())
	}
}

// --- LeastConnections ---

func TestLeastConnections_SelectsLowest(t *testing.T) {
	urls := []url.URL{
		mustURL("http://a:8080"),
		mustURL("http://b:8080"),
		mustURL("http://c:8080"),
	}
	im := repository.NewInMemory(urls, []int{10, 10, 10})
	var pool repository.SharedState = im

	// Give a:10, b:0, c:20 connections.
	// With Power of Two Choices, b (fewest) should win most comparisons.
	im.AddConnections(urls[0], 10)
	im.AddConnections(urls[2], 20)

	lc := &LeastConnectionsPolicy{}
	req, _ := http.NewRequest("GET", "/", nil)

	counts := map[string]int{}
	iterations := 1000
	for i := 0; i < iterations; i++ {
		target, err := lc.GetTarget(&pool, req)
		if err != nil {
			t.Fatal(err)
		}
		counts[target.String()]++
	}

	bCount := counts["http://b:8080"]
	// b has 0 connections; it wins any pair it appears in.
	// Probability of b appearing in a random pair of 3 = 1 - C(2,2)/C(3,2) = 1 - 1/3 ≈ 66%.
	// Allow generous lower bound of 50% to avoid flaky failures.
	if bCount < iterations/2 {
		t.Errorf("expected b:8080 (0 conns) to be selected >50%% of the time, got %d/%d", bCount, iterations)
	}
}

func TestLeastConnections_NoHealthyServers(t *testing.T) {
	pool := newPool([]url.URL{}, []int{})
	lc := &LeastConnectionsPolicy{}
	req, _ := http.NewRequest("GET", "/", nil)

	_, err := lc.GetTarget(&pool, req)
	if err == nil {
		t.Error("expected error when no healthy servers")
	}
}

// --- Weighted ---

func TestWeighted_RespectsWeightDistribution(t *testing.T) {
	urls := []url.URL{
		mustURL("http://a:8080"),
		mustURL("http://b:8080"),
	}
	pool := newPool(urls, []int{80, 20})
	w := &Weighted{Weights: make(map[url.URL][]int)}
	req, _ := http.NewRequest("GET", "/", nil)

	counts := map[string]int{}
	total := 1000
	for i := 0; i < total; i++ {
		target, err := w.GetTarget(&pool, req)
		if err != nil {
			t.Fatal(err)
		}
		counts[target.String()]++
	}

	aCount := counts["http://a:8080"]
	bCount := counts["http://b:8080"]

	// With 80/20 weights, A should get roughly 4x B's requests.
	// Allow generous tolerance since the algorithm is randomized.
	ratio := float64(aCount) / float64(bCount)
	if ratio < 2.0 || ratio > 6.0 {
		t.Errorf("expected ratio ~4.0, got %.2f (a=%d, b=%d)", ratio, aCount, bCount)
	}
}

func TestWeighted_NoHealthyServers(t *testing.T) {
	pool := newPool([]url.URL{}, []int{})
	w := &Weighted{Weights: make(map[url.URL][]int)}
	req, _ := http.NewRequest("GET", "/", nil)

	_, err := w.GetTarget(&pool, req)
	if err == nil {
		t.Error("expected error when no healthy servers")
	}
}

// --- Shared: skips unhealthy backends ---

func TestAlgorithms_SkipUnhealthyBackends(t *testing.T) {
	urls := []url.URL{
		mustURL("http://a:8080"),
		mustURL("http://b:8080"),
	}
	im := repository.NewInMemory(urls, []int{10, 10})
	var pool repository.SharedState = im

	im.MarkHealthy(urls[0], false)

	req, _ := http.NewRequest("GET", "/", nil)

	algorithms := []struct {
		name string
		algo Rule
	}{
		{"round-robin", &RoundRobin{}},
		{"least-connections", &LeastConnectionsPolicy{}},
		{"weighted", &Weighted{Weights: make(map[url.URL][]int)}},
	}

	for _, tc := range algorithms {
		t.Run(tc.name, func(t *testing.T) {
			target, err := tc.algo.GetTarget(&pool, req)
			if err != nil {
				t.Fatal(err)
			}
			if target.String() != "http://b:8080" {
				t.Errorf("expected http://b:8080 (only healthy), got %s", target.String())
			}
		})
	}
}
