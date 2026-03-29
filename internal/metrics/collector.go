// Package metrics provides thread-safe request-level and time-series
// metrics collection for the load balancer.
//
// The Collector accumulates per-request latency, success/failure/timeout
// counts, and retry rates. It computes percentile distributions (p50, p95,
// p99) on demand via sorted-copy-and-index, and exports time-series data
// as CSV for post-experiment analysis in Locust or external tools.
//
// Concurrency model: a sync.RWMutex serializes writes (RecordRequest,
// RecordTimeSeriesPoint) and allows concurrent reads (GetSummary,
// GetTimeSeriesData, ExportCSV).
package metrics

import (
	"encoding/csv"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"sync"
	"time"
)

const maxLatencySamples = 10000

// Collector is the central metrics accumulator. One instance is created
// per LB process, shared by all proxy goroutines via the RWMutex.
type Collector struct {
	mu sync.RWMutex

	totalRequests      int64
	successfulRequests int64
	failedRequests     int64
	retriedRequests    int64

	// latencies stores every request's latency in milliseconds.
	// Pre-allocated to 10,000 to reduce early allocations under load.
	// Sorted on demand in GetSummary for percentile computation.
	latencies []float64

	// backendMetrics tracks per-backend breakdown. Keyed by URL string.
	backendMetrics map[string]*BackendMetrics

	policyName string // Identifies the algorithm for experiment labeling.

	timeSeriesData []*TimeSeriesPoint
	startTime      time.Time // Used to compute cumulative requests-per-second.

	latencyCount int64   // Total number of recorded latencies (for exact average).
	latencySum   float64 // Running sum for average computation.
}

// BackendMetrics tracks per-backend request counts and cumulative latency.
type BackendMetrics struct {
	RequestCount int64
	SuccessCount int64
	FailureCount int64
	TimeoutCount int64
	TotalLatency float64 // Sum of latencies in milliseconds.
}

// TimeSeriesPoint is a periodic snapshot of system-wide throughput
// and health. Recorded every 5 seconds by a background goroutine.
type TimeSeriesPoint struct {
	Timestamp      time.Time
	RequestsPerSec float64
	AvgLatency     float64
	ActiveBackends int
}

// Summary is the read-only view returned by GetSummary.
// Percentiles are computed from a sorted copy of the latencies slice.
type Summary struct {
	PolicyName         string
	TotalRequests      int64
	SuccessfulRequests int64
	FailedRequests     int64
	RetriedRequests    int64
	SuccessRate        float64 // Percentage (0-100).
	RetryRate          float64 // Percentage (0-100).
	AvgLatency         float64
	LatencyP50         float64
	LatencyP95         float64
	LatencyP99         float64
	BackendStats       map[string]*BackendStats
}

// BackendStats is the per-backend view within a Summary.
type BackendStats struct {
	RequestCount int64
	SuccessCount int64
	FailureCount int64
	TimeoutCount int64
	AvgLatency   float64
}

// NewCollector initializes a Collector with pre-allocated storage.
func NewCollector(policyName string) *Collector {
	return &Collector{
		backendMetrics: make(map[string]*BackendMetrics),
		policyName:     policyName,
		latencies:      make([]float64, 0, maxLatencySamples),
		timeSeriesData: make([]*TimeSeriesPoint, 0),
		startTime:      time.Now(),
	}
}

// RecordRequest is called by the proxy after each completed or failed
// request. All counters and the latencies slice are updated atomically
// under the write lock.
func (c *Collector) RecordRequest(backend string, latency time.Duration, success bool, timeout bool, retried bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.totalRequests++
	if success {
		c.successfulRequests++
	} else {
		c.failedRequests++
	}
	if retried {
		c.retriedRequests++
	}

	latencyMs := float64(latency.Milliseconds())
	c.latencyCount++
	c.latencySum += latencyMs

	if len(c.latencies) < maxLatencySamples {
		c.latencies = append(c.latencies, latencyMs)
	} else {
		// Reservoir sampling: replace a random element with decreasing probability.
		j := rand.Int63n(c.latencyCount)
		if j < maxLatencySamples {
			c.latencies[j] = latencyMs
		}
	}

	// Lazily initialize per-backend metrics on first request.
	if _, exists := c.backendMetrics[backend]; !exists {
		c.backendMetrics[backend] = &BackendMetrics{}
	}

	bm := c.backendMetrics[backend]
	bm.RequestCount++
	bm.TotalLatency += latencyMs

	if success {
		bm.SuccessCount++
	} else {
		bm.FailureCount++
	}

	if timeout {
		bm.TimeoutCount++
	}
}

