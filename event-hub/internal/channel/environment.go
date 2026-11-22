package channel

import (
	"errors"
	"log/slog"
	"time"
)

// FromEnvironment leaves infrastructure-only deployments unchanged when no channel endpoint is configured.
func FromEnvironment(getenv func(string) string, logger *slog.Logger) (*Supervisor, error) {
	base, token := getenv("EVENT_HUB_BACKEND_URL"), getenv("KIKO_HUB_TOKEN")
	if base == "" && token == "" {
		return nil, nil
	}
	backend, err := NewBackend(base, token)
	if err != nil {
		return nil, err
	}
	addr := getenv("EVENT_HUB_CONTROL_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8082"
	}
	interval := 30 * time.Second
	if raw := getenv("EVENT_HUB_CONFIG_INTERVAL"); raw != "" {
		interval, err = time.ParseDuration(raw)
		if err != nil || interval < time.Second {
			return nil, errors.New("invalid EVENT_HUB_CONFIG_INTERVAL")
		}
	}
	return NewSupervisor(backend, addr, interval, logger), nil
}
