package apiserver

import (
	"encoding/json"

	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/proto"
)

// ConfigBridge wires infra/conf builders without importing that package (avoids import cycles).
// It is registered from infra/conf/httpapi_bridge.go via init().
var ConfigBridge struct {
	BuildInboundHandler     func(raw json.RawMessage) (*core.InboundHandlerConfig, error)
	BuildInboundProxyOnly   func(tag, protocol string, settings *json.RawMessage) (*core.InboundHandlerConfig, error)
	BuildOutboundHandler    func(raw json.RawMessage) (*core.OutboundHandlerConfig, error)
	BuildRouterRules        func(routing json.RawMessage) (proto.Message, error)
	BuildRouterRulesFromStr func(partialJSON string) (proto.Message, error)
}

func mustBridge() {
	if ConfigBridge.BuildInboundHandler == nil {
		panic("httpapi: ConfigBridge not registered; import _ \"github.com/xtls/xray-core/infra/conf\"")
	}
}
