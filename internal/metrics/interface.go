package metrics

import (
	"time"
)

// CollectMetrics defines the contract for request-level and time-series
// metrics collection.
type CollectMetrics interface {
	RecordRequest(string, time.Duration, bool, bool, bool)
	RecordTimeSeriesPoint(int)
	GetSummary() *Summary
	GetTimeSeriesData() []*TimeSeriesPoint
	ExportCSV(string) error
}
