package internet_test

import (
	"testing"

	"github.com/xtls/xray-core/transport/internet"
)

func TestIsValidHTTPHostAny(t *testing.T) {
	hosts := []string{"eu1-cf.nim4you.ir", "cdn.nim4you.ir"}

	if !internet.IsValidHTTPHostAny("eu1-cf.nim4you.ir", hosts) {
		t.Fatal("expected primary host to match")
	}
	if !internet.IsValidHTTPHostAny("cdn.nim4you.ir:443", hosts) {
		t.Fatal("expected secondary host with port to match")
	}
	if internet.IsValidHTTPHostAny("example.com", hosts) {
		t.Fatal("unexpected host match")
	}
	if !internet.IsValidHTTPHostAny("anything.example", nil) {
		t.Fatal("empty host list should accept any host")
	}
}

func TestPickFirstHost(t *testing.T) {
	if got := internet.PickFirstHost([]string{"a", "b"}, "fallback"); got != "a" {
		t.Fatalf("unexpected host: %s", got)
	}
	if got := internet.PickFirstHost(nil, "fallback"); got != "fallback" {
		t.Fatalf("unexpected fallback: %s", got)
	}
}
