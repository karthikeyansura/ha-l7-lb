// Package config provides YAML-based configuration with environment
// variable overrides for containerized deployments.
//
// Environment override precedence:
//   - REDIS_ADDR overrides redis.addr (ElastiCache endpoint via ECS).
//   - REDIS_PASSWORD overrides redis.password.
//   - RETRIES_ENABLED overrides load_balancer.retries_enabled.
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
type Config struct {
	LoadBalancer struct {
		Port    int           `yaml:"port"`
		Timeout time.Duration `yaml:"timeout"`
		RetriesEnabled bool `yaml:"retries_enabled"` // Flipped via RETRIES_ENABLED env var.
	} `yaml:"load_balancer"`

	Route struct {
		Policy   string `yaml:"policy"` // "round-robin" | "least-connections" | "weighted"
		Backends []struct {
			Endpoint string `yaml:"endpoint"` // Full URL: "http://host:port"
			Weight   int    `yaml:"weight"`   // Used by Weighted algorithm only.
		} `yaml:"backends"`
	} `yaml:"route"`

	HealthCheck struct {
		Interval time.Duration `yaml:"interval"`
		Timeout  time.Duration `yaml:"timeout"`
	} `yaml:"health_check"`

	// RedisConfig is nil when omitted from YAML. Redis is optional;
	// if unavailable, the LB runs with local-only health tracking.
	RedisConfig *struct {
		Addr     string `yaml:"addr"`
		Password string `yaml:"password"`
		DB       int    `yaml:"db"`
	} `yaml:"redis"`
}

// AppConfig is the singleton configuration instance.
var (
	AppConfig *Config
	once      sync.Once
)

// Load reads config.yaml and applies environment variable overrides.
func Load(configPath string) {
	once.Do(func() {
		AppConfig = &Config{}
		AppConfig.LoadBalancer.RetriesEnabled = true

		data, err := os.ReadFile(configPath)
		if err != nil {
			log.Fatalf("Error reading config file: %v", err)
		}

		err = yaml.Unmarshal(data, AppConfig)
		if err != nil {
			log.Fatalf("Error parsing config file: %v", err)
		}

		if v := os.Getenv("RETRIES_ENABLED"); v != "" {
			switch strings.ToLower(v) {
			case "true", "1", "yes":
				AppConfig.LoadBalancer.RetriesEnabled = true
			case "false", "0", "no":
				AppConfig.LoadBalancer.RetriesEnabled = false
			}
		}

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
