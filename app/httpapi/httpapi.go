package httpapi

import (
	"context"
	"net/http"

	"github.com/xtls/xray-core/app/httpapi/apiserver"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/core"
)

// Handler is an optional Xray app that exposes a REST/JSON HTTP API.
type Handler struct {
	instance *core.Instance
	config   *Config
	inner    *apiserver.Server
}

// NewHandler creates a new HTTP API handler from config.
func NewHandler(ctx context.Context, config *Config) (*Handler, error) {
	if config == nil || config.Listen == "" {
		return nil, errors.New("httpapi: listen is required")
	}
	return &Handler{
		instance: core.MustFromContext(ctx),
		config:   config,
	}, nil
}

// Type implements common.HasType.
func (h *Handler) Type() interface{} {
	return (*Handler)(nil)
}

// Start implements common.Runnable.
func (h *Handler) Start() error {
	s, err := apiserver.New(h.instance, apiserver.Options{
		Listen:     h.config.GetListen(),
		Username:   h.config.GetUsername(),
		Password:   h.config.GetPassword(),
		ConfigPath: h.config.GetConfigPath(),
	})
	if err != nil {
		return err
	}
	h.inner = s
	go func() {
		if err := s.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errors.LogErrorInner(context.Background(), err, "httpapi server stopped")
		}
	}()
	errors.LogInfo(context.Background(), "HTTP API listening on ", h.config.GetListen())
	return nil
}

// Close implements common.Closable.
func (h *Handler) Close() error {
	if h.inner != nil {
		return h.inner.Close()
	}
	return nil
}

func init() {
	common.Must(common.RegisterConfig((*Config)(nil), func(ctx context.Context, cfg interface{}) (interface{}, error) {
		return NewHandler(ctx, cfg.(*Config))
	}))
}
