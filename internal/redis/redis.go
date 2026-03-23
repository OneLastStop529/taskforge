// Package redis provides shared connection config and client construction.
package redis

import (
	"fmt"

	goredis "github.com/redis/go-redis/v9"
)

// Config holds shared Redis connection settings.
type Config struct {
	Addr     string
	Username string
	Password string
	DB       int
}

// Validate checks whether the config is usable for a Redis client.
func (c Config) Validate() error {
	if c.Addr == "" {
		return fmt.Errorf("taskforge: redis address must not be empty")
	}
	return nil
}

// NewClient constructs a go-redis client from shared connection config.
func NewClient(cfg Config) (*goredis.Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return goredis.NewClient(&goredis.Options{
		Addr:     cfg.Addr,
		Username: cfg.Username,
		Password: cfg.Password,
		DB:       cfg.DB,
	}), nil
}
