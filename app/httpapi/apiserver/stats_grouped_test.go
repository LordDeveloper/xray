package apiserver

import "testing"

func TestInferGroupsFromPattern(t *testing.T) {
	tests := []struct {
		pattern string
		want    []string
	}{
		{"", []string{"inbound", "outbound", "user"}},
		{"inbound>>>", []string{"inbound"}},
		{"inbound>>>new-in>>>traffic>>>", []string{"inbound"}},
		{"outbound>>>proxy>>>traffic>>>uplink", []string{"outbound"}},
		{"user>>>a@x.com>>>traffic>>>", []string{"user"}},
		{"(uplink|downlink)", []string{"inbound", "outbound", "user"}},
	}
	for _, tc := range tests {
		got := inferGroupsFromPattern(tc.pattern)
		for _, cat := range tc.want {
			if !got[cat] {
				t.Fatalf("pattern %q: expected group %q", tc.pattern, cat)
			}
		}
		for cat := range got {
			found := false
			for _, w := range tc.want {
				if w == cat {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("pattern %q: unexpected group %q", tc.pattern, cat)
			}
		}
	}
}

func TestGroupedStatsToFilteredMap(t *testing.T) {
	g := newGroupedStats()
	g.putTraffic("inbound", "vless-in", "uplink", 100)
	g.putTraffic("outbound", "proxy", "downlink", 200)
	include := map[string]bool{"inbound": true}
	out := g.toFilteredMap(include)
	if _, ok := out["inbound"]; !ok {
		t.Fatal("expected inbound")
	}
	if _, ok := out["outbound"]; ok {
		t.Fatal("outbound should be omitted")
	}
}

func TestResolveGroupFilterExplicit(t *testing.T) {
	include, err := resolveGroupFilter("inbound>>>", "user,outbound")
	if err != nil {
		t.Fatal(err)
	}
	if include["inbound"] || !include["user"] || !include["outbound"] {
		t.Fatalf("unexpected include map: %v", include)
	}
}
