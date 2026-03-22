package health

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/karthikeyansura/ha-l7-lb/internal/repository"
)

// mockUpdater records calls to UpdateBackendStatus.
type mockUpdater struct {
	mu      sync.Mutex
	updates []statusUpdate
}

type statusUpdate struct {
	URL    string
	Status string
}

func (m *mockUpdater) UpdateBackendStatus(u url.URL, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updates = append(m.updates, statusUpdate{URL: u.String(), Status: status})
	return nil
}

func (m *mockUpdater) getUpdates() []statusUpdate {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]statusUpdate, len(m.updates))
	copy(result, m.updates)
	return result
}

func mustURL(raw string) url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return *u
}

func TestCheckBackend_HealthyServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	serverURL := mustURL(server.URL)
	pool := repository.NewInMemory([]url.URL{serverURL}, []int{10})
	updater := &mockUpdater{}

	checker := NewChecker(pool, updater, 1*time.Second, 2*time.Second)
	checker.checkAll()
	time.Sleep(100 * time.Millisecond)

	// Server started healthy and is healthy — no transition, no update expected.
	updates := updater.getUpdates()
	if len(updates) != 0 {
		t.Errorf("expected no updates (no state change), got %d", len(updates))
	}

	servers, _ := pool.GetAllServers()
	if !servers[0].IsHealthy() {
		t.Error("server should remain healthy")
	}
}

func TestCheckBackend_UnhealthyServer_Non200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	serverURL := mustURL(server.URL)
	pool := repository.NewInMemory([]url.URL{serverURL}, []int{10})
	updater := &mockUpdater{}

	checker := NewChecker(pool, updater, 1*time.Second, 2*time.Second)
	checker.checkAll()
	time.Sleep(100 * time.Millisecond)

	servers, _ := pool.GetAllServers()
	if servers[0].IsHealthy() {
		t.Error("server should be marked unhealthy after 500 response")
	}

	updates := updater.getUpdates()
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
	if updates[0].Status != "DOWN" {
		t.Errorf("expected DOWN status, got %s", updates[0].Status)
	}
}

func TestCheckBackend_UnreachableServer_MarkedDown(t *testing.T) {
	// Use a URL that will refuse connections.
	serverURL := mustURL("http://127.0.0.1:1")
	pool := repository.NewInMemory([]url.URL{serverURL}, []int{10})
	updater := &mockUpdater{}

	checker := NewChecker(pool, updater, 1*time.Second, 1*time.Second)
	checker.checkAll()
	time.Sleep(1500 * time.Millisecond)

	servers, _ := pool.GetAllServers()
	if servers[0].IsHealthy() {
		t.Error("unreachable server should be marked unhealthy (fix #5)")
	}

	updates := updater.getUpdates()
	if len(updates) != 1 {
		t.Fatalf("expected 1 update for unreachable server, got %d", len(updates))
	}
	if updates[0].Status != "DOWN" {
		t.Errorf("expected DOWN, got %s", updates[0].Status)
	}
}

func TestCheckBackend_Recovery(t *testing.T) {
	healthy := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if healthy {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	serverURL := mustURL(server.URL)
	pool := repository.NewInMemory([]url.URL{serverURL}, []int{10})
	updater := &mockUpdater{}

	checker := NewChecker(pool, updater, 1*time.Second, 2*time.Second)

	// Make unhealthy
	healthy = false
	checker.checkAll()
	time.Sleep(100 * time.Millisecond)

	servers, _ := pool.GetAllServers()
	if servers[0].IsHealthy() {
		t.Fatal("should be unhealthy")
	}

	// Recover
	healthy = true
	checker.checkAll()
	time.Sleep(100 * time.Millisecond)

	servers, _ = pool.GetAllServers()
	if !servers[0].IsHealthy() {
		t.Error("should have recovered to healthy")
	}

	updates := updater.getUpdates()
	if len(updates) != 2 {
		t.Fatalf("expected 2 updates (DOWN then UP), got %d", len(updates))
	}
	if updates[0].Status != "DOWN" || updates[1].Status != "UP" {
		t.Errorf("expected DOWN then UP, got %s then %s", updates[0].Status, updates[1].Status)
	}
}

func TestCheckBackend_NilUpdater(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	serverURL := mustURL(server.URL)
	pool := repository.NewInMemory([]url.URL{serverURL}, []int{10})

	// nil updater — degraded mode
	checker := NewChecker(pool, nil, 1*time.Second, 2*time.Second)
	checker.checkAll()
	time.Sleep(100 * time.Millisecond)

	// Should still update local state without panicking
	servers, _ := pool.GetAllServers()
	if servers[0].IsHealthy() {
		t.Error("server should be marked unhealthy even with nil updater")
	}
}
