// Package algorithms implements pluggable load balancing strategies.
//
// Each algorithm satisfies the Rule interface, allowing the proxy to
// select a backend without knowing which strategy is in use.
// The request is passed to GetTarget so that request-aware algorithms
// (e.g., IP-based affinity, if added later) can inspect headers or
// source addresses. Currently unused by the three implemented policies.
package algorithms

import (
	"net/http"
	"net/url"

	"github.com/karthikeyansura/ha-l7-lb/internal/repository"
)

// Rule is the strategy interface for backend selection.
// Implementations must be safe for concurrent use; the proxy calls
// GetTarget from multiple goroutines handling simultaneous requests.
//
// Contract: returns an error only when zero healthy backends exist.
// On success, the returned url.URL is guaranteed to belong to a
// backend that was healthy at the time of the call (though it may
// become unhealthy between selection and the actual proxy request).
type Rule interface {
	GetTarget(*repository.SharedState, *http.Request) (url.URL, error)
}
