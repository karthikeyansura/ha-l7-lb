// Package config provides YAML-based configuration with environment
// variable overrides for containerized deployments.
//
// The Load function uses sync.Once to guarantee exactly one parse,
// making it safe to call from multiple goroutines (though in practice
// it is called once from main).
//
// Environment override precedence:
//   - REDIS_ADDR overrides redis.addr from YAML. This is how the ECS
//     task definition injects the ElastiCache endpoint at runtime.
//   - REDIS_PASSWORD overrides redis.password (for authenticated clusters).
//   - RETRIES_ENABLED overrides load_balancer.retries_enabled. Used by
//     Experiment 2 to toggle retry behavior without rebuilding the image.
//
// If REDIS_ADDR is set but no redis block exists in YAML, a redis
// config struct is created dynamically so the LB can run with only
// env vars and a minimal config file.
package config

import (
	"log"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level structure mirroring config.yaml.
// Duration fields (Timeout, Interval) are parsed directly by gopkg.in/yaml.v3
// from strings like "5s", "10s" via time.Duration's YAML unmarshaler.
type Config struct {
	LoadBalancer struct {
		Port    int           `yaml:"port"`
		Timeout time.Duration `yaml:"timeout"`
		// RetriesEnabled controls whether the proxy retries failed idempotent
		// requests on an alternate backend. Defaults to true (retry enabled)
		// if omitted. Flip to false for Experiment 2's retries-disabled variant
		// via the RETRIES_ENABLED env var.
		RetriesEnabled bool `yaml:"retries_enabled"`
	} `yaml:"load_balancer"`

	Route struct {
		Policy   string `yaml:"policy"` // "round-robin" | "least-connections" | "weighted"
		Backends []struct {
			Endpoint string `yaml:"endpoint"` // Full URL: "http://host:port"
			Weight   int    `yaml:"weight"`   // Used by Weighted algorithm only.
		} `yaml:"backends"`
	} `yaml:"route"`

	HealthCheck struct {
		Interval time.Duration `yaml:"interval"` // Time between health probe cycles.
		Timeout  time.Duration `yaml:"timeout"`  // Per-backend HTTP GET timeout.
	} `yaml:"health_check"`

	// RedisConfig is a pointer so it can be nil (omitted from YAML).
	// Redis is optional: if unavailable or unconfigured, the LB runs
	// in degraded mode with local-only health tracking (no cross-instance sync).
	RedisConfig *struct {
		Addr     string `yaml:"addr"` // Single: "host:6379". Cluster: "h1:6379,h2:6379".
		Password string `yaml:"password"`
		DB       int    `yaml:"db"` // Ignored in cluster mode.
	} `yaml:"redis"`
}

// AppConfig is the singleton configuration instance, populated by Load.
var (
	AppConfig *Config
	once      sync.Once
)

// Load reads and parses config.yaml, then applies environment variable
// overrides. Calls log.Fatalf on read or parse failure (no recovery).
func Load(configPath string) {
	once.Do(func() {
		AppConfig = &Config{}
		// Default RetriesEnabled to true; YAML unmarshal only overrides
		// if the key is present in the file, so omission means "default on".
		AppConfig.LoadBalancer.RetriesEnabled = true

		data, err := os.ReadFile(configPath)
		if err != nil {
			log.Fatalf("Error reading config file: %v", err)
		}

		err = yaml.Unmarshal(data, AppConfig)
		if err != nil {
			log.Fatalf("Error parsing config file: %v", err)
		}

		// RETRIES_ENABLED env var override: enables flipping retry behavior
		// via ECS task definition without rebuilding the image (Experiment 2).
		if v := os.Getenv("RETRIES_ENABLED"); v != "" {
			switch strings.ToLower(v) {
			case "true", "1", "yes":
				AppConfig.LoadBalancer.RetriesEnabled = true
			case "false", "0", "no":
				AppConfig.LoadBalancer.RetriesEnabled = false
			}
		}

		// REDIS_ADDR override: enables container-native configuration
		// where the ElastiCache endpoint is injected by ECS task definition.
		if addr := os.Getenv("REDIS_ADDR"); addr != "" {
			if AppConfig.RedisConfig == nil {
				AppConfig.RedisConfig = &struct {
					Addr     string `yaml:"addr"`
					Password string `yaml:"password"`
					DB       int    `yaml:"db"`
				}{}
			}
			AppConfig.RedisConfig.Addr = addr
		}
		if pass := os.Getenv("REDIS_PASSWORD"); pass != "" && AppConfig.RedisConfig != nil {
			AppConfig.RedisConfig.Password = pass
		}

		slog.Info("Config loaded", "config", AppConfig)
	})
}
