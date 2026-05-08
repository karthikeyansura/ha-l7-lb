// Package discovery provides dynamic backend resolution via AWS Cloud Map.
// It continuously polls the internal DNS namespace to detect horizontally
// scaled ECS Fargate tasks and updates the load balancer's routing pool.
package discovery

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"time"

	"github.com/karthikeyansura/ha-l7-lb/internal/repository"
)

// StartDNSWatcher periodically resolves the target DNS name and updates
// the shared pool, scoped by sourceTag to support multiple DNS sources.
func StartDNSWatcher(ctx context.Context, sourceTag, targetHostname, port, scheme string, weight int, pool repository.SharedState) {
	ticker := time.NewTicker(5 * time.Second)
	go func() {
		defer ticker.Stop()

		syncDNS(sourceTag, targetHostname, port, scheme, weight, pool)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				syncDNS(sourceTag, targetHostname, port, scheme, weight, pool)
			}
		}
	}()
}

// syncDNS performs a single DNS lookup and synchronizes the active IP addresses
// with the shared repository pool. Only backends belonging to the given sourceTag
// are affected; other sources' backends are preserved.
func syncDNS(sourceTag, hostname, port, scheme string, weight int, pool repository.SharedState) {
	ips, err := net.LookupIP(hostname)
	if err != nil {
		slog.Warn("DNS lookup failed (cluster might be scaling to 0 or DNS unavailable)", "host", hostname, "error", err)
		return
	}

	var activeURLs []url.URL
	for _, ip := range ips {
		if ip.To4() != nil { // Only map IPv4
			u, _ := url.Parse(fmt.Sprintf("%s://%s:%s", scheme, ip.String(), port))
			activeURLs = append(activeURLs, *u)
		}
	}

	if len(activeURLs) > 0 {
		pool.SyncServersBySource(sourceTag, activeURLs, weight)
	}
}
