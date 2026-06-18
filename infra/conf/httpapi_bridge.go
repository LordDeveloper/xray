package conf

import (
	"encoding/json"

	"github.com/xtls/xray-core/app/httpapi/apiserver"
	"github.com/xtls/xray-core/common/errors"
	cserial "github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/proto"
)

func init() {
	apiserver.ConfigBridge.BuildInboundHandler = func(raw json.RawMessage) (*core.InboundHandlerConfig, error) {
		var c InboundDetourConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		return c.Build()
	}
	apiserver.ConfigBridge.BuildInboundProxyOnly = func(tag, protocol string, settings *json.RawMessage) (*core.InboundHandlerConfig, error) {
		inbConf := &InboundDetourConfig{Tag: tag, Protocol: protocol, Settings: settings}
		proxyMsg, err := inbConf.BuildProxySettingsOnly()
		if err != nil {
			return nil, err
		}
		return &core.InboundHandlerConfig{ProxySettings: cserial.ToTypedMessage(proxyMsg)}, nil
	}
	apiserver.ConfigBridge.BuildOutboundHandler = func(raw json.RawMessage) (*core.OutboundHandlerConfig, error) {
		var c OutboundDetourConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		return c.Build()
	}
	apiserver.ConfigBridge.BuildRouterRules = func(routing json.RawMessage) (proto.Message, error) {
		var rc RouterConfig
		if err := json.Unmarshal(routing, &rc); err != nil {
			return nil, err
		}
		return rc.Build()
	}
	apiserver.ConfigBridge.BuildRouterRulesFromStr = func(partialJSON string) (proto.Message, error) {
		var cfg struct {
			Routing *RouterConfig `json:"routing"`
		}
		if err := json.Unmarshal([]byte(partialJSON), &cfg); err != nil {
			return nil, err
		}
		if cfg.Routing == nil {
			return nil, errors.New("routing rules required")
		}
		return cfg.Routing.Build()
	}
}
