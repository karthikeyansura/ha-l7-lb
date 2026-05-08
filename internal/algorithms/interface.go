// Package algorithms implements pluggable load balancing strategies.
// Each algorithm satisfies the Rule interface, allowing the proxy to
// select a backend without knowing which strategy is in use.
package algorithms

import (
	"net/http"
	"net/url"

	"github.com/karthikeyansura/ha-l7-lb/internal/repository"
)

// Rule is the strategy interface for backend selection. Implementations
// must be safe for concurrent use. Returns an error only when zero
// healthy backends exist.
type Rule interface {
	GetTarget(*repository.SharedState, *http.Request) (url.URL, error)
}
