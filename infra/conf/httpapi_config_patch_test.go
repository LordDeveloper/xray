package conf

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/xtls/xray-core/app/httpapi/apiserver"
)

func TestConcurrentUserPatchesDoNotDropClients(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	seed := []byte(`{
	  "inbounds": [{
	    "tag": "in",
	    "listen": "127.0.0.1",
	    "port": 443,
	    "protocol": "vless",
	    "settings": {
	      "decryption": "none",
	      "clients": [{
	        "id": "11111111-1111-4111-8111-111111111111",
	        "email": "seed@x"
	      }]
	    }
	  }],
	  "outbounds": [{"protocol": "freedom", "tag": "direct"}]
	}`)
	if err := os.WriteFile(path, seed, 0o644); err != nil {
		t.Fatal(err)
	}

	users := []struct {
		email string
		id    string
	}{
		{"a@x", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
		{"b@x", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"},
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(users))
	for _, user := range users {
		wg.Add(1)
		go func(email, id string) {
			defer wg.Done()
			raw := json.RawMessage(`{"clients":[{"id":"` + id + `","email":"` + email + `"}]}`)
			errs <- PatchConfigInboundUsers(path, []apiserver.InboundUserFilePatch{{
				Tag:      "in",
				Settings: &raw,
			}})
		}(user.email, user.id)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	root, err := readConfigRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	inbound, _, err := findInboundMapInRoot(root, "in")
	if err != nil {
		t.Fatal(err)
	}
	settings, _ := inbound["settings"].(map[string]interface{})
	clients, _ := settings["clients"].([]interface{})
	got := map[string]bool{}
	for _, row := range clients {
		item, _ := row.(map[string]interface{})
		email, _ := item["email"].(string)
		got[email] = true
	}
	for _, email := range []string{"seed@x", "a@x", "b@x"} {
		if !got[email] {
			t.Fatalf("config.json lost %s; emails=%v", email, got)
		}
	}
}
