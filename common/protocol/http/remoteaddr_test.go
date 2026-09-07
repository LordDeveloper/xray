package http_test

import (
	"testing"

	. "github.com/xtls/xray-core/common/protocol/http"
)

func TestShouldSkipCloudflareIP(t *testing.T) {
	cases := []struct {
		ip     string
		skip   bool
	}{
		{"172.64.144.180", true},
		{"104.16.1.1", true},
		{"203.0.113.10", false},
		{"129.78.138.66", false},
	}

	for _, testCase := range cases {
		resolver := NewRemoteAddrResolver(RemoteAddrSettings{
			Headers:   []string{"X-Forwarded-For"},
			SkipCfIPs: true,
		})
		header := mapHeader{"X-Forwarded-For": testCase.ip + ", 203.0.113.10"}
		addr := resolver.Resolve(header, &fakeAddr{value: "172.64.144.180:443"})
		if testCase.skip {
			if addr.String() != "203.0.113.10:0" {
				t.Fatalf("expected fallback IP for %s, got %s", testCase.ip, addr.String())
			}
			continue
		}
		if addr.String() != testCase.ip+":0" {
			t.Fatalf("expected %s:0, got %s", testCase.ip, addr.String())
		}
	}
}

type mapHeader map[string]string

func (m mapHeader) Get(key string) string {
	return m[key]
}

func (m mapHeader) Values(key string) []string {
	if v, ok := m[key]; ok {
		return []string{v}
	}
	return nil
}

type fakeAddr struct {
	value string
}

func (f *fakeAddr) Network() string { return "tcp" }
func (f *fakeAddr) String() string  { return f.value }
