package http_test

import (
	"bufio"
	gonet "net"
	"net/http"
	"strings"
	"testing"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/net"
	. "github.com/xtls/xray-core/common/protocol/http"
)

func TestApplyRemoteAddrHeaders(t *testing.T) {
	remoteAddr := &gonet.TCPAddr{IP: gonet.ParseIP("127.0.0.1"), Port: 12345}
	cfRemoteAddr := &gonet.TCPAddr{IP: gonet.ParseIP("172.64.144.180"), Port: 443}

	t.Run("ignore X-Forwarded-For without configured headers", func(t *testing.T) {
		header := http.Header{}
		header.Add("X-Forwarded-For", "129.78.138.66, 129.78.64.103")

		if addr := ApplyRemoteAddrHeaders(header, RemoteAddrSettings{}, remoteAddr); addr != remoteAddr {
			t.Fatalf("unexpected remote address: %v", addr)
		}
	})

	t.Run("read IP from configured header list", func(t *testing.T) {
		header := http.Header{}
		header.Add("X-Forwarded-For", "129.78.138.66, 129.78.64.103")

		addr := ApplyRemoteAddrHeaders(header, RemoteAddrSettings{Headers: []string{"X-Forwarded-For"}}, remoteAddr)
		if addr.String() != "129.78.138.66:0" {
			t.Fatalf("unexpected remote address: %v", addr)
		}
	})

	t.Run("read IP from later header when earlier marker has no IP", func(t *testing.T) {
		header := http.Header{}
		header.Add("X-Forwarded-For", "129.78.138.66, 129.78.64.103")
		header.Add("X-Trusted-CDN", "")

		addr := ApplyRemoteAddrHeaders(header, RemoteAddrSettings{Headers: []string{"X-Trusted-CDN", "X-Forwarded-For"}}, remoteAddr)
		if addr.String() != "129.78.138.66:0" {
			t.Fatalf("unexpected remote address: %v", addr)
		}
	})

	t.Run("ignore non-IP header values", func(t *testing.T) {
		header := http.Header{}
		header.Add("X-Forwarded-For", "example.com")
		header.Add("X-Trusted-CDN", "")

		if addr := ApplyRemoteAddrHeaders(header, RemoteAddrSettings{Headers: []string{"X-Trusted-CDN", "X-Forwarded-For"}}, remoteAddr); addr != remoteAddr {
			t.Fatalf("unexpected remote address: %v", addr)
		}
	})

	t.Run("read IP directly from CF-Connecting-IP", func(t *testing.T) {
		header := http.Header{}
		header.Add("CF-Connecting-IP", "203.0.113.10")

		addr := ApplyRemoteAddrHeaders(header, RemoteAddrSettings{Headers: []string{"CF-Connecting-IP"}}, cfRemoteAddr)
		if addr.String() != "203.0.113.10:0" {
			t.Fatalf("unexpected remote address: %v", addr)
		}
	})

	t.Run("skip cloudflare IP in X-Forwarded-For chain when skipCfIPs enabled", func(t *testing.T) {
		header := http.Header{}
		header.Add("X-Forwarded-For", "203.0.113.10, 172.64.144.180")

		addr := ApplyRemoteAddrHeaders(header, RemoteAddrSettings{Headers: []string{"X-Forwarded-For"}, SkipCfIPs: true}, cfRemoteAddr)
		if addr.String() != "203.0.113.10:0" {
			t.Fatalf("unexpected remote address: %v", addr)
		}
	})

	t.Run("keep cloudflare IP in chain when skipCfIPs disabled", func(t *testing.T) {
		header := http.Header{}
		header.Add("X-Forwarded-For", "172.64.144.180, 203.0.113.10")

		addr := ApplyRemoteAddrHeaders(header, RemoteAddrSettings{Headers: []string{"X-Forwarded-For"}, SkipCfIPs: false}, cfRemoteAddr)
		if addr.String() != "172.64.144.180:0" {
			t.Fatalf("unexpected remote address: %v", addr)
		}
	})

	t.Run("skip cloudflare-only CF-Connecting-IP and use X-Forwarded-For", func(t *testing.T) {
		header := http.Header{}
		header.Add("CF-Connecting-IP", "172.64.144.180")
		header.Add("X-Forwarded-For", "203.0.113.10, 172.64.144.180")

		addr := ApplyRemoteAddrHeaders(header, RemoteAddrSettings{
			Headers:   []string{"CF-Connecting-IP", "X-Forwarded-For"},
			SkipCfIPs: true,
		}, cfRemoteAddr)
		if addr.String() != "203.0.113.10:0" {
			t.Fatalf("unexpected remote address: %v", addr)
		}
	})

	t.Run("leave non-http transports unchanged without configured headers", func(t *testing.T) {
		header := http.Header{}
		if addr := ApplyRemoteAddrHeaders(header, RemoteAddrSettings{}, cfRemoteAddr); addr != cfRemoteAddr {
			t.Fatalf("unexpected remote address: %v", addr)
		}
	})
}

