package apiserver

import (
	"strings"

	feature_stats "github.com/xtls/xray-core/features/stats"
)

func parseQueryBool(v string) bool {
	return v == "true" || v == "1"
}

func buildOnlineEmailSet(m feature_stats.Manager) map[string]bool {
	set := make(map[string]bool)
	for _, fullName := range m.GetAllOnlineUsers() {
		email := onlineUserNameToEmail(fullName)
		if email != "" {
			set[email] = true
		}
	}
	return set
}

// userEmailFromStatName returns the subscription email for user>>> counters.
func userEmailFromStatName(name string) (email string, isUser bool) {
	parts := strings.Split(name, ">>>")
	if len(parts) < 3 || parts[0] != "user" {
		return "", false
	}
	return parts[1], true
}

// includeUserStatWhenOnlineOnly reports whether a counter should appear when online_only is set.
// Only user>>>email>>>... counters for currently online emails are included.
func includeUserStatWhenOnlineOnly(name string, online map[string]bool) bool {
	email, isUser := userEmailFromStatName(name)
	if !isUser {
		return false
	}
	return online[email]
}

func (g *groupedStats) filterToOnlineUsers(online map[string]bool) {
	for email := range g.User {
		if !online[email] {
			delete(g.User, email)
		}
	}
	if len(g.Online) > 0 {
		for email := range g.Online {
			if !online[email] {
				delete(g.Online, email)
			}
		}
		if len(g.Online) == 0 {
			g.Online = nil
		}
	}
}

// onlineUserTrafficResponse builds the dedicated /api/stats/online/traffic payload.
func onlineUserTrafficResponse(g *groupedStats) map[string]interface{} {
	users := make(map[string]map[string]int64, len(g.User))
	for email, traffic := range g.User {
		entry := make(map[string]int64, len(traffic)+1)
		for k, v := range traffic {
			entry[k] = v
		}
		if g.Online != nil {
			if n, ok := g.Online[email]; ok {
				entry["sessions"] = n
			}
		}
		users[email] = entry
	}
	// Online users with no traffic counters yet (sessions only).
	if g.Online != nil {
		for email, sessions := range g.Online {
			if _, ok := users[email]; ok {
				continue
			}
			users[email] = map[string]int64{"sessions": sessions}
		}
	}
	return map[string]interface{}{"users": users}
}
