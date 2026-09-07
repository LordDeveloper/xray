package http

import (
	"context"
	"net"
	"strings"

	"github.com/xtls/xray-core/common/errors"
	xnet "github.com/xtls/xray-core/common/net"
)

// RemoteAddrResolver resolves a client address from configured proxy headers.
// When no headers are configured it leaves the connection address unchanged.
type RemoteAddrResolver struct {
	headers []string
}

// NewRemoteAddrResolver creates a resolver for sockopt.trustedXForwardedFor header names.
func NewRemoteAddrResolver(headers []string) *RemoteAddrResolver {
	if len(headers) == 0 {
		return nil
	}
	copied := make([]string, len(headers))
	copy(copied, headers)
	return &RemoteAddrResolver{headers: copied}
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

func firstNonSkippedIP(addrs []xnet.Address) xnet.Address {
	for _, addr := range addrs {
		if !shouldSkipRemoteIP(addr.IP()) {
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

// Resolve returns the client address from configured headers, skipping known proxy/CDN hops.
func (r *RemoteAddrResolver) Resolve(headers HeaderReader, remoteAddr net.Addr) net.Addr {
	if r == nil || len(r.headers) == 0 {
		return remoteAddr
	}

	for _, name := range r.headers {
		if len(headers.Values(name)) == 0 {
			continue
		}
		if addr := firstNonSkippedIP(parseHeaderIPs(headers.Get(name))); addr != nil {
			return addrFromRemoteIP(addr)
		}
	}

	if ip := remoteIPFromAddr(remoteAddr); ip != nil && !shouldSkipRemoteIP(ip) {
		return remoteAddr
	}

	errors.LogDebug(context.Background(), "skipped proxy/CDN remote address ", remoteAddr, `; configure "sockopt.trustedXForwardedFor" with headers such as CF-Connecting-IP and X-Forwarded-For`)
	return remoteAddr
}
