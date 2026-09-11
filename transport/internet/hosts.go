package internet

import (
	"net"
	"strings"
)

func IsValidHTTPHost(request string, config string) bool {
	r := strings.ToLower(request)
	c := strings.ToLower(config)
	if strings.Contains(r, ":") {
		h, _, _ := net.SplitHostPort(r)
		return h == c
	}
	return r == c
}

// IsValidHTTPHostAny reports whether request matches any configured host.
func IsValidHTTPHostAny(request string, hosts []string) bool {
	if len(hosts) == 0 {
		return true
	}
	for _, host := range hosts {
		if IsValidHTTPHost(request, host) {
			return true
		}
	}
	return false
}

// PickFirstHost returns the first configured host or fallback.
func PickFirstHost(hosts []string, fallback string) string {
	if len(hosts) > 0 && hosts[0] != "" {
		return hosts[0]
	}
	return fallback
}
