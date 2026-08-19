package apiserver

import "testing"

func TestIncludeUserStatWhenOnlineOnly(t *testing.T) {
	online := map[string]bool{
		"a@x.com": true,
		"b@x.com": true,
	}
	if !includeUserStatWhenOnlineOnly("user>>>a@x.com>>>traffic>>>uplink", online) {
		t.Fatal("expected online user stat")
	}
	if includeUserStatWhenOnlineOnly("user>>>c@x.com>>>traffic>>>uplink", online) {
		t.Fatal("expected offline user excluded")
	}
	if includeUserStatWhenOnlineOnly("inbound>>>in1>>>traffic>>>uplink", online) {
		t.Fatal("expected non-user stat excluded")
	}
}

func TestGroupedStatsFilterToOnlineUsers(t *testing.T) {
	g := newGroupedStats()
	g.putTraffic("user", "a@x.com", "uplink", 100)
	g.putTraffic("user", "b@x.com", "downlink", 200)
	g.Online = map[string]int64{"a@x.com": 2, "b@x.com": 0, "c@x.com": 1}
	g.filterToOnlineUsers(map[string]bool{"a@x.com": true, "c@x.com": true})
	if _, ok := g.User["b@x.com"]; ok {
		t.Fatal("offline user should be removed from traffic")
	}
	if _, ok := g.User["a@x.com"]; !ok {
		t.Fatal("online user traffic should remain")
	}
	if _, ok := g.Online["b@x.com"]; ok {
		t.Fatal("offline user should be removed from online map")
	}
	if g.Online["c@x.com"] != 1 {
		t.Fatal("online session count for c@x.com")
	}
}

func TestOnlineUserTrafficResponse(t *testing.T) {
	g := newGroupedStats()
	g.putTraffic("user", "u@x.com", "uplink", 10)
	g.putTraffic("user", "u@x.com", "downlink", 20)
	g.Online = map[string]int64{"u@x.com": 1, "v@x.com": 2}
	out := onlineUserTrafficResponse(g)
	users, ok := out["users"].(map[string]map[string]int64)
	if !ok {
		t.Fatal("users map")
	}
	if users["u@x.com"]["uplink"] != 10 || users["u@x.com"]["sessions"] != 1 {
		t.Fatal("u@x.com entry")
	}
	if users["v@x.com"]["sessions"] != 2 {
		t.Fatal("v@x.com sessions-only entry")
	}
}
