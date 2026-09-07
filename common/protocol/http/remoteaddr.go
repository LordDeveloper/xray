package http

import (
	"context"
	"net"
	"strings"

	"github.com/xtls/xray-core/common/errors"
	xnet "github.com/xtls/xray-core/common/net"
)

// RemoteAddrSettings controls how inbound remote addresses are resolved from proxy headers.
type RemoteAddrSettings struct {
	Headers   []string
	SkipCfIPs bool
}

// RemoteAddrResolver resolves a client address from configured proxy headers.
type RemoteAddrResolver struct {
	settings RemoteAddrSettings
}

// NewRemoteAddrResolver creates a resolver for sockopt remote address settings.
func NewRemoteAddrResolver(settings RemoteAddrSettings) *RemoteAddrResolver {
	if len(settings.Headers) == 0 {
		return nil
	}
	copied := make([]string, len(settings.Headers))
	copy(copied, settings.Headers)
	settings.Headers = copied
	return &RemoteAddrResolver{settings: settings}
}

func parseHeaderIPs(value string) []xnet.Address {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	addrs := make([]xnet.Address, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		addr := xnet.ParseAddress(part)
		if addr.Family().IsIP() {
			addrs = append(addrs, addr)
		}
	}
	return addrs
}

func firstUsableIP(addrs []xnet.Address, skipCfIPs bool) xnet.Address {
	for _, addr := range addrs {
		if !shouldSkipRemoteIP(addr.IP(), skipCfIPs) {
			return addr
		}
	}
	return nil
}

func addrFromRemoteIP(addr xnet.Address) net.Addr {
	return &xnet.TCPAddr{
		IP:   addr.IP(),
		Port: 0,
	}
}

func remoteIPFromAddr(remoteAddr net.Addr) net.IP {
	if remoteAddr == nil {
		return nil
	}
	host, _, err := net.SplitHostPort(remoteAddr.String())
	if err != nil {
		return net.ParseIP(remoteAddr.String())
	}
	return net.ParseIP(host)
}

// Resolve returns the client address from configured headers.
func (r *RemoteAddrResolver) Resolve(headers HeaderReader, remoteAddr net.Addr) net.Addr {
	if r == nil || len(r.settings.Headers) == 0 {
		return remoteAddr
	}

	for _, name := range r.settings.Headers {
		if len(headers.Values(name)) == 0 {
			continue
		}
		if addr := firstUsableIP(parseHeaderIPs(headers.Get(name)), r.settings.SkipCfIPs); addr != nil {
			return addrFromRemoteIP(addr)
		}
	}

	if ip := remoteIPFromAddr(remoteAddr); ip != nil && !shouldSkipRemoteIP(ip, r.settings.SkipCfIPs) {
		return remoteAddr
	}

	errors.LogDebug(context.Background(), "skipped proxy/CDN remote address ", remoteAddr, `; configure "sockopt.trustedXForwardedFor" with headers such as CF-Connecting-IP and X-Forwarded-For`)
	return remoteAddr
}
