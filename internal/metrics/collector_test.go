package metrics

import (
	"os"
	"sync"
	"testing"
	"time"
)

func TestRecordRequest_CountsCorrectly(t *testing.T) {
	c := NewCollector("test")

	c.RecordRequest("http://a:8080", 100*time.Millisecond, true, false, false)
	c.RecordRequest("http://a:8080", 200*time.Millisecond, true, false, true)
	c.RecordRequest("http://b:8080", 300*time.Millisecond, false, true, false)

	summary := c.GetSummary()

	if summary.TotalRequests != 3 {
		t.Errorf("expected 3 total, got %d", summary.TotalRequests)
	}
	if summary.SuccessfulRequests != 2 {
		t.Errorf("expected 2 successful, got %d", summary.SuccessfulRequests)
	}
	if summary.FailedRequests != 1 {
		t.Errorf("expected 1 failed, got %d", summary.FailedRequests)
	}
	if summary.RetriedRequests != 1 {
		t.Errorf("expected 1 retried, got %d", summary.RetriedRequests)
	}
}

func TestGetSummary_SuccessAndRetryRates(t *testing.T) {
	c := NewCollector("test")

	for i := 0; i < 80; i++ {
		c.RecordRequest("http://a:8080", 10*time.Millisecond, true, false, false)
	}
	for i := 0; i < 20; i++ {
		c.RecordRequest("http://a:8080", 10*time.Millisecond, false, false, false)
	}
	for i := 0; i < 10; i++ {
		c.RecordRequest("http://a:8080", 10*time.Millisecond, true, false, true)
	}

	summary := c.GetSummary()

	// 90 successful out of 110
	expectedSuccessRate := float64(90) / float64(110) * 100
	if diff := summary.SuccessRate - expectedSuccessRate; diff > 0.01 || diff < -0.01 {
		t.Errorf("expected success rate ~%.2f, got %.2f", expectedSuccessRate, summary.SuccessRate)
	}

	expectedRetryRate := float64(10) / float64(110) * 100
	if diff := summary.RetryRate - expectedRetryRate; diff > 0.01 || diff < -0.01 {
		t.Errorf("expected retry rate ~%.2f, got %.2f", expectedRetryRate, summary.RetryRate)
	}
}

func TestGetSummary_Percentiles(t *testing.T) {
	c := NewCollector("test")

	// Add 100 requests with latencies 1ms, 2ms, ..., 100ms
	for i := 1; i <= 100; i++ {
		c.RecordRequest("http://a:8080", time.Duration(i)*time.Millisecond, true, false, false)
	}

	summary := c.GetSummary()

	// p50 should be around 50
	if summary.LatencyP50 < 45 || summary.LatencyP50 > 55 {
		t.Errorf("expected p50 ~50ms, got %.2f", summary.LatencyP50)
	}
	// p95 should be around 95
	if summary.LatencyP95 < 90 || summary.LatencyP95 > 100 {
		t.Errorf("expected p95 ~95ms, got %.2f", summary.LatencyP95)
	}
	// p99 should be around 99
	if summary.LatencyP99 < 95 || summary.LatencyP99 > 100 {
		t.Errorf("expected p99 ~99ms, got %.2f", summary.LatencyP99)
	}
}

func TestGetSummary_BackendStats(t *testing.T) {
	c := NewCollector("test")

	c.RecordRequest("http://a:8080", 100*time.Millisecond, true, false, false)
	c.RecordRequest("http://a:8080", 200*time.Millisecond, true, false, false)
	c.RecordRequest("http://b:8080", 50*time.Millisecond, false, true, false)

	summary := c.GetSummary()

	aStats := summary.BackendStats["http://a:8080"]
	if aStats == nil {
		t.Fatal("expected stats for backend a")
	}
	if aStats.RequestCount != 2 {
		t.Errorf("expected 2 requests for a, got %d", aStats.RequestCount)
	}
	if aStats.AvgLatency != 150 {
		t.Errorf("expected avg latency 150ms for a, got %.2f", aStats.AvgLatency)
	}

	bStats := summary.BackendStats["http://b:8080"]
	if bStats == nil {
		t.Fatal("expected stats for backend b")
	}
	if bStats.TimeoutCount != 1 {
		t.Errorf("expected 1 timeout for b, got %d", bStats.TimeoutCount)
	}
}

func TestGetSummary_Empty(t *testing.T) {
	c := NewCollector("test")
	summary := c.GetSummary()

	if summary.TotalRequests != 0 {
		t.Errorf("expected 0 total, got %d", summary.TotalRequests)
	}
	if summary.SuccessRate != 0 {
		t.Errorf("expected 0 success rate, got %.2f", summary.SuccessRate)
	}
	if summary.LatencyP50 != 0 {
		t.Errorf("expected 0 p50, got %.2f", summary.LatencyP50)
	}
}

func TestRecordTimeSeriesPoint(t *testing.T) {
	c := NewCollector("test")

	c.RecordRequest("http://a:8080", 100*time.Millisecond, true, false, false)
	c.RecordTimeSeriesPoint(3)

	data := c.GetTimeSeriesData()
	if len(data) != 1 {
		t.Fatalf("expected 1 time-series point, got %d", len(data))
	}
	if data[0].ActiveBackends != 3 {
		t.Errorf("expected 3 active backends, got %d", data[0].ActiveBackends)
	}
	if data[0].RequestsPerSec <= 0 {
		t.Error("expected positive RPS")
	}
}

func TestExportCSV(t *testing.T) {
	c := NewCollector("test")
	c.RecordRequest("http://a:8080", 100*time.Millisecond, true, false, false)
	c.RecordTimeSeriesPoint(2)

	tmpFile := t.TempDir() + "/metrics.csv"
	err := c.ExportCSV(tmpFile)
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	if len(content) == 0 {
		t.Error("CSV file is empty")
	}
	// Should have header + 1 data row
	if !contains(content, "Timestamp") {
		t.Error("CSV missing header")
	}
	if !contains(content, "ActiveBackends") {
		t.Error("CSV missing ActiveBackends column")
	}
}

func TestConcurrentRecordAndRead(t *testing.T) {
	c := NewCollector("test")

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			c.RecordRequest("http://a:8080", 10*time.Millisecond, true, false, false)
		}()
		go func() {
			defer wg.Done()
			c.GetSummary()
		}()
	}
	wg.Wait()

	summary := c.GetSummary()
	if summary.TotalRequests != 100 {
		t.Errorf("expected 100 requests, got %d", summary.TotalRequests)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
