package conf

import (
	"encoding/json"
	"strings"

	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	cserial "github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/dokodemo"
	"github.com/xtls/xray-core/proxy/freedom"
	"github.com/xtls/xray-core/proxy/shadowsocks"
	"github.com/xtls/xray-core/proxy/shadowsocks_2022"
	"github.com/xtls/xray-core/proxy/trojan"
	"github.com/xtls/xray-core/proxy/vless"
	vlessin "github.com/xtls/xray-core/proxy/vless/inbound"
	"github.com/xtls/xray-core/proxy/vmess"
	vmessin "github.com/xtls/xray-core/proxy/vmess/inbound"
	"github.com/xtls/xray-core/transport/internet"
)

var inboundProxyTypeToProtocol = map[string]string{
	"xray.proxy.vless.inbound.Config":            "vless",
	"xray.proxy.vmess.inbound.Config":            "vmess",
	"xray.proxy.trojan.ServerConfig":             "trojan",
	"xray.proxy.shadowsocks.ServerConfig":        "shadowsocks",
	"xray.proxy.shadowsocks_2022.MultiUserServerConfig": "shadowsocks",
	"xray.proxy.shadowsocks_2022.ServerConfig":   "shadowsocks",
	"xray.proxy.dokodemo.Config":                 "dokodemo-door",
	"xray.proxy.http.ServerConfig":                 "http",
	"xray.proxy.socks.ServerConfig":              "socks",
	"xray.proxy.wireguard.DeviceConfig":          "wireguard",
	"xray.proxy.hysteria.ServerConfig":           "hysteria",
	"xray.proxy.tun.DeviceConfig":                "tun",
}

var outboundProxyTypeToProtocol = map[string]string{
	"xray.proxy.freedom.Config":           "freedom",
	"xray.proxy.blackhole.Config":         "blackhole",
	"xray.proxy.loopback.Config":          "loopback",
	"xray.proxy.http.ClientConfig":        "http",
	"xray.proxy.socks.ClientConfig":       "socks",
	"xray.proxy.vless.outbound.Config":    "vless",
	"xray.proxy.vmess.outbound.Config":    "vmess",
	"xray.proxy.trojan.ClientConfig":      "trojan",
	"xray.proxy.shadowsocks.ClientConfig": "shadowsocks",
	"xray.proxy.dns.Config":               "dns",
	"xray.proxy.hysteria.ClientConfig":    "hysteria",
	"xray.proxy.wireguard.DeviceConfig":   "wireguard",
}

// InboundFromCore converts runtime inbound handler config to JSON config shape.
func InboundFromCore(in *core.InboundHandlerConfig) (*InboundDetourConfig, error) {
	if in == nil {
		return nil, nil
	}
	out := &InboundDetourConfig{Tag: in.Tag}
	if in.ReceiverSettings != nil {
		recvMsg, err := in.ReceiverSettings.GetInstance()
		if err != nil {
			return nil, err
		}
		if receiver, ok := recvMsg.(*proxyman.ReceiverConfig); ok {
			if receiver.Listen != nil {
				addr := receiver.Listen.AsAddress()
				out.ListenOn = &Address{Address: addr}
			}
			if receiver.PortList != nil {
				out.PortList = portListFromProto(receiver.PortList)
			}
			if receiver.SniffingSettings != nil {
				out.SniffingConfig = sniffingFromProto(receiver.SniffingSettings)
			}
		}
	}
	if in.ProxySettings != nil {
		protoName, ok := inboundProxyTypeToProtocol[in.ProxySettings.Type]
		if !ok {
			protoName = strings.TrimPrefix(in.ProxySettings.Type, "xray.proxy.")
		}
		out.Protocol = protoName
		settings, err := inboundSettingsFromTypedMessage(in.ProxySettings)
		if err != nil {
			return nil, err
		}
		out.Settings = settings
	}
	return out, nil
}

