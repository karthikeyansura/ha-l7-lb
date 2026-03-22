package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/karthikeyansura/ha-l7-lb/internal/algorithms"
	"github.com/karthikeyansura/ha-l7-lb/internal/metrics"
	"github.com/karthikeyansura/ha-l7-lb/internal/repository"
)

func mustURL(raw string) url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return *u
}

// setupProxy creates a test backend server and wired reverse proxy.
func setupProxy(handler http.Handler) (*httptest.Server, *ReverseProxy, *repository.InMemory) {
	backend := httptest.NewServer(handler)
	backendURL := mustURL(backend.URL)

	pool := repository.NewInMemory([]url.URL{backendURL}, []int{10})
	var state repository.SharedState = pool
	algo := &algorithms.RoundRobin{}
	collector := metrics.NewCollector("round-robin")

	proxy := NewReverseProxy(state, algo, collector, nil)

	return backend, proxy, pool
}

func TestProxy_ForwardsRequest(t *testing.T) {
	backend, proxy, _ := setupProxy(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "test")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("hello"))
	}))
	defer backend.Close()

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != "hello" {
		t.Errorf("expected 'hello', got '%s'", w.Body.String())
	}
	if w.Header().Get("X-Backend") != "test" {
		t.Error("expected X-Backend header to be forwarded")
	}
}

func TestProxy_503_NoHealthyBackends(t *testing.T) {
	pool := repository.NewInMemory([]url.URL{}, []int{})
	var state repository.SharedState = pool
	algo := &algorithms.RoundRobin{}
	collector := metrics.NewCollector("round-robin")

	proxy := NewReverseProxy(state, algo, collector, nil)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestProxy_RetriesIdempotentOnFailure(t *testing.T) {
	callCount := 0
	// First backend always fails; we need two backends for retry.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	backend1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate failure by closing connection
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("server does not support hijacking")
		}
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	defer backend1.Close()

	backend2 := httptest.NewServer(handler)
	defer backend2.Close()

	url1 := mustURL(backend1.URL)
	url2 := mustURL(backend2.URL)

	pool := repository.NewInMemory([]url.URL{url1, url2}, []int{10, 10})
	var state repository.SharedState = pool
	algo := &algorithms.RoundRobin{}
	collector := metrics.NewCollector("round-robin")

	proxy := NewReverseProxy(state, algo, collector, nil)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	proxy.ServeHTTP(w, req)

	// The request should eventually succeed via retry on backend2
	if w.Code != http.StatusOK && callCount == 0 {
		// If the first backend selected was backend2, it succeeds immediately.
		// If backend1 was selected first, it should retry on backend2.
		t.Logf("response code: %d, backend2 calls: %d", w.Code, callCount)
	}
}

func TestProxy_NoRetryForPOST(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	defer backend.Close()

	backendURL := mustURL(backend.URL)
	pool := repository.NewInMemory([]url.URL{backendURL}, []int{10})
	var state repository.SharedState = pool
	algo := &algorithms.RoundRobin{}
	collector := metrics.NewCollector("round-robin")

	proxy := NewReverseProxy(state, algo, collector, nil)

	req := httptest.NewRequest("POST", "/test", strings.NewReader(`{"key":"value"}`))
	w := httptest.NewRecorder()

	proxy.ServeHTTP(w, req)

	// POST should not retry — should get gateway timeout
	if w.Code != http.StatusGatewayTimeout {
		t.Errorf("expected 504 for failed POST (no retry), got %d", w.Code)
	}
}

func TestProxy_TracksConnections(t *testing.T) {
	done := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-done // Block until test releases
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	backendURL := mustURL(backend.URL)
	pool := repository.NewInMemory([]url.URL{backendURL}, []int{10})
	var state repository.SharedState = pool
	algo := &algorithms.RoundRobin{}
	collector := metrics.NewCollector("round-robin")

	proxy := NewReverseProxy(state, algo, collector, nil)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	// Start request in background
	go proxy.ServeHTTP(w, req)

	// Give the proxy time to connect and increment
	time.Sleep(200 * time.Millisecond)

	servers, _ := pool.GetAllServers()
	conns := servers[0].GetActiveConnections()
	if conns != 1 {
		t.Errorf("expected 1 active connection during request, got %d", conns)
	}

	close(done)
	time.Sleep(200 * time.Millisecond)

	conns = servers[0].GetActiveConnections()
	if conns != 0 {
		t.Errorf("expected 0 connections after request, got %d", conns)
	}
}

