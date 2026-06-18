package conf

import (
	"github.com/xtls/xray-core/app/httpapi"
	"github.com/xtls/xray-core/common/errors"
)

// HTTPAPIConfig is the config for the optional REST/JSON HTTP API server.
// This file is part of the fork extension; keep it separate from upstream api.go.
type HTTPAPIConfig struct {
	Listen     string `json:"listen"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	ConfigPath string `json:"config_path"`
}

func (c *HTTPAPIConfig) Build() (*httpapi.Config, error) {
	if c.Listen == "" {
		return nil, errors.New("httpapi.listen is required")
	}
	return &httpapi.Config{
		Listen:     c.Listen,
		Username:   c.Username,
		Password:   c.Password,
		ConfigPath: c.ConfigPath,
	}, nil
}
