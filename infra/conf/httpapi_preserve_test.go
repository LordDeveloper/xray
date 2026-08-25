package conf

import (
	"encoding/json"
	"testing"
)

func TestMergeInboundMapsPreserveClients(t *testing.T) {
	existing := map[string]interface{}{
		"tag":      "in-1",
		"protocol": "vless",
		"listen":   "0.0.0.0",
		"port":     float64(443),
		"settings": map[string]interface{}{
			"decryption": "none",
			"clients": []interface{}{
				map[string]interface{}{"email": "a@x.com", "id": "111"},
				map[string]interface{}{"email": "b@x.com", "id": "222"},
			},
		},
		"streamSettings": map[string]interface{}{"network": "tcp"},
	}
	patch := map[string]interface{}{
		"tag":  "in-1",
		"port": float64(8443),
		"streamSettings": map[string]interface{}{
			"network": "ws",
			"wsSettings": map[string]interface{}{
				"path": "/ws",
			},
		},
		"settings": map[string]interface{}{
			"decryption": "none",
			"fallbacks":  []interface{}{},
			"clients": []interface{}{
				map[string]interface{}{"email": "should-be-ignored", "id": "999"},
			},
		},
	}
	merged := mergeInboundMapsPreserveClients(existing, patch)
	if merged["port"] != float64(8443) {
		t.Fatalf("port not updated: %v", merged["port"])
	}
	ss, _ := merged["streamSettings"].(map[string]interface{})
	if ss["network"] != "ws" {
		t.Fatalf("streamSettings not updated: %v", ss)
	}
	settings, _ := merged["settings"].(map[string]interface{})
	clients, _ := settings["clients"].([]interface{})
	if len(clients) != 2 {
		t.Fatalf("clients wiped or replaced: got %d", len(clients))
	}
	if _, ok := settings["fallbacks"]; !ok {
		t.Fatal("non-client settings field not merged")
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		t.Fatal(err)
	}
	if countClientsInInboundMap(merged) != 2 {
		t.Fatalf("clients_count want 2 got %d (%s)", countClientsInInboundMap(merged), raw)
	}
}