// OutboundFromCore converts runtime outbound handler config to JSON config shape.
func OutboundFromCore(ob *core.OutboundHandlerConfig) (*OutboundDetourConfig, error) {
	if ob == nil {
		return nil, nil
	}
	out := &OutboundDetourConfig{Tag: ob.Tag}
	if ob.SenderSettings != nil {
		senderMsg, err := ob.SenderSettings.GetInstance()
		if err != nil {
			return nil, err
		}
		if sender, ok := senderMsg.(*proxyman.SenderConfig); ok {
			if sender.StreamSettings != nil {
				out.StreamSetting = streamConfigFromInternet(sender.StreamSettings)
			}
		}
	}
	if ob.ProxySettings != nil {
		protoName, ok := outboundProxyTypeToProtocol[ob.ProxySettings.Type]
		if !ok {
			protoName = strings.TrimPrefix(ob.ProxySettings.Type, "xray.proxy.")
		}
		out.Protocol = protoName
		settings, err := outboundSettingsFromTypedMessage(ob.ProxySettings)
		if err != nil {
			return nil, err
		}
		out.Settings = settings
	}
	return out, nil
}

// InboundWithUsers overlays runtime users onto exported inbound settings.
func InboundWithUsers(ib *InboundDetourConfig, users []*protocol.User) error {
	if ib == nil || len(users) == 0 {
		return nil
	}
	clients, err := ClientsFromUsers(ib.Protocol, users)
	if err != nil {
		return err
	}
	if len(clients) == 0 {
		return nil
	}
	ib.Settings, err = MergeClientsIntoSettings(ib.Settings, ib.Protocol, clients)
	return err
}