// RecordTimeSeriesPoint appends a system-wide snapshot.
// Called every 5 seconds by a background goroutine in main.
// RPS is cumulative (total requests / total elapsed time), not instantaneous.
func (c *Collector) RecordTimeSeriesPoint(activeBackends int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elapsed := time.Since(c.startTime).Seconds()
	rps := float64(c.totalRequests) / elapsed

	avgLatency := 0.0
	if c.latencyCount > 0 {
		avgLatency = c.latencySum / float64(c.latencyCount)
	}

	c.timeSeriesData = append(c.timeSeriesData, &TimeSeriesPoint{
		Timestamp:      time.Now(),
		RequestsPerSec: rps,
		AvgLatency:     avgLatency,
		ActiveBackends: activeBackends,
	})
}

// GetSummary computes a point-in-time summary including percentiles.
// Percentile computation copies and sorts the latencies slice to avoid
// mutating the live data. This is O(n log n) but only runs on demand
// (metrics endpoint or graceful shutdown), not on the hot path.
func (c *Collector) GetSummary() *Summary {
	c.mu.RLock()
	defer c.mu.RUnlock()

	summary := &Summary{
		PolicyName:         c.policyName,
		TotalRequests:      c.totalRequests,
		SuccessfulRequests: c.successfulRequests,
		FailedRequests:     c.failedRequests,
		RetriedRequests:    c.retriedRequests,
		SuccessRate:        0.0,
		RetryRate:          0.0,
		BackendStats:       make(map[string]*BackendStats),
	}

	if c.totalRequests > 0 {
		summary.SuccessRate = float64(c.successfulRequests) / float64(c.totalRequests) * 100
		summary.RetryRate = float64(c.retriedRequests) / float64(c.totalRequests) * 100
	}

	if len(c.latencies) > 0 {
		// Sorted copy for non-destructive percentile computation.
		sorted := make([]float64, len(c.latencies))
		copy(sorted, c.latencies)
		sort.Float64s(sorted)

		summary.LatencyP50 = percentile(sorted, 50)
		summary.LatencyP95 = percentile(sorted, 95)
		summary.LatencyP99 = percentile(sorted, 99)
	}

	if c.latencyCount > 0 {
		summary.AvgLatency = c.latencySum / float64(c.latencyCount)
	}

	for url, bm := range c.backendMetrics {
		avgLatency := 0.0
		if bm.RequestCount > 0 {
			avgLatency = bm.TotalLatency / float64(bm.RequestCount)
		}

		summary.BackendStats[url] = &BackendStats{
			RequestCount: bm.RequestCount,
			SuccessCount: bm.SuccessCount,
			FailureCount: bm.FailureCount,
			TimeoutCount: bm.TimeoutCount,
			AvgLatency:   avgLatency,
		}
	}

	return summary
}

// GetTimeSeriesData returns a snapshot (shallow copy) of all recorded
// time-series points. Safe for concurrent access via RLock.
func (c *Collector) GetTimeSeriesData() []*TimeSeriesPoint {
	c.mu.RLock()
	defer c.mu.RUnlock()

	data := make([]*TimeSeriesPoint, len(c.timeSeriesData))
	copy(data, c.timeSeriesData)
	return data
}

// ExportCSV writes time-series data to a CSV file.
// Used during graceful shutdown and via the /metrics/export HTTP endpoint.
func (c *Collector) ExportCSV(filepath string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	file, err := os.Create(filepath)
	if err != nil {
		return err
	}

	defer func() {
		_ = file.Close()
	}()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	if err := writer.Write([]string{"Timestamp", "RequestsPerSec", "AvgLatency", "ActiveBackends"}); err != nil {
		return err
	}

	for _, point := range c.timeSeriesData {
		if err := writer.Write([]string{
			point.Timestamp.Format(time.RFC3339),
			fmt.Sprintf("%.2f", point.RequestsPerSec),
			fmt.Sprintf("%.2f", point.AvgLatency),
			fmt.Sprintf("%d", point.ActiveBackends),
		}); err != nil {
			return err
		}
	}

	return writer.Error()
}

// percentile returns the value at the given percentile from a pre-sorted slice.
// Uses the nearest-rank method: index = floor(len * p / 100), clamped to bounds.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}

	index := int(float64(len(sorted)) * p / 100.0)
	if index >= len(sorted) {
		index = len(sorted) - 1
	}

	return sorted[index]
}
