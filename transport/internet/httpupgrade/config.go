package httpupgrade

import (
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/transport/internet"
)

func (c *Config) EffectiveHosts() []string {
	if len(c.Hosts) > 0 {
		return c.Hosts
	}
	if c.Host != "" {
		return []string{c.Host}
	}
	return nil
}

func (c *Config) GetNormalizedPath() string {
	path := c.Path
	if path == "" {
		return "/"
	}
	if path[0] != '/' {
		return "/" + path
	}
	return path
}

func init() {
	common.Must(internet.RegisterProtocolConfigCreator(protocolName, func() interface{} {
		return new(Config)
	}))
}