// ConfigClientsFromMemoryUsers returns inbound client objects in config.json shape.
func ConfigClientsFromMemoryUsers(protocolName string, users []*protocol.MemoryUser) ([]interface{}, error) {
	if len(users) == 0 {
		return []interface{}{}, nil
	}
	protoUsers := make([]*protocol.User, 0, len(users))
	for _, u := range users {
		if u == nil {
			continue
		}
		protoUsers = append(protoUsers, protocol.ToProtoUser(u))
	}
	raws, err := ClientsFromUsers(protocolName, protoUsers)
	if err != nil {
		return nil, err
	}
	out := make([]interface{}, 0, len(raws))
	for _, raw := range raws {
		var item interface{}
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// OverlayInboundClients replaces settings.clients with live users in config.json format.
func OverlayInboundClients(inbound map[string]interface{}, protocolName string, users []*protocol.MemoryUser) error {
	if inbound == nil {
		return nil
	}
	switch protocolName {
	case "vless", "vmess", "trojan", "shadowsocks":
	default:
		return nil
	}
	clients, err := ConfigClientsFromMemoryUsers(protocolName, users)
	if err != nil {
		return err
	}
	settings, _ := inbound["settings"].(map[string]interface{})
	if settings == nil {
		settings = make(map[string]interface{})
		inbound["settings"] = settings
	}
	settings["clients"] = clients
	delete(settings, "users")
	return nil
}

// ClientsFromUsers builds clients array for inbound settings from live users.
func ClientsFromUsers(protocolName string, users []*protocol.User) ([]json.RawMessage, error) {
	if len(users) == 0 {
		return nil, nil
	}
	switch protocolName {
	case "vless":
		return vlessClientsFromUsers(users)
	case "vmess":
		return vmessClientsFromUsers(users)
	case "trojan":
		return trojanClientsFromUsers(users)
	case "shadowsocks":
		return ssClientsFromUsers(users)
	default:
		return nil, nil
	}
}

// MergeClientsIntoSettings replaces clients/users in inbound settings JSON.
func MergeClientsIntoSettings(settings *json.RawMessage, protocolName string, clients []json.RawMessage) (*json.RawMessage, error) {
	base := map[string]interface{}{}
	if settings != nil && len(*settings) > 0 {
		if err := json.Unmarshal(*settings, &base); err != nil {
			return nil, err
		}
	}
	switch protocolName {
	case "shadowsocks":
		base["clients"] = clients
		delete(base, "users")
	default:
		base["clients"] = clients
		delete(base, "users")
	}
	b, err := json.Marshal(base)
	if err != nil {
		return nil, err
	}
	rm := json.RawMessage(b)
	return &rm, nil
}

func vlessClientsFromUsers(users []*protocol.User) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(users))
	for _, u := range users {
		if u == nil {
			continue
		}
		acc, err := u.GetTypedAccount()
		if err != nil {
			continue
		}
		va, ok := acc.(*vless.MemoryAccount)
		if !ok {
			continue
		}
		item := map[string]interface{}{
			"id":    va.ID.String(),
			"email": u.Email,
		}
		if va.Flow != "" {
			item["flow"] = va.Flow
		}
		b, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

func vmessClientsFromUsers(users []*protocol.User) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(users))
	for _, u := range users {
		if u == nil {
			continue
		}
		acc, err := u.GetTypedAccount()
		if err != nil {
			continue
		}
		va, ok := acc.(*vmess.MemoryAccount)
		if !ok {
			continue
		}
		item := map[string]interface{}{
			"id":    va.ID.String(),
			"email": u.Email,
		}
		if va.Security != protocol.SecurityType_AUTO {
			item["security"] = securityTypeToString(va.Security)
		}
		b, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

func trojanClientsFromUsers(users []*protocol.User) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(users))
	for _, u := range users {
		if u == nil {
			continue
		}
		acc, err := u.GetTypedAccount()
		if err != nil {
			continue
		}
		ta, ok := acc.(*trojan.MemoryAccount)
		if !ok {
			continue
		}
		item := map[string]interface{}{
			"password": ta.Password,
			"email":    u.Email,
		}
		b, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

func ssClientsFromUsers(users []*protocol.User) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(users))
	for _, u := range users {
		if u == nil {
			continue
		}
		acc, err := u.GetTypedAccount()
		if err != nil {
			continue
		}
		sa, ok := acc.(*shadowsocks.MemoryAccount)
		if !ok {
			continue
		}
		item := map[string]interface{}{
			"email":    u.Email,
			"password": sa.Password,
			"method":   cipherTypeToConfString(sa.CipherType),
		}
		b, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

func securityTypeToString(st protocol.SecurityType) string {
	switch st {
	case protocol.SecurityType_AES128_GCM:
		return "aes-128-gcm"
	case protocol.SecurityType_CHACHA20_POLY1305:
		return "chacha20-poly1305"
	case protocol.SecurityType_NONE:
		return "none"
	case protocol.SecurityType_ZERO:
		return "zero"
	default:
		return "auto"
	}
}

func cipherTypeToConfString(t shadowsocks.CipherType) string {
	switch t {
	case shadowsocks.CipherType_AES_128_GCM:
		return "aes-128-gcm"
	case shadowsocks.CipherType_AES_256_GCM:
		return "aes-256-gcm"
	case shadowsocks.CipherType_CHACHA20_POLY1305:
		return "chacha20-poly1305"
	case shadowsocks.CipherType_XCHACHA20_POLY1305:
		return "xchacha20-poly1305"
	case shadowsocks.CipherType_NONE:
		return "none"
	default:
		return ""
	}
}

func portListFromProto(pl *net.PortList) *PortList {
	if pl == nil || len(pl.Range) == 0 {
		return nil
	}
	out := &PortList{}
	for _, r := range pl.Range {
		out.Range = append(out.Range, PortRange{
			From: r.From,
			To:   r.To,
		})
	}
	return out
}

func sniffingFromProto(s *proxyman.SniffingConfig) *SniffingConfig {
	if s == nil {
		return nil
	}
	out := &SniffingConfig{
		Enabled:      s.Enabled,
		MetadataOnly: s.MetadataOnly,
		RouteOnly:    s.RouteOnly,
	}
	for _, p := range s.DestinationOverride {
		switch p {
		case "http":
			out.DestOverride = append(out.DestOverride, "http")
		case "tls":
			out.DestOverride = append(out.DestOverride, "tls")
		case "quic":
			out.DestOverride = append(out.DestOverride, "quic")
		case "fakedns":
			out.DestOverride = append(out.DestOverride, "fakedns")
		}
	}
	return out
}

func inboundSettingsFromTypedMessage(tm *cserial.TypedMessage) (*json.RawMessage, error) {
	if tm == nil {
		return nil, nil
	}
	msg, err := tm.GetInstance()
	if err != nil {
		return nil, err
	}
	switch cfg := msg.(type) {
	case *vlessin.Config:
		return vlessInboundSettingsToConf(cfg)
	case *vmessin.Config:
		return vmessInboundSettingsToConf(cfg)
	case *trojan.ServerConfig:
		return trojanInboundSettingsToConf(cfg)
	case *shadowsocks.ServerConfig:
		return ssInboundSettingsToConf(cfg)
	case *shadowsocks_2022.MultiUserServerConfig:
		return ss2022InboundSettingsToConf(cfg)
	case *shadowsocks_2022.ServerConfig:
		return ss2022SingleInboundSettingsToConf(cfg)
	case *dokodemo.Config:
		return dokodemoInboundSettingsToConf(cfg)
	default:
		b, err := json.Marshal(msg)
		if err != nil {
			return nil, err
		}
		rm := json.RawMessage(b)
		return &rm, nil
	}
}

func outboundSettingsFromTypedMessage(tm *cserial.TypedMessage) (*json.RawMessage, error) {
	if tm == nil {
		return nil, nil
	}
	msg, err := tm.GetInstance()
	if err != nil {
		return nil, err
	}
	switch cfg := msg.(type) {
	case *freedom.Config:
		out := map[string]interface{}{}
		if cfg.DomainStrategy != 0 {
			switch cfg.DomainStrategy {
			case internet.DomainStrategy_USE_IP:
				out["domainStrategy"] = "UseIP"
			case internet.DomainStrategy_USE_IP4:
				out["domainStrategy"] = "UseIPv4"
			case internet.DomainStrategy_USE_IP6:
				out["domainStrategy"] = "UseIPv6"
			case internet.DomainStrategy_FORCE_IP:
				out["domainStrategy"] = "ForceIP"
			default:
				out["domainStrategy"] = "AsIs"
			}
		}
		b, err := json.Marshal(out)
		if err != nil {
			return nil, err
		}
		rm := json.RawMessage(b)
		return &rm, nil
	default:
		b, err := json.Marshal(msg)
		if err != nil {
			return nil, err
		}
		rm := json.RawMessage(b)
		return &rm, nil
	}
}

func vlessInboundSettingsToConf(config *vlessin.Config) (*json.RawMessage, error) {
	out := &VLessInboundConfig{Decryption: config.Decryption}
	for _, fb := range config.Fallbacks {
		dest, _ := json.Marshal(fb.Dest)
		out.Fallbacks = append(out.Fallbacks, &VLessInboundFallback{
			Name: fb.Name, Alpn: fb.Alpn, Path: fb.Path, Type: fb.Type,
			Dest: json.RawMessage(dest), Xver: fb.Xver,
		})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	rm := json.RawMessage(b)
	return &rm, nil
}

func vmessInboundSettingsToConf(config *vmessin.Config) (*json.RawMessage, error) {
	out := map[string]interface{}{}
	if config.Default != nil {
		out["default"] = config.Default
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	rm := json.RawMessage(b)
	return &rm, nil
}

func trojanInboundSettingsToConf(config *trojan.ServerConfig) (*json.RawMessage, error) {
	out := map[string]interface{}{}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	rm := json.RawMessage(b)
	return &rm, nil
}

func ssInboundSettingsToConf(config *shadowsocks.ServerConfig) (*json.RawMessage, error) {
	out := map[string]interface{}{}
	_ = config
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	rm := json.RawMessage(b)
	return &rm, nil
}

func ss2022InboundSettingsToConf(config *shadowsocks_2022.MultiUserServerConfig) (*json.RawMessage, error) {
	out := &ShadowsocksServerConfig{Cipher: config.Method}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	rm := json.RawMessage(b)
	return &rm, nil
}

func ss2022SingleInboundSettingsToConf(config *shadowsocks_2022.ServerConfig) (*json.RawMessage, error) {
	out := &ShadowsocksServerConfig{Cipher: config.Method}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	rm := json.RawMessage(b)
	return &rm, nil
}

func dokodemoInboundSettingsToConf(config *dokodemo.Config) (*json.RawMessage, error) {
	out := map[string]interface{}{
		"followRedirect": config.FollowRedirect,
	}
	if config.RewriteAddress != nil {
		addr := config.RewriteAddress.AsAddress()
		out["address"] = addr.String()
	}
	if config.RewritePort != 0 {
		out["port"] = config.RewritePort
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	rm := json.RawMessage(b)
	return &rm, nil
}

func streamConfigFromInternet(ss *internet.StreamConfig) *StreamConfig {
	if ss == nil {
		return nil
	}
	out := &StreamConfig{}
	if ss.ProtocolName != "" && ss.ProtocolName != "tcp" {
		np := TransportProtocol(ss.ProtocolName)
		out.Network = &np
	}
	if strings.Contains(ss.SecurityType, "tls") {
		out.Security = "tls"
	} else if strings.Contains(ss.SecurityType, "reality") {
		out.Security = "reality"
	}
	return out
}
