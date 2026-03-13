package metrics

import (
	"time"
)

// CollectMetrics defines the contract for request-level and time-series
// metrics collection. The Collector is the sole implementation.
//
// Parameters for RecordRequest:
//   - backend: URL string identifying which backend served the request.
//   - latency: wall-clock duration from proxy start to response completion.
//   - success: true if the backend returned a successful response.
//   - timeout: true if the failure was specifically a context deadline exceeded.
//   - retried: true if this request was a retry attempt (not the first try).
type CollectMetrics interface {
	RecordRequest(string, time.Duration, bool, bool, bool)
	RecordTimeSeriesPoint(int)
	GetSummary() *Summary
	GetTimeSeriesData() []*TimeSeriesPoint
	ExportCSV(string) error
}
