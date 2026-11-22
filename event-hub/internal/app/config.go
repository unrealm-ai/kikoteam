package app

import (
	"errors"
	"fmt"
	"time"

	"github.com/unrealm-ai/kikoteam/event-hub/internal/election"
)

type Config struct {
	Election         election.Config
	MigrationTimeout time.Duration
	ShutdownTimeout  time.Duration
	HealthAddr       string
}

func LoadConfig(getenv func(string) string) (Config, error) {
	c := Config{
		Election: election.Config{DSN: getenv("EVENT_HUB_DATABASE_URL"), ConnectTimeout: 5 * time.Second,
			RetryInterval: 2 * time.Second, HeartbeatInterval: 3 * time.Second, HeartbeatTimeout: 2 * time.Second},
		MigrationTimeout: 10 * time.Minute, ShutdownTimeout: 15 * time.Second, HealthAddr: "127.0.0.1:8081",
	}
	if addr := getenv("EVENT_HUB_HEALTH_ADDR"); addr != "" {
		c.HealthAddr = addr
	}
	for name, target := range map[string]*time.Duration{
		"EVENT_HUB_CONNECT_TIMEOUT":    &c.Election.ConnectTimeout,
		"EVENT_HUB_RETRY_INTERVAL":     &c.Election.RetryInterval,
		"EVENT_HUB_HEARTBEAT_INTERVAL": &c.Election.HeartbeatInterval,
		"EVENT_HUB_HEARTBEAT_TIMEOUT":  &c.Election.HeartbeatTimeout,
		"EVENT_HUB_MIGRATION_TIMEOUT":  &c.MigrationTimeout,
		"EVENT_HUB_SHUTDOWN_TIMEOUT":   &c.ShutdownTimeout,
	} {
		if value := getenv(name); value != "" {
			d, err := time.ParseDuration(value)
			if err != nil || d <= 0 {
				return c, fmt.Errorf("%s must be a positive duration", name)
			}
			*target = d
		}
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	if err := c.Election.Validate(); err != nil {
		return err
	}
	if c.MigrationTimeout <= 0 || c.ShutdownTimeout <= 0 {
		return errors.New("migration and shutdown timeouts must be positive")
	}
	if c.HealthAddr == "" {
		return errors.New("health address is required")
	}
	return nil
}
