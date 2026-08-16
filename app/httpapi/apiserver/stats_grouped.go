package apiserver

import (
	"strings"

	"github.com/xtls/xray-core/app/stats"
	statscmd "github.com/xtls/xray-core/app/stats/command"
	"github.com/xtls/xray-core/common/errors"
	feature_stats "github.com/xtls/xray-core/features/stats"
)

// groupedStats is inbound/outbound/user traffic grouped by tag or email.
type groupedStats struct {
	Inbound  map[string]map[string]int64 `json:"inbound"`
	Outbound map[string]map[string]int64 `json:"outbound"`
	User     map[string]map[string]int64 `json:"user"`
	Online   map[string]int64            `json:"online,omitempty"`
	Other    map[string]int64            `json:"other,omitempty"`
}

func newGroupedStats() *groupedStats {
	return &groupedStats{
		Inbound:  make(map[string]map[string]int64),
		Outbound: make(map[string]map[string]int64),
		User:     make(map[string]map[string]int64),
		Online:   make(map[string]int64),
		Other:    make(map[string]int64),
	}
}

func (g *groupedStats) putTraffic(category, key, direction string, value int64) {
	var bucket map[string]map[string]int64
	switch category {
	case "inbound":
		bucket = g.Inbound
	case "outbound":
		bucket = g.Outbound
	case "user":
		bucket = g.User
	default:
		g.Other[category+">>>"+key+">>>"+direction] = value
		return
	}
	if bucket[key] == nil {
		bucket[key] = make(map[string]int64)
	}
	bucket[key][direction] = value
}

func groupStatsFromList(stats []*statscmd.Stat, pattern string) *groupedStats {
	out := newGroupedStats()
	for _, st := range stats {
		if st == nil || !matchStatPattern(st.Name, pattern) {
			continue
		}
		parts := strings.Split(st.Name, ">>>")
		switch {
		case len(parts) >= 4 && parts[2] == "traffic":
			out.putTraffic(parts[0], parts[1], parts[3], st.Value)
		case len(parts) == 3 && parts[0] == "user" && parts[2] == "online":
			out.Online[parts[1]] = st.Value
		default:
			out.Other[st.Name] = st.Value
		}
	}
	if len(out.Online) == 0 {
		out.Online = nil
	}
	if len(out.Other) == 0 {
		out.Other = nil
	}
	return out
}

func collectGroupedStats(m feature_stats.Manager, pattern string, reset bool) (*groupedStats, error) {
	manager, ok := m.(*stats.Manager)
	if !ok {
		return nil, errors.New("grouped stats only works with stats.Manager")
	}
	var entries []*statscmd.Stat
	manager.VisitCounters(func(name string, c feature_stats.Counter) bool {
		if !matchStatPattern(name, pattern) {
			return true
		}
		var value int64
		if reset {
			value = c.Set(0)
		} else {
			value = c.Value()
		}
		entries = append(entries, &statscmd.Stat{Name: name, Value: value})
		return true
	})
	return groupStatsFromList(entries, pattern), nil
}

// resolveGroupFilter decides which top-level groups to return.
// Explicit group= inbound,outbound,user overrides pattern inference.
func resolveGroupFilter(pattern, groupParam string) (map[string]bool, error) {
	if groupParam != "" {
		allowed := map[string]bool{}
		for _, part := range strings.Split(groupParam, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			switch part {
			case "inbound", "outbound", "user":
				allowed[part] = true
			default:
				return nil, errors.New("invalid group: ", part, " (use inbound, outbound, user)")
			}
		}
		if len(allowed) == 0 {
			return nil, errors.New("group parameter is empty")
		}
		return allowed, nil
	}
	return inferGroupsFromPattern(pattern), nil
}

func inferGroupsFromPattern(pattern string) map[string]bool {
	all := map[string]bool{"inbound": true, "outbound": true, "user": true}
	if pattern == "" {
		return all
	}
	out := map[string]bool{}
	for cat := range all {
		prefix := cat + ">>>"
		if strings.HasPrefix(pattern, prefix) || strings.Contains(pattern, prefix) {
			out[cat] = true
		}
	}
	if len(out) == 0 {
		return all
	}
	return out
}

func (g *groupedStats) toFilteredMap(include map[string]bool) map[string]interface{} {
	out := map[string]interface{}{}
	if include["inbound"] && len(g.Inbound) > 0 {
		out["inbound"] = g.Inbound
	}
	if include["outbound"] && len(g.Outbound) > 0 {
		out["outbound"] = g.Outbound
	}
	if include["user"] && len(g.User) > 0 {
		out["user"] = g.User
	}
	if include["user"] && len(g.Online) > 0 {
		out["online"] = g.Online
	}
	if len(g.Other) > 0 {
		out["other"] = g.Other
	}
	return out
}
