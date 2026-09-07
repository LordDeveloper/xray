package internet

import http_proto "github.com/xtls/xray-core/common/protocol/http"

func RemoteAddrSettingsFromSocket(s *SocketConfig) http_proto.RemoteAddrSettings {
	if s == nil {
		return http_proto.RemoteAddrSettings{}
	}
	return http_proto.RemoteAddrSettings{
		Headers:   s.TrustedXForwardedFor,
		SkipCfIPs: s.SkipCfIPs,
	}
}