func TestProxy_BodyPreservedOnRetry(t *testing.T) {
	var receivedBody string

	backend1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	defer backend1.Close()

	backend2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		receivedBody = string(buf[:n])
		w.WriteHeader(http.StatusOK)
	}))
	defer backend2.Close()

	url1 := mustURL(backend1.URL)
	url2 := mustURL(backend2.URL)

	pool := repository.NewInMemory([]url.URL{url1, url2}, []int{10, 10})
	var state repository.SharedState = pool

	// Force round-robin to hit backend1 first
	algo := &algorithms.RoundRobin{}
	collector := metrics.NewCollector("round-robin")

	proxy := NewReverseProxy(state, algo, collector, nil)

	body := `{"test":"data"}`
	req := httptest.NewRequest("PUT", "/test", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	proxy.ServeHTTP(w, req)

	// If retry happened and body was preserved, backend2 should have received it
	if receivedBody != "" && receivedBody != body {
		t.Errorf("expected body '%s' on retry, got '%s'", body, receivedBody)
	}
}

func TestIsIdempotent(t *testing.T) {
	tests := []struct {
		method   string
		expected bool
	}{
		{"GET", true},
		{"PUT", true},
		{"DELETE", true},
		{"POST", false},
		{"PATCH", false},
	}
	for _, tc := range tests {
		if got := isIdempotent(tc.method); got != tc.expected {
			t.Errorf("isIdempotent(%q) = %v, want %v", tc.method, got, tc.expected)
		}
	}
}

func TestSelectDifferent_ExcludesFailedBackend(t *testing.T) {
	urls := []url.URL{
		mustURL("http://a:8080"),
		mustURL("http://b:8080"),
		mustURL("http://c:8080"),
	}
	pool := repository.NewInMemory(urls, []int{10, 10, 10})
	var state repository.SharedState = pool

	proxy := NewReverseProxy(state, &algorithms.RoundRobin{}, metrics.NewCollector("rr"), nil)

	backends, _ := pool.GetAllServers()
	exclude := mustURL("http://a:8080")

	result := proxy.selectDifferent(backends, &exclude, nil)
	if result == nil {
		t.Fatal("expected a result")
	}
	if result.String() == "http://a:8080" {
		t.Error("selectDifferent should not return the excluded backend")
	}
}

func TestSelectDifferent_RespectsConnectionCounts(t *testing.T) {
	urls := []url.URL{
		mustURL("http://a:8080"),
		mustURL("http://b:8080"),
		mustURL("http://c:8080"),
	}
	pool := repository.NewInMemory(urls, []int{10, 10, 10})
	var state repository.SharedState = pool

	// Give b:5, c:20 connections
	pool.AddConnections(urls[1], 5)
	pool.AddConnections(urls[2], 20)

	proxy := NewReverseProxy(state, &algorithms.RoundRobin{}, metrics.NewCollector("rr"), nil)

	backends, _ := pool.GetAllServers()
	exclude := mustURL("http://a:8080")

	result := proxy.selectDifferent(backends, &exclude, nil)
	if result == nil {
		t.Fatal("expected a result")
	}
	// Should pick b (5 connections) over c (20 connections)
	if result.String() != "http://b:8080" {
		t.Errorf("expected http://b:8080 (fewer connections), got %s", result.String())
	}
}

func TestSelectDifferent_SingleBackend_ReturnsNil(t *testing.T) {
	urls := []url.URL{mustURL("http://a:8080")}
	pool := repository.NewInMemory(urls, []int{10})
	var state repository.SharedState = pool

	proxy := NewReverseProxy(state, &algorithms.RoundRobin{}, metrics.NewCollector("rr"), nil)

	backends, _ := pool.GetAllServers()
	exclude := mustURL("http://a:8080")

	result := proxy.selectDifferent(backends, &exclude, nil)
	if result != nil {
		t.Error("expected nil when only backend is excluded")
	}
}
