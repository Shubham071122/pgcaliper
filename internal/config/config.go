package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"pgcaliper/internal/alert"
)

type Config struct {
	Version  string              `yaml:"version"`
	Database DatabaseConfig      `yaml:"database"`
	Schedule ScheduleConfig      `yaml:"schedule"`
	Storage  StorageConfig       `yaml:"storage"`
	Strategy StrategyConfig      `yaml:"strategy"`
	Alerts   alert.WebhookConfig `yaml:"alerts"`
	Engine   EngineConfig        `yaml:"engine"`
}

type DatabaseConfig struct {
	URL                 string `yaml:"url"`
	SafeModeReadReplica bool   `yaml:"safe_mode_read_replica"`
	StatementTimeout    string `yaml:"statement_timeout"`
}

type ScheduleConfig struct {
	Interval      string `yaml:"interval"`
	RetentionDays int    `yaml:"retention_days"`
}

type StorageConfig struct {
	Schema string `yaml:"schema"`
}

type CustomGroup struct {
	Name   string   `yaml:"name"`
	Tables []string `yaml:"tables"`
}

type StrategyConfig struct {
	Mode              string        `yaml:"mode"`
	SchemaPattern     string        `yaml:"schema_pattern,omitempty"`
	TenantColumn      string        `yaml:"tenant_column,omitempty"`
	Tables            []string      `yaml:"tables,omitempty"`
	DefaultQuotaBytes int64         `yaml:"default_quota_bytes"`
	Groups            []CustomGroup `yaml:"groups,omitempty"`
}

type EngineConfig struct {
	Concurrency int `yaml:"concurrency"`
}

func (c *Config) ParseInterval() (time.Duration, error) {
	if c.Schedule.Interval == "" {
		return 1 * time.Hour, nil
	}
	return time.ParseDuration(c.Schedule.Interval)
}

func expandEnv(content []byte) []byte {
	re := regexp.MustCompile(`\$\{([a-zA-Z_0-9]+)(?::-([^}]*))?\}|\$([a-zA-Z_0-9]+)`)
	return re.ReplaceAllFunc(content, func(m []byte) []byte {
		str := string(m)
		if strings.HasPrefix(str, "${") {
			str = strings.TrimPrefix(strings.TrimSuffix(str, "}"), "${")
			parts := strings.SplitN(str, ":-", 2)
			varName := parts[0]
			defaultVal := ""
			if len(parts) == 2 {
				defaultVal = parts[1]
			}
			if val, ok := os.LookupEnv(varName); ok && val != "" {
				return []byte(val)
			}
			return []byte(defaultVal)
		} else if strings.HasPrefix(str, "$") {
			varName := strings.TrimPrefix(str, "$")
			if val, ok := os.LookupEnv(varName); ok {
				return []byte(val)
			}
		}
		return m
	})
}

func LoadConfig(filePath string) (*Config, error) {
	targetPath := filePath
	if targetPath == "pgcaliper.yaml" || targetPath == "" {
		candidates := []string{
			"pgcaliper.yaml",
			"/etc/pgcaliper/pgcaliper.yaml",
		}
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates, home+"/.config/pgcaliper/pgcaliper.yaml")
		}
		for _, cand := range candidates {
			if _, err := os.Stat(cand); err == nil {
				targetPath = cand
				break
			}
		}
	}

	data, err := os.ReadFile(targetPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", targetPath, err)
	}

	expandedData := expandEnv(data)

	var cfg Config
	if err := yaml.Unmarshal(expandedData, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse yaml config: %w", err)
	}

	if cfg.Storage.Schema == "" {
		cfg.Storage.Schema = "_pgcaliper"
	}
	if cfg.Schedule.Interval == "" {
		cfg.Schedule.Interval = "1h"
	}
	if cfg.Schedule.RetentionDays <= 0 {
		cfg.Schedule.RetentionDays = 90
	}
	if cfg.Strategy.Mode == "" {
		cfg.Strategy.Mode = "schema"
	}
	if cfg.Strategy.TenantColumn == "" {
		cfg.Strategy.TenantColumn = "org_id"
	}
	if cfg.Strategy.DefaultQuotaBytes <= 0 {
		cfg.Strategy.DefaultQuotaBytes = 15 * 1024 * 1024 * 1024
	}
	if cfg.Engine.Concurrency <= 0 {
		cfg.Engine.Concurrency = 4
	}
	if cfg.Database.StatementTimeout == "" {
		cfg.Database.StatementTimeout = "10s"
	}

	return &cfg, nil
}
