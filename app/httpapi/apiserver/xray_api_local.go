// In-process equivalents of main/commands/all/api gRPC calls.
// Uses the same protobuf request/operation types (AddInboundRequest, AlterInboundRequest, …)
// without modifying upstream packages or dialing grpc_api.

package apiserver

import (
	"context"

	"github.com/xtls/xray-core/app/commander"
	proxymancmd "github.com/xtls/xray-core/app/proxyman/command"
	routercmd "github.com/xtls/xray-core/app/router/command"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/protocol"
	cserial "github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/inbound"
	"github.com/xtls/xray-core/proxy"
	"github.com/xtls/xray-core/proxy/shadowsocks"
	"github.com/xtls/xray-core/proxy/shadowsocks_2022"
	"github.com/xtls/xray-core/proxy/trojan"
	vlessin "github.com/xtls/xray-core/proxy/vless/inbound"
	vmessin "github.com/xtls/xray-core/proxy/vmess/inbound"
)

// localAlterInbound mirrors HandlerService.AlterInbound (see inbound_user_add.go in commands/all/api).
func (s *Server) localAlterInbound(ctx context.Context, req *proxymancmd.AlterInboundRequest) error {
	if req == nil || req.Tag == "" {
		return errors.New("inbound tag is required")
	}
	rawOperation, err := req.Operation.GetInstance()
	if err != nil {
		return errors.New("unknown operation").Base(err)
	}
	operation, ok := rawOperation.(proxymancmd.InboundOperation)
	if !ok {
		return errors.New("not an inbound operation")
	}
	handler, err := s.ihm.GetHandler(ctx, req.Tag)
	if err != nil {
		return errors.New("failed to get handler: ", req.Tag).Base(err)
	}
	return operation.ApplyInbound(ctx, handler)
}

func (s *Server) protoAddInbound(ctx context.Context, built *core.InboundHandlerConfig) error {
	if built == nil || built.Tag == "" {
		return errors.New("inbound tag is required")
	}
	// Same as commands/all/api/inbounds_add.go → AddInboundRequest
	return core.AddInboundHandler(s.instance, built)
}

func (s *Server) protoRemoveInbound(ctx context.Context, tag string) error {
	if tag == "" {
		return errors.New("tag is required")
	}
	return s.ihm.RemoveHandler(ctx, tag)
}

func (s *Server) protoReplaceInbound(ctx context.Context, built *core.InboundHandlerConfig) error {
	if _, err := s.ihm.GetHandler(ctx, built.Tag); err != nil {
		return errors.New("inbound not found: ", built.Tag)
	}
	if err := s.protoRemoveInbound(ctx, built.Tag); err != nil {
		return errors.New("remove inbound ", built.Tag).Base(err)
	}
	return s.protoAddInbound(ctx, built)
}

// protoReplaceInboundPreserveUsers rebuilds an inbound while copying live users onto the new handler config.
// Callers must supply a built inbound whose ProxySettings clients/users may be empty or incomplete;
// runtime users always win.
func (s *Server) protoReplaceInboundPreserveUsers(ctx context.Context, built *core.InboundHandlerConfig) (int, error) {
	if built == nil || built.Tag == "" {
		return 0, errors.New("inbound tag is required")
	}
	handler, err := s.ihm.GetHandler(ctx, built.Tag)
	if err != nil {
		return 0, errors.New("inbound not found: ", built.Tag)
	}
	users, count, err := runtimeUsersFromHandler(ctx, handler)
	if err != nil {
		return 0, err
	}
	if err := injectUsersIntoInboundConfig(built, users); err != nil {
		return 0, err
	}
	if err := s.protoRemoveInbound(ctx, built.Tag); err != nil {
		return 0, errors.New("remove inbound ", built.Tag).Base(err)
	}
	if err := s.protoAddInbound(ctx, built); err != nil {
		return count, errors.New("add inbound ", built.Tag).Base(err)
	}
	return count, nil
}

func runtimeUsersFromHandler(ctx context.Context, handler inbound.Handler) ([]*protocol.User, int, error) {
	p, err := getInbound(handler)
	if err != nil {
		return nil, 0, err
	}
	um, ok := p.(proxy.UserManager)
	if !ok {
		return nil, 0, nil
	}
	mem := um.GetUsers(ctx)
	out := make([]*protocol.User, 0, len(mem))
	for _, mu := range mem {
		if mu == nil {
			continue
		}
		out = append(out, protocol.ToProtoUser(mu))
	}
	return out, len(out), nil
}

