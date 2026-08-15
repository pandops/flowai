// Package config loads Web UI process configuration.
package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
)

const defaultPort = 8080

type Config struct {
	BindHost string
	BindPort int
}

func Load() (Config, error) {
	cfg := Config{
		BindHost: os.Getenv("WEB_UI_BIND_HOST"),
		BindPort: defaultPort,
	}
	if cfg.BindHost == "" {
		cfg.BindHost = "0.0.0.0"
	}
	if raw := os.Getenv("WEB_UI_BIND_PORT"); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || port < 0 || port > 65535 {
			return Config{}, fmt.Errorf("WEB_UI_BIND_PORT must be an integer from 0 to 65535")
		}
		cfg.BindPort = port
	}
	return cfg, nil
}

func (c Config) BindAddress() string {
	return net.JoinHostPort(c.BindHost, strconv.Itoa(c.BindPort))
}
