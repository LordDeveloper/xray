package http

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/net"
)

// HeaderReader reads HTTP-style header values.
type HeaderReader interface {
	Get(key string) string
	Values(key string) []string
}

// ApplyTrustedXForwardedFor resolves remoteAddr from sockopt.trustedXForwardedFor header names.
// Each entry is a header to inspect in order. Proxy/CDN addresses such as Cloudflare are skipped.
// When the list is empty the connection address is returned unchanged.
func ApplyTrustedXForwardedFor(header http.Header, trusted []string, remoteAddr net.Addr) net.Addr {
	if len(trusted) == 0 {
		if header.Get("X-Forwarded-For") != "" {
			errors.LogWarning(context.Background(), `received "X-Forwarded-For" from `, remoteAddr, ` but "sockopt.trustedXForwardedFor" is not configured; ignoring it and using the real remote address`)
		}
		return remoteAddr
	}

	resolver := NewRemoteAddrResolver(trusted)
	resolved := resolver.Resolve(header, remoteAddr)
	if resolved != remoteAddr {
		return resolved
	}

	if header.Get("X-Forwarded-For") != "" {
		for _, name := range trusted {
			if strings.EqualFold(name, "X-Forwarded-For") {
				return remoteAddr
			}
		}
		errors.LogDebug(context.Background(), `no usable client IP found in configured remote address headers from `, remoteAddr)
	}
	return remoteAddr
}

// RemoveHopByHopHeaders removes hop by hop headers in http header list.
func RemoveHopByHopHeaders(header http.Header) {
	// Strip hop-by-hop header based on RFC:
	// http://www.w3.org/Protocols/rfc2616/rfc2616-sec13.html#sec13.5.1
	// https://www.mnot.net/blog/2011/07/11/what_proxies_must_do

	header.Del("Proxy-Connection")
	header.Del("Proxy-Authenticate")
	header.Del("Proxy-Authorization")
	header.Del("TE")
	header.Del("Trailers")
	header.Del("Transfer-Encoding")
	header.Del("Upgrade")

	connections := header.Get("Connection")
	header.Del("Connection")
	if connections == "" {
		return
	}
	for _, h := range strings.Split(connections, ",") {
		header.Del(strings.TrimSpace(h))
	}
}

// ParseHost splits host and port from a raw string. Default port is used when raw string doesn't contain port.
func ParseHost(rawHost string, defaultPort net.Port) (net.Destination, error) {
	port := defaultPort
	host, rawPort, err := net.SplitHostPort(rawHost)
	if err != nil {
		if addrError, ok := err.(*net.AddrError); ok && strings.Contains(addrError.Err, "missing port") {
			host = rawHost
		} else {
			return net.Destination{}, err
		}
	} else if len(rawPort) > 0 {
		intPort, err := strconv.Atoi(rawPort)
		if err != nil {
			return net.Destination{}, err
		}
		port = net.Port(intPort)
	}

	return net.TCPDestination(net.ParseAddress(host), port), nil
}