func injectUsersIntoInboundConfig(built *core.InboundHandlerConfig, users []*protocol.User) error {
	if built == nil || built.ProxySettings == nil {
		return errors.New("inbound proxy settings missing")
	}
	inst, err := built.ProxySettings.GetInstance()
	if err != nil || inst == nil {
		return errors.New("inbound proxy settings instance").Base(err)
	}
	switch ty := inst.(type) {
	case *vmessin.Config:
		ty.User = users
		built.ProxySettings = cserial.ToTypedMessage(ty)
	case *vlessin.Config:
		ty.Users = users
		built.ProxySettings = cserial.ToTypedMessage(ty)
	case *trojan.ServerConfig:
		ty.Users = users
		built.ProxySettings = cserial.ToTypedMessage(ty)
	case *shadowsocks.ServerConfig:
		ty.Users = users
		built.ProxySettings = cserial.ToTypedMessage(ty)
	case *shadowsocks_2022.MultiUserServerConfig:
		ty.Users = users
		built.ProxySettings = cserial.ToTypedMessage(ty)
	default:
		if len(users) > 0 {
			return errors.New("inbound type does not support preserving users")
		}
	}
	return nil
}

func (s *Server) protoAddOutbound(ctx context.Context, built *core.OutboundHandlerConfig) error {
	if built == nil || built.Tag == "" {
		return errors.New("outbound tag is required")
	}
	return core.AddOutboundHandler(s.instance, built)
}

func (s *Server) protoRemoveOutbound(ctx context.Context, tag string) error {
	if tag == "" {
		return errors.New("tag is required")
	}
	return s.ohm.RemoveHandler(ctx, tag)
}

func (s *Server) protoReplaceOutbound(ctx context.Context, built *core.OutboundHandlerConfig) error {
	tag := built.Tag
	h := s.ohm.GetHandler(tag)
	if h == nil {
		return errors.New("outbound not found: ", tag)
	}
	if _, ok := h.(*commander.Outbound); ok {
		return errors.New("cannot replace internal outbound: ", tag)
	}
	if err := s.protoRemoveOutbound(ctx, tag); err != nil {
		return errors.New("remove outbound ", tag).Base(err)
	}
	return s.protoAddOutbound(ctx, built)
}

func (s *Server) protoAlterAddUser(ctx context.Context, tag string, user *protocol.User) error {
	if user == nil || user.Email == "" {
		return errors.New("user email is required")
	}
	return s.localAlterInbound(ctx, &proxymancmd.AlterInboundRequest{
		Tag: tag,
		Operation: cserial.ToTypedMessage(&proxymancmd.AddUserOperation{
			User: user,
		}),
	})
}

func (s *Server) protoAlterRemoveUser(ctx context.Context, tag, email string) error {
	if email == "" {
		return errors.New("email is required")
	}
	return s.localAlterInbound(ctx, &proxymancmd.AlterInboundRequest{
		Tag: tag,
		Operation: cserial.ToTypedMessage(&proxymancmd.RemoveUserOperation{
			Email: email,
		}),
	})
}

func (s *Server) protoReplaceUser(ctx context.Context, tag string, user *protocol.User) error {
	if err := s.protoAlterRemoveUser(ctx, tag, user.Email); err != nil {
		return err
	}
	return s.protoAlterAddUser(ctx, tag, user)
}

func (s *Server) protoAddRule(ctx context.Context, config *cserial.TypedMessage, shouldAppend bool) error {
	if config == nil {
		return errors.New("rule config is required")
	}
	_, err := s.routingSvc.AddRule(ctx, &routercmd.AddRuleRequest{
		Config:       config,
		ShouldAppend: shouldAppend,
	})
	return err
}

func (s *Server) protoRemoveRule(ctx context.Context, ruleTag string) error {
	if ruleTag == "" {
		return errors.New("rule_tag is required")
	}
	_, err := s.routingSvc.RemoveRule(ctx, &routercmd.RemoveRuleRequest{RuleTag: ruleTag})
	return err
}
