package conf

import (
	"context"
	"encoding/json"

	"github.com/xtls/xray-core/app/commander"
	"github.com/xtls/xray-core/app/httpapi/apiserver"
	"github.com/xtls/xray-core/common/errors"
	cserial "github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
)

// HTTPAPIApplyResult reports what was hot-reloaded after config import.
type HTTPAPIApplyResult struct {
	Inbounds  *HTTPAPISyncCounts `json:"inbounds,omitempty"`
	Outbounds *HTTPAPISyncCounts `json:"outbounds,omitempty"`
	Routing   bool               `json:"routing,omitempty"`
	Skipped   []string           `json:"skipped,omitempty"`
}

// HTTPAPISyncCounts counts inbound/outbound sync operations.
type HTTPAPISyncCounts struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
	Updated int `json:"updated"`
}

func init() {
	apiserver.ConfigBridge.ApplyRuntimeConfig = applyHTTPAPIRuntimeConfig
}

func applyHTTPAPIRuntimeConfig(target apiserver.RuntimeApplyTarget, data []byte) (interface{}, error) {
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, errors.New("decode config").Base(err)
	}
	if err := PostProcessConfigureFile(&cfg); err != nil {
		return nil, errors.New("post-process config").Base(err)
	}

	result := &HTTPAPIApplyResult{
		Skipped: []string{
			"log (restart logger via API if needed)",
			"dns",
			"policy",
			"transport",
			"api/metrics/httpapi listen address",
		},
	}
	ctx := context.Background()

	inRes, err := syncHTTPAPIInbounds(ctx, target, &cfg)
	if err != nil {
		return nil, err
	}
	result.Inbounds = inRes

	outRes, err := syncHTTPAPIOutbounds(ctx, target, &cfg)
	if err != nil {
		return nil, err
	}
	result.Outbounds = outRes

	if cfg.RouterConfig != nil {
		rc, err := cfg.RouterConfig.Build()
		if err != nil {
			return nil, errors.New("build routing").Base(err)
		}
		tmsg := cserial.ToTypedMessage(rc)
		if tmsg == nil {
			return nil, errors.New("failed to encode routing config")
		}
		if err := target.Router.AddRule(tmsg, false); err != nil {
			return nil, errors.New("apply routing").Base(err)
		}
		result.Routing = true
	}

	return result, nil
}

func syncHTTPAPIInbounds(ctx context.Context, target apiserver.RuntimeApplyTarget, cfg *Config) (*HTTPAPISyncCounts, error) {
	res := &HTTPAPISyncCounts{}
	desired := make(map[string]*InboundDetourConfig)
	for i := range cfg.InboundConfigs {
		inb := &cfg.InboundConfigs[i]
		if inb.Tag == "" {
			continue
		}
		desired[inb.Tag] = inb
	}

	for _, h := range target.Inbound.ListHandlers(ctx) {
		tag := h.Tag()
		if _, ok := desired[tag]; !ok {
			if err := target.Inbound.RemoveHandler(ctx, tag); err != nil {
				return nil, errors.New("remove inbound ", tag).Base(err)
			}
			res.Removed++
		}
	}

	for tag, inb := range desired {
		exists := false
		if _, err := target.Inbound.GetHandler(ctx, tag); err == nil {
			exists = true
			if err := target.Inbound.RemoveHandler(ctx, tag); err != nil {
				return nil, errors.New("update inbound ", tag).Base(err)
			}
		}
		built, err := inb.Build()
		if err != nil {
			return nil, errors.New("build inbound ", tag).Base(err)
		}
		if err := core.AddInboundHandler(target.Instance, built); err != nil {
			return nil, errors.New("add inbound ", tag).Base(err)
		}
		if exists {
			res.Updated++
		} else {
			res.Added++
		}
	}
	return res, nil
}

func syncHTTPAPIOutbounds(ctx context.Context, target apiserver.RuntimeApplyTarget, cfg *Config) (*HTTPAPISyncCounts, error) {
	res := &HTTPAPISyncCounts{}
	desired := make(map[string]*OutboundDetourConfig)
	for i := range cfg.OutboundConfigs {
		out := &cfg.OutboundConfigs[i]
		if out.Tag == "" {
			continue
		}
		desired[out.Tag] = out
	}

	for _, h := range target.Outbound.ListHandlers(ctx) {
		if _, ok := h.(*commander.Outbound); ok {
			continue
		}
		tag := h.Tag()
		if _, ok := desired[tag]; !ok {
			if err := target.Outbound.RemoveHandler(ctx, tag); err != nil {
				return nil, errors.New("remove outbound ", tag).Base(err)
			}
			res.Removed++
		}
	}

	for tag, out := range desired {
		exists := false
		if h := target.Outbound.GetHandler(tag); h != nil {
			if _, ok := h.(*commander.Outbound); ok {
				continue
			}
			exists = true
			if err := target.Outbound.RemoveHandler(ctx, tag); err != nil {
				return nil, errors.New("update outbound ", tag).Base(err)
			}
		}
		built, err := out.Build()
		if err != nil {
			return nil, errors.New("build outbound ", tag).Base(err)
		}
		if err := core.AddOutboundHandler(target.Instance, built); err != nil {
			return nil, errors.New("add outbound ", tag).Base(err)
		}
		if exists {
			res.Updated++
		} else {
			res.Added++
		}
	}
	return res, nil
}
