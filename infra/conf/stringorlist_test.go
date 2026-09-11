package conf_test

import (
	"encoding/json"
	"testing"

	"github.com/xtls/xray-core/infra/conf"
	"github.com/xtls/xray-core/transport/internet/tls"
	"github.com/xtls/xray-core/transport/internet/websocket"
)

func TestStringOrListUnmarshal(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		var host conf.StringOrList
		if err := json.Unmarshal([]byte(`"eu1-cf.nim4you.ir"`), &host); err != nil {
			t.Fatal(err)
		}
		if host.First() != "eu1-cf.nim4you.ir" {
			t.Fatalf("unexpected host: %v", host)
		}
	})

	t.Run("array", func(t *testing.T) {
		var host conf.StringOrList
		if err := json.Unmarshal([]byte(`["a.example.com","b.example.com"]`), &host); err != nil {
			t.Fatal(err)
		}
		if len(host) != 2 || host.First() != "a.example.com" {
			t.Fatalf("unexpected host: %v", host)
		}
	})
}

func TestWebSocketConfigHostArray(t *testing.T) {
	raw := []byte(`{
		"path": "/ws",
		"host": ["eu1-cf.nim4you.ir", "cdn.nim4you.ir"]
	}`)
	var cfg conf.WebSocketConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	msg, err := cfg.Build()
	if err != nil {
		t.Fatal(err)
	}
	ws := msg.(*websocket.Config)
	if ws.Host != "eu1-cf.nim4you.ir" {
		t.Fatalf("unexpected primary host: %s", ws.Host)
	}
	if len(ws.Hosts) != 2 {
		t.Fatalf("unexpected hosts: %v", ws.Hosts)
	}
}

func TestTLSConfigServerNameArray(t *testing.T) {
	raw := []byte(`{
		"serverName": ["chess.com", "iloveimg.com"]
	}`)
	var cfg conf.TLSConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	msg, err := cfg.Build()
	if err != nil {
		t.Fatal(err)
	}
	tlsCfg := msg.(*tls.Config)
	if tlsCfg.ServerName != "chess.com" {
		t.Fatalf("unexpected primary server name: %s", tlsCfg.ServerName)
	}
	if len(tlsCfg.ServerNames) != 2 {
		t.Fatalf("unexpected server names: %v", tlsCfg.ServerNames)
	}
}
