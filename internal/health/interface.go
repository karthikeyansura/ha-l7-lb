package health

import "net/url"

// StatusUpdater abstracts health state persistence and propagation.
// Implemented by RedisManager for cross-instance synchronization.
type StatusUpdater interface {
	UpdateBackendStatus(url url.URL, status string) error
}