func TestHopByHopHeadersRemoving(t *testing.T) {
	rawRequest := `GET /pkg/net/http/ HTTP/1.1
Host: golang.org
Connection: keep-alive,Foo, Bar
Foo: foo
Bar: bar
Proxy-Connection: keep-alive
Proxy-Authenticate: abc
Accept-Encoding: gzip
Accept-Charset: ISO-8859-1,UTF-8;q=0.7,*;q=0.7
Cache-Control: no-cache
Accept-Language: de,en;q=0.7,en-us;q=0.3

`
	b := bufio.NewReader(strings.NewReader(rawRequest))
	req, err := http.ReadRequest(b)
	common.Must(err)
	headers := []struct {
		Key   string
		Value string
	}{
		{
			Key:   "Foo",
			Value: "foo",
		},
		{
			Key:   "Bar",
			Value: "bar",
		},
		{
			Key:   "Connection",
			Value: "keep-alive,Foo, Bar",
		},
		{
			Key:   "Proxy-Connection",
			Value: "keep-alive",
		},
		{
			Key:   "Proxy-Authenticate",
			Value: "abc",
		},
	}
	for _, header := range headers {
		if v := req.Header.Get(header.Key); v != header.Value {
			t.Error("header ", header.Key, " = ", v, " want ", header.Value)
		}
	}

	RemoveHopByHopHeaders(req.Header)

	for _, header := range []string{"Connection", "Foo", "Bar", "Proxy-Connection", "Proxy-Authenticate"} {
		if v := req.Header.Get(header); v != "" {
			t.Error("header ", header, " = ", v)
		}
	}
}

func TestParseHost(t *testing.T) {
	testCases := []struct {
		RawHost     string
		DefaultPort net.Port
		Destination net.Destination
		Error       bool
	}{
		{
			RawHost:     "example.com:80",
			DefaultPort: 443,
			Destination: net.TCPDestination(net.DomainAddress("example.com"), 80),
		},
		{
			RawHost:     "tls.example.com",
			DefaultPort: 443,
			Destination: net.TCPDestination(net.DomainAddress("tls.example.com"), 443),
		},
		{
			RawHost:     "[2401:1bc0:51f0:ec08::1]:80",
			DefaultPort: 443,
			Destination: net.TCPDestination(net.ParseAddress("[2401:1bc0:51f0:ec08::1]"), 80),
		},
	}

	for _, testCase := range testCases {
		dest, err := ParseHost(testCase.RawHost, testCase.DefaultPort)
		if testCase.Error {
			if err == nil {
				t.Error("for test case: ", testCase.RawHost, " expected error, but actually nil")
			}
		} else {
			if dest != testCase.Destination {
				t.Error("for test case: ", testCase.RawHost, " expected host: ", testCase.Destination.String(), " but got ", dest.String())
			}
		}
	}
}
