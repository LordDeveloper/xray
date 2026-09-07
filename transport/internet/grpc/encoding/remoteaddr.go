package encoding

import (
	"context"
	"net"

	"github.com/xtls/xray-core/common/errors"
	xnet "github.com/xtls/xray-core/common/net"
	http_proto "github.com/xtls/xray-core/common/protocol/http"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

type mdHeaderReader struct {
	md metadata.MD
}

func (r mdHeaderReader) Get(key string) string {
	values := r.md.Get(key)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (r mdHeaderReader) Values(key string) []string {
	return r.md.Get(key)
}

func remoteAddrFromContext(ctx context.Context, trusted []string) net.Addr {
	var remoteAddr net.Addr
	if pr, ok := peer.FromContext(ctx); ok {
		remoteAddr = pr.Addr
	} else {
		remoteAddr = &xnet.TCPAddr{
			IP:   []byte{0, 0, 0, 0},
			Port: 0,
		}
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return remoteAddr
	}

	resolver := http_proto.NewRemoteAddrResolver(trusted)
	if resolver == nil {
		if values := md.Get("X-Forwarded-For"); len(values) > 0 && values[0] != "" {
			errors.LogWarning(context.Background(), `received "X-Forwarded-For" from `, remoteAddr, ` but "sockopt.trustedXForwardedFor" is not configured; ignoring it and using the real remote address`)
		}
		return remoteAddr
	}

	return resolver.Resolve(mdHeaderReader{md: md}, remoteAddr)
}
