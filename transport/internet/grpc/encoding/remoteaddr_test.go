package encoding

import (
	"context"
	"net"
	"testing"

	http_proto "github.com/xtls/xray-core/common/protocol/http"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

func TestRemoteAddrFromContext(t *testing.T) {
	tests := []struct {
		name                  string
		metadata              metadata.MD
		settings              http_proto.RemoteAddrSettings
		expectedRemoteAddress string
	}{
		{
			name:                  "trust X-Forwarded-For when configured",
			metadata:              metadata.Pairs("X-Forwarded-For", "2.2.2.2, 3.3.3.3"),
			settings:              http_proto.RemoteAddrSettings{Headers: []string{"X-Forwarded-For"}},
			expectedRemoteAddress: "2.2.2.2:0",
		},
		{
			name:                  "trust X-Forwarded-For with trusted marker",
			metadata:              metadata.Pairs("X-Forwarded-For", "4.4.4.4", "X-Trusted-CDN", "1"),
			settings:              http_proto.RemoteAddrSettings{Headers: []string{"X-Trusted-CDN", "X-Forwarded-For"}},
			expectedRemoteAddress: "4.4.4.4:0",
		},
		{
			name:                  "ignore X-Forwarded-For without trusted marker",
			metadata:              metadata.Pairs("X-Forwarded-For", "5.5.5.5"),
			settings:              http_proto.RemoteAddrSettings{Headers: []string{"X-Trusted-CDN"}},
			expectedRemoteAddress: "127.0.0.1:12345",
		},
		{
			name:                  "read IP directly from CF-Connecting-IP",
			metadata:              metadata.Pairs("CF-Connecting-IP", "203.0.113.10"),
			settings:              http_proto.RemoteAddrSettings{Headers: []string{"CF-Connecting-IP"}},
			expectedRemoteAddress: "203.0.113.10:0",
		},
		{
			name:                  "skip cloudflare IP in X-Forwarded-For chain",
			metadata:              metadata.Pairs("X-Forwarded-For", "203.0.113.10, 172.64.144.180"),
			settings:              http_proto.RemoteAddrSettings{Headers: []string{"X-Forwarded-For"}, SkipCfIPs: true},
			expectedRemoteAddress: "203.0.113.10:0",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := peer.NewContext(metadata.NewIncomingContext(context.Background(), test.metadata), &peer.Peer{
				Addr: &net.TCPAddr{
					IP:   net.ParseIP("127.0.0.1"),
					Port: 12345,
				},
			})
			remoteAddr := remoteAddrFromContext(ctx, test.settings)
			if remoteAddr.String() != test.expectedRemoteAddress {
				t.Fatalf("unexpected remote address: %s", remoteAddr.String())
			}
		})
	}
}
