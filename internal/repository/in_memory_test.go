package repository

import (
	"net/url"
	"sync"
	"testing"
)

func mustURL(raw string) url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return *u
}

func newTestPool() *InMemory {
	urls := []url.URL{
		mustURL("http://backend1:8080"),
		mustURL("http://backend2:8080"),
		mustURL("http://backend3:8080"),
	}
	weights := []int{10, 20, 30}
	return NewInMemory(urls, weights)
}

func TestNewInMemory_AllHealthy(t *testing.T) {
	pool := newTestPool()
	servers, err := pool.GetAllServers()
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 3 {
		t.Fatalf("expected 3 servers, got %d", len(servers))
	}
	for _, s := range servers {
		if !s.IsHealthy() {
			t.Errorf("expected %s to be healthy", s.ServerURL.String())
		}
	}
}

func TestGetHealthy_FiltersUnhealthy(t *testing.T) {
	pool := newTestPool()

	pool.MarkHealthy(mustURL("http://backend2:8080"), false)

	healthy, err := pool.GetHealthy()
	if err != nil {
		t.Fatal(err)
	}
	if len(healthy) != 2 {
		t.Fatalf("expected 2 healthy servers, got %d", len(healthy))
	}
	for _, s := range healthy {
		if s.ServerURL.String() == "http://backend2:8080" {
			t.Error("backend2 should not be in healthy list")
		}
	}
}

func TestMarkHealthy_RecoveryTransition(t *testing.T) {
	pool := newTestPool()
	u := mustURL("http://backend1:8080")

	pool.MarkHealthy(u, false)
	healthy, _ := pool.GetHealthy()
	if len(healthy) != 2 {
		t.Fatalf("expected 2 healthy after marking down, got %d", len(healthy))
	}

	pool.MarkHealthy(u, true)
	healthy, _ = pool.GetHealthy()
	if len(healthy) != 3 {
		t.Fatalf("expected 3 healthy after recovery, got %d", len(healthy))
	}
}

func TestMarkHealthy_UnknownURL_NoOp(t *testing.T) {
	pool := newTestPool()
	pool.MarkHealthy(mustURL("http://unknown:9999"), false)

	healthy, _ := pool.GetHealthy()
	if len(healthy) != 3 {
		t.Fatalf("expected 3 healthy (unknown URL should be no-op), got %d", len(healthy))
	}
}

func TestAddRemoveConnections(t *testing.T) {
	pool := newTestPool()
	u := mustURL("http://backend1:8080")

	pool.AddConnections(u, 5)

	servers, _ := pool.GetAllServers()
	for _, s := range servers {
		if s.ServerURL == u {
			if s.GetActiveConnections() != 5 {
				t.Errorf("expected 5 connections, got %d", s.GetActiveConnections())
			}
		}
	}

	pool.RemoveConnections(u, 3)
	servers, _ = pool.GetAllServers()
	for _, s := range servers {
		if s.ServerURL == u {
			if s.GetActiveConnections() != 2 {
				t.Errorf("expected 2 connections after removal, got %d", s.GetActiveConnections())
			}
		}
	}
}

func TestSyncServers_AddsAndRemoves(t *testing.T) {
	pool := newTestPool()

	// New set: keep backend1, drop backend2/3, add backend4
	newURLs := []url.URL{
		mustURL("http://backend1:8080"),
		mustURL("http://backend4:8080"),
	}
	pool.SyncServers(newURLs, 50)

	servers, _ := pool.GetAllServers()
	if len(servers) != 2 {
		t.Fatalf("expected 2 servers after sync, got %d", len(servers))
	}

	found := map[string]bool{}
	for _, s := range servers {
		found[s.ServerURL.String()] = true
	}
	if !found["http://backend1:8080"] {
		t.Error("backend1 should be preserved")
	}
	if !found["http://backend4:8080"] {
		t.Error("backend4 should be added")
	}
}

func TestSyncServers_PreservesExistingState(t *testing.T) {
	pool := newTestPool()
	u := mustURL("http://backend1:8080")

	pool.MarkHealthy(u, false)
	pool.AddConnections(u, 10)

	pool.SyncServers([]url.URL{u}, 50)

	servers, _ := pool.GetAllServers()
	if len(servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(servers))
	}
	if servers[0].IsHealthy() {
		t.Error("expected preserved unhealthy state")
	}
	if servers[0].GetActiveConnections() != 10 {
		t.Errorf("expected preserved connections=10, got %d", servers[0].GetActiveConnections())
	}
}

func TestConcurrentAccess(t *testing.T) {
	pool := newTestPool()
	u := mustURL("http://backend1:8080")

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			pool.AddConnections(u, 1)
		}()
		go func() {
			defer wg.Done()
			pool.GetHealthy()
		}()
		go func() {
			defer wg.Done()
			pool.MarkHealthy(u, true)
		}()
	}
	wg.Wait()

	servers, _ := pool.GetAllServers()
	for _, s := range servers {
		if s.ServerURL == u {
			if s.GetActiveConnections() != 100 {
				t.Errorf("expected 100 connections, got %d", s.GetActiveConnections())
			}
		}
	}
}

func TestGetAllServers_ReturnsCopy(t *testing.T) {
	pool := newTestPool()
	servers1, _ := pool.GetAllServers()
	servers2, _ := pool.GetAllServers()

	// Modifying the returned slice should not affect the pool
	servers1[0] = nil
	if servers2[0] == nil {
		t.Error("GetAllServers should return independent slice copies")
	}
}
