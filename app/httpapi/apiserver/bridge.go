package apiserver

import (
	"encoding/json"

	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/inbound"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/routing"
	"google.golang.org/protobuf/proto"
)

// RuntimeApplyTarget passes live Xray handles into config apply logic (wired from infra/conf).
type RuntimeApplyTarget struct {
	Instance *core.Instance
	Inbound  inbound.Manager
	Outbound outbound.Manager
	Router   routing.Router
}

// InboundUserFilePatch carries validated user settings to merge into config.json.
type InboundUserFilePatch struct {
	Tag      string
	Settings *json.RawMessage
}

// ConfigBridge wires infra/conf builders without importing that package (avoids import cycles).
// It is registered from infra/conf/httpapi_bridge.go via init().
var ConfigBridge struct {
	BuildInboundHandler     func(raw json.RawMessage) (*core.InboundHandlerConfig, error)
	BuildInboundProxyOnly   func(tag, protocol string, settings *json.RawMessage) (*core.InboundHandlerConfig, error)
	BuildOutboundHandler    func(raw json.RawMessage) (*core.OutboundHandlerConfig, error)
	BuildRouterRules        func(routing json.RawMessage) (proto.Message, error)
	BuildRouterRulesFromStr func(partialJSON string) (proto.Message, error)
	ApplyRuntimeConfig      func(target RuntimeApplyTarget, data []byte) (interface{}, error)
	PatchConfigInbounds           func(configPath string, upsert []json.RawMessage, removeTags []string) error
	PatchConfigOutbounds          func(configPath string, upsert []json.RawMessage, removeTags []string) error
	PatchConfigInboundUsers       func(configPath string, patches []InboundUserFilePatch) error
	PatchConfigInboundUsersRemove func(configPath, tag string, emails []string) error
	ListConfigInbounds            func(configPath string, runtimeTags map[string]struct{}) ([]interface{}, error)
	ListConfigOutbounds           func(configPath string, runtimeTags map[string]struct{}) ([]interface{}, error)
	ListConfigRules               func(configPath string) ([]interface{}, error)
	ConfigClientsFromMemoryUsers  func(protocol string, users []*protocol.MemoryUser) ([]interface{}, error)
	OverlayInboundClients         func(inbound map[string]interface{}, protocol string, users []*protocol.MemoryUser) error
	PatchConfigRulesAdd           func(configPath string, routing json.RawMessage, prepend bool) error
	PatchConfigRulesRemove        func(configPath string, ruleTags []string, indices []int) error
	PatchConfigRulesEdit          func(configPath string, ruleTag string, rule json.RawMessage) error
	ValidateConfigBytes           func(data []byte) error
	ValidateInboundBatch          func(raws []json.RawMessage) error
	ValidateOutboundBatch         func(raws []json.RawMessage) error
	ValidateRoutingRules          func(routing json.RawMessage) error
	ValidateInboundUserSettings   func(protocol string, settings *json.RawMessage) error
	ValidateNonEmptyTags          func(field string, tags []string) error
	ValidateNonEmptyEmails        func(emails []string) error
}

func mustBridge() {
	if ConfigBridge.BuildInboundHandler == nil {
		panic("httpapi: ConfigBridge not registered; import _ \"github.com/xtls/xray-core/infra/conf\"")
	}
}

func (s *Server) applyRuntimeConfig(data []byte) (interface{}, error) {
	if ConfigBridge.ApplyRuntimeConfig == nil {
		return nil, errApplyNotRegistered
	}
	return ConfigBridge.ApplyRuntimeConfig(RuntimeApplyTarget{
		Instance: s.instance,
		Inbound:  s.ihm,
		Outbound: s.ohm,
		Router:   s.router,
	}, data)
}

var errApplyNotRegistered = &bridgeError{msg: "runtime config apply not registered"}

type bridgeError struct{ msg string }

func (e *bridgeError) Error() string { return e.msg }
