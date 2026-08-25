package apiserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xtls/xray-core/app/commander"
	"github.com/xtls/xray-core/app/log"
	routercmd "github.com/xtls/xray-core/app/router/command"
	"github.com/xtls/xray-core/app/stats"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/platform"
	cserial "github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/inbound"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/routing"
	feature_stats "github.com/xtls/xray-core/features/stats"
	"github.com/xtls/xray-core/proxy"
	"github.com/xtls/xray-core/proxy/shadowsocks"
	"github.com/xtls/xray-core/proxy/shadowsocks_2022"
	"github.com/xtls/xray-core/proxy/trojan"
	vlessin "github.com/xtls/xray-core/proxy/vless/inbound"
	vmessin "github.com/xtls/xray-core/proxy/vmess/inbound"
)

// Options configures the REST/JSON HTTP API server.
type Options struct {
	Listen     string
	Username   string
	Password   string
	ConfigPath string
}

// New prepares the HTTP API server for the given core instance.
func New(instance *core.Instance, opt Options) (*Server, error) {
	mustBridge()
	configPath := opt.ConfigPath
	if configPath == "" {
		configPath = platform.GetConfigurationPath()
	}
	s := &Server{
		instance:   instance,
		startTime:  time.Now(),
		configPath: configPath,
		authUser:   opt.Username,
		authPass:   opt.Password,
		listen:     opt.Listen,
	}
	common.Must(instance.RequireFeatures(func(im inbound.Manager, om outbound.Manager) {
		s.ihm = im
		s.ohm = om
	}, false))
	common.Must(instance.RequireFeatures(func(r routing.Router) {
		s.router = r
	}, false))
	common.Must(instance.RequireFeatures(func(sm feature_stats.Manager) {
		s.statsManager = sm
	}, false))
	s.routingSvc = routercmd.NewRoutingServer(s.router, nil)

	mux := http.NewServeMux()

	// Logger
	mux.HandleFunc("/api/logger/restart", s.handleLoggerRestart)

	// Stats
	mux.HandleFunc("/api/stats", s.handleGetStats)
	mux.HandleFunc("/api/stats/query", s.handleQueryStats)
	mux.HandleFunc("/api/stats/sys", s.handleSysStats)
	mux.HandleFunc("/api/stats/online", s.handleStatsOnline)
	mux.HandleFunc("/api/stats/online/iplist", s.handleStatsOnlineIpList)
	mux.HandleFunc("/api/stats/online/traffic", s.handleStatsOnlineTraffic)
	mux.HandleFunc("/api/stats/online/users", s.handleGetAllOnlineUsers)
	mux.HandleFunc("/api/stats/online/all", s.handleGetAllOnlineUsersWithIps)

	// Inbounds
	mux.HandleFunc("/api/inbounds/add", s.handleAddInbounds)
	mux.HandleFunc("/api/inbounds/edit", s.handleEditInbounds)
	mux.HandleFunc("/api/inbounds/remove", s.handleRemoveInbounds)
	mux.HandleFunc("/api/inbounds/list", s.handleListInbounds)
	mux.HandleFunc("/api/inbounds/users/add", s.handleAddInboundUsers)
	mux.HandleFunc("/api/inbounds/users/edit", s.handleEditInboundUsers)
	mux.HandleFunc("/api/inbounds/users/upsert", s.handleUpsertInboundUsers)
	mux.HandleFunc("/api/inbounds/users/remove", s.handleRemoveInboundUsers)
	mux.HandleFunc("/api/inbounds/users", s.handleGetInboundUsers)
	mux.HandleFunc("/api/inbounds/users/count", s.handleGetInboundUsersCount)

	// Outbounds
	mux.HandleFunc("/api/outbounds/add", s.handleAddOutbounds)
	mux.HandleFunc("/api/outbounds/edit", s.handleEditOutbounds)
	mux.HandleFunc("/api/outbounds/remove", s.handleRemoveOutbounds)
	mux.HandleFunc("/api/outbounds/list", s.handleListOutbounds)

	// Router / Rules / Balancer
	mux.HandleFunc("/api/rules/add", s.handleAddRules)
	mux.HandleFunc("/api/rules/edit", s.handleEditRules)
	mux.HandleFunc("/api/rules/replace", s.handleReplaceRules)
	mux.HandleFunc("/api/rules/remove", s.handleRemoveRules)
	mux.HandleFunc("/api/rules/list", s.handleListRules)
	mux.HandleFunc("/api/balancer/info", s.handleBalancerInfo)
	mux.HandleFunc("/api/balancer/override", s.handleBalancerOverride)
	mux.HandleFunc("/api/sourceip/block", s.handleSourceIpBlock)

	// Config file utilities
	mux.HandleFunc("/api/config/import", s.handleConfigExport)

	// Interactive API docs (embedded HTML; canonical URL /docs/)
	mountDocs(mux)

	handler := withRecover(s.withAuth(mux))
	s.httpServer = &http.Server{Addr: s.listen, Handler: handler}
	return s, nil
}

// ListenAndServe blocks until the HTTP server stops.
func (s *Server) ListenAndServe() error {
	return s.httpServer.ListenAndServe()
}

// Close shuts down the HTTP server.
func (s *Server) Close() error {
	if s.httpServer != nil {
		return s.httpServer.Close()
	}
	return nil
}

type Server struct {
	instance     *core.Instance
	ihm          inbound.Manager
	ohm          outbound.Manager
	router       routing.Router
	routingSvc   routercmd.RoutingServiceServer
	statsManager feature_stats.Manager
	httpServer   *http.Server
	startTime    time.Time
	mu           sync.Mutex
	configPath   string
	authUser     string
	authPass     string
	listen       string
}

func (s *Server) withAuth(next http.Handler) http.Handler {
	// Basic auth is optional: enabled only when both username and password are set.
	if s.authUser == "" || s.authPass == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isDocsPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != s.authUser || pass != s.authPass {
			w.Header().Set("WWW-Authenticate", "Basic realm='Xray HTTP API'")
			writeJSON(w, http.StatusUnauthorized, APIErrorResponse{
				Error: "unauthorized",
				Code:  "unauthorized",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func getInbound(handler inbound.Handler) (proxy.Inbound, error) {
	gi, ok := handler.(proxy.GetInbound)
	if !ok {
		return nil, errors.New("can't get inbound proxy from handler")
	}
	return gi.GetInbound(), nil
}

// getInboundProtocol returns the protocol name for parsing settings (e.g. "vless", "vmess").
func getInboundProtocol(handler inbound.Handler) (string, error) {
	p, err := getInbound(handler)
	if err != nil {
		return "", err
	}
	switch p.(type) {
	case *vlessin.Handler:
		return "vless", nil
	case *vmessin.Handler:
		return "vmess", nil
	case *trojan.Server:
		return "trojan", nil
	case *shadowsocks.Server, *shadowsocks_2022.MultiUserInbound:
		return "shadowsocks", nil
	default:
		return "", errors.New("unknown inbound type for add users")
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) finishMutation(w http.ResponseWriter, body interface{}, save func() error) {
	if save != nil && s.configPath != "" {
		if err := save(); err != nil {
			writeMutationSaveError(w, err)
			return
		}
	}
	switch v := body.(type) {
	case map[string]interface{}:
		writeJSON(w, http.StatusOK, v)
	case map[string]string:
		writeJSON(w, http.StatusOK, v)
	case map[string]int:
		writeJSON(w, http.StatusOK, v)
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func (s *Server) runtimeInboundTags(ctx context.Context) map[string]struct{} {
	tags := make(map[string]struct{})
	for _, h := range s.ihm.ListHandlers(ctx) {
		if tag := h.Tag(); tag != "" {
			tags[tag] = struct{}{}
		}
	}
	return tags
}

func (s *Server) runtimeOutboundTags(ctx context.Context) map[string]struct{} {
	tags := make(map[string]struct{})
	for _, h := range s.ohm.ListHandlers(ctx) {
		if _, ok := h.(*commander.Outbound); ok {
			continue
		}
		if tag := h.Tag(); tag != "" {
			tags[tag] = struct{}{}
		}
	}
	return tags
}

func allowMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		writeJSON(w, http.StatusMethodNotAllowed, APIErrorResponse{
			Error:  "method not allowed",
			Code:   "method_not_allowed",
			Details: []string{"expected " + method + ", got " + r.Method},
		})
		return false
	}
	return true
}

// --- Logger ---
func (s *Server) handleLoggerRestart(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	logger := s.instance.GetFeature((*log.Instance)(nil))
	if logger == nil {
		writeAPIErrorMsg(w, http.StatusInternalServerError, "unable to get logger instance")
		return
	}
	if err := logger.Close(); err != nil {
		writeAPIError(w, err)
		return
	}
	if err := logger.Start(); err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- Stats ---
func (s *Server) handleGetStats(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	name := r.URL.Query().Get("name")
	reset := r.URL.Query().Get("reset") == "true" || r.URL.Query().Get("reset") == "1"
	c := s.statsManager.GetCounter(name)
	if c == nil {
		if looksLikeRegexPattern(name) {
		writeAPIErrorMsg(w, http.StatusBadRequest, "pattern looks like a regex; use GET /api/stats/query?pattern="+name)
			return
		}
		writeAPIErrorMsg(w, http.StatusNotFound, name+" not found")
		return
	}
	var value int64
	if reset {
		value = c.Set(0)
	} else {
		value = c.Value()
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"stat": map[string]interface{}{"name": name, "value": value}})
}

func (s *Server) handleQueryStats(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	pattern := r.URL.Query().Get("pattern")
	reset := r.URL.Query().Get("reset") == "true" || r.URL.Query().Get("reset") == "1"
	grouped := r.URL.Query().Get("grouped") == "true" || r.URL.Query().Get("grouped") == "1"
	onlineOnly := parseQueryBool(r.URL.Query().Get("online_only")) || parseQueryBool(r.URL.Query().Get("onlineOnly"))
	var onlineSet map[string]bool
	if onlineOnly {
		onlineSet = buildOnlineEmailSet(s.statsManager)
	}
	if grouped {
		include, err := resolveGroupFilter(pattern, r.URL.Query().Get("group"))
		if err != nil {
			writeValidationError(w, err)
			return
		}
		statsGrouped, err := collectGroupedStats(s.statsManager, pattern, reset)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		if onlineOnly {
			statsGrouped.filterToOnlineUsers(onlineSet)
			include = map[string]bool{"user": true}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"stats": statsGrouped.toFilteredMap(include)})
		return
	}
	manager, ok := s.statsManager.(*stats.Manager)
	if !ok {
		writeAPIErrorMsg(w, http.StatusInternalServerError, "QueryStats only works with stats.Manager")
		return
	}
	var statList []map[string]interface{}
	manager.VisitCounters(func(name string, c feature_stats.Counter) bool {
		if !matchStatPattern(name, pattern) {
			return true
		}
		if onlineOnly && !includeUserStatWhenOnlineOnly(name, onlineSet) {
			return true
		}
		var value int64
		if reset {
			value = c.Set(0)
		} else {
			value = c.Value()
		}
		statList = append(statList, map[string]interface{}{"name": name, "value": value})
		return true
	})
	writeJSON(w, http.StatusOK, map[string]interface{}{"stat": statList})
}

// onlineUserNameToEmail extracts email from stats counter name "user>>>email>>>online".
func onlineUserNameToEmail(name string) string {
	const prefix = "user>>>"
	const suffix = ">>>online"
	if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, suffix) && len(name) > len(prefix)+len(suffix) {
		return name[len(prefix) : len(name)-len(suffix)]
	}
	return name
}

func (s *Server) handleSysStats(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	var rtm runtime.MemStats
	runtime.ReadMemStats(&rtm)
	uptime := time.Since(s.startTime)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"uptime":        uint32(uptime.Seconds()),
		"numGoroutine": uint32(runtime.NumGoroutine()),
		"alloc":         rtm.Alloc,
		"totalAlloc":    rtm.TotalAlloc,
		"sys":           rtm.Sys,
		"mallocs":       rtm.Mallocs,
		"frees":         rtm.Frees,
		"liveObjects":   rtm.Mallocs - rtm.Frees,
		"numGC":         rtm.NumGC,
		"pauseTotalNs":  rtm.PauseTotalNs,
	})
}

func (s *Server) handleStatsOnline(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	email := r.URL.Query().Get("email")
	name := "user>>>" + email + ">>>online"
	c := s.statsManager.GetOnlineMap(name)
	if c == nil {
		writeAPIErrorMsg(w, http.StatusNotFound, name+" not found")
		return
	}
	value := int64(c.Count())
	writeJSON(w, http.StatusOK, map[string]interface{}{"stat": map[string]interface{}{"name": name, "value": value}})
}

func (s *Server) handleStatsOnlineIpList(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	email := r.URL.Query().Get("email")
	name := "user>>>" + email + ">>>online"
	c := s.statsManager.GetOnlineMap(name)
	if c == nil {
		writeAPIErrorMsg(w, http.StatusNotFound, name+" not found")
		return
	}
	ips := make(map[string]int64)
	c.ForEach(func(ip string, lastSeen int64) bool {
		ips[ip] = lastSeen
		return true
	})
	writeJSON(w, http.StatusOK, map[string]interface{}{"name": name, "ips": ips})
}

func (s *Server) handleGetAllOnlineUsers(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	raw := s.statsManager.GetAllOnlineUsers()
	users := make([]string, 0, len(raw))
	for _, name := range raw {
		users = append(users, onlineUserNameToEmail(name))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"users": users})
}

// handleGetAllOnlineUsersWithIps returns all online users and their connected IPs (with last-seen time).
// Fetches each user's IP list in parallel. Response uses only email (no user>>>...>>>online).
// Optional query: email=... (repeatable) to return only those users.
func (s *Server) handleGetAllOnlineUsersWithIps(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	users := s.statsManager.GetAllOnlineUsers()
	// Optional filter: only these emails
	if emails := r.URL.Query()["email"]; len(emails) > 0 {
		want := make(map[string]bool)
		for _, e := range emails {
			if e != "" {
				want[e] = true
			}
		}
		if len(want) > 0 {
			filtered := users[:0]
			for _, u := range users {
				if want[onlineUserNameToEmail(u)] {
					filtered = append(filtered, u)
				}
			}
			users = filtered
		}
	}
	if len(users) == 0 {
		writeJSON(w, http.StatusOK, map[string]interface{}{"users": []interface{}{}})
		return
	}
	type userIps struct {
		User string            `json:"email"`
		Ips  map[string]int64 `json:"ips"`
	}
	ch := make(chan userIps, len(users))
	var wg sync.WaitGroup
	for _, fullName := range users {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			om := s.statsManager.GetOnlineMap(name)
			ips := make(map[string]int64)
			if om != nil {
				om.ForEach(func(ip string, lastSeen int64) bool {
					ips[ip] = lastSeen
					return true
				})
			}
			ch <- userIps{User: onlineUserNameToEmail(name), Ips: ips}
		}(fullName)
	}
	go func() {
		wg.Wait()
		close(ch)
	}()
	list := make([]userIps, 0, len(users))
	for res := range ch {
		list = append(list, res)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"users": list})
}

// handleStatsOnlineTraffic returns uplink/downlink traffic for every currently online subscription (user email).
func (s *Server) handleStatsOnlineTraffic(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	reset := parseQueryBool(r.URL.Query().Get("reset"))
	grouped, err := collectGroupedStats(s.statsManager, "user>>>", reset)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	onlineSet := buildOnlineEmailSet(s.statsManager)
	grouped.filterToOnlineUsers(onlineSet)
	writeJSON(w, http.StatusOK, onlineUserTrafficResponse(grouped))
}

// --- Inbounds ---
func (s *Server) handleAddInbounds(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		Inbounds []json.RawMessage `json:"inbounds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDecodeError(w, err)
		return
	}
	if ConfigBridge.ValidateInboundBatch != nil {
		if err := ConfigBridge.ValidateInboundBatch(body.Inbounds); err != nil {
			writeValidationError(w, err)
			return
		}
	}
	ctx := r.Context()
	for _, raw := range body.Inbounds {
		built, err := ConfigBridge.BuildInboundHandler(raw)
		if err != nil {
			writeValidationError(w, err)
			return
		}
		if built.Tag == "" {
			continue
		}
		if err := s.protoAddInbound(ctx, built); err != nil {
			writeAPIError(w, err)
			return
		}
	}
	s.finishMutation(w, map[string]string{"status": "ok"}, func() error {
		if ConfigBridge.PatchConfigInbounds == nil {
			return nil
		}
		return ConfigBridge.PatchConfigInbounds(s.configPath, body.Inbounds, nil)
	})
}

func (s *Server) handleEditInbounds(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	start := time.Now()
	var body struct {
		PreserveClients *bool            `json:"preserve_clients"`
		Inbounds        []json.RawMessage `json:"inbounds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDecodeError(w, err)
		return
	}
	if len(body.Inbounds) == 0 {
		writeValidationError(w, errors.New("inbounds array is required"))
		return
	}
	// Default true: format updates must not wipe clients. Set preserve_clients=false for full replace.
	preserve := parseOptionalBool(body.PreserveClients, true)
	ctx := r.Context()

	if preserve {
		if ConfigBridge.ValidateInboundBatchMeta != nil {
			if err := ConfigBridge.ValidateInboundBatchMeta(body.Inbounds); err != nil {
				writeValidationError(w, err)
				return
			}
		}
		type editedInbound struct {
			Tag          string `json:"tag"`
			ClientsCount int    `json:"clients_count"`
		}
		results := make([]editedInbound, 0, len(body.Inbounds))
		for _, raw := range body.Inbounds {
			var tagProbe struct {
				Tag string `json:"tag"`
			}
			_ = json.Unmarshal(raw, &tagProbe)
			if tagProbe.Tag == "" {
				writeValidationError(w, errors.New("inbound tag is required"))
				return
			}
			var buildRaw json.RawMessage
			diskCount := 0
			if s.configPath != "" && ConfigBridge.MergeInboundPreserveClients != nil {
				merged, n, err := ConfigBridge.MergeInboundPreserveClients(s.configPath, raw)
				if err != nil {
					writeValidationError(w, err)
					return
				}
				buildRaw, diskCount = merged, n
			} else {
				// No config file: strip clients from request and rely on runtime inject.
				var m map[string]interface{}
				if err := json.Unmarshal(raw, &m); err != nil {
					writeDecodeError(w, err)
					return
				}
				if settings, ok := m["settings"].(map[string]interface{}); ok {
					settings["clients"] = []interface{}{}
					delete(settings, "users")
					m["settings"] = settings
				}
				b, err := json.Marshal(m)
				if err != nil {
					writeAPIError(w, err)
					return
				}
				buildRaw = b
			}
			built, err := ConfigBridge.BuildInboundHandler(buildRaw)
			if err != nil {
				writeValidationError(w, err)
				return
			}
			count, err := s.protoReplaceInboundPreserveUsers(ctx, built)
			if err != nil {
				writeAPIError(w, err)
				return
			}
			if count == 0 && diskCount > 0 {
				count = diskCount
			}
			results = append(results, editedInbound{Tag: tagProbe.Tag, ClientsCount: count})
			logMutation(ctx, "inbounds/edit", tagProbe.Tag, count, &preserve, time.Since(start), 1, 0)
		}
		s.finishMutation(w, map[string]interface{}{
			"status":   "ok",
			"inbounds": results,
		}, func() error {
			if ConfigBridge.PatchConfigInboundsPreserveClients == nil {
				return nil
			}
			_, err := ConfigBridge.PatchConfigInboundsPreserveClients(s.configPath, body.Inbounds)
			return err
		})
		return
	}

	if ConfigBridge.ValidateInboundBatch != nil {
		if err := ConfigBridge.ValidateInboundBatch(body.Inbounds); err != nil {
			writeValidationError(w, err)
			return
		}
	}
	for _, raw := range body.Inbounds {
		built, err := ConfigBridge.BuildInboundHandler(raw)
		if err != nil {
			writeValidationError(w, err)
			return
		}
		if err := s.protoReplaceInbound(ctx, built); err != nil {
			writeAPIError(w, err)
			return
		}
		logMutation(ctx, "inbounds/edit", built.Tag, 0, &preserve, time.Since(start), 1, 0)
	}
	s.finishMutation(w, map[string]string{"status": "ok"}, func() error {
		if ConfigBridge.PatchConfigInbounds == nil {
			return nil
		}
		return ConfigBridge.PatchConfigInbounds(s.configPath, body.Inbounds, nil)
	})
}

func (s *Server) handleRemoveInbounds(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDecodeError(w, err)
		return
	}
	if ConfigBridge.ValidateNonEmptyTags != nil {
		if err := ConfigBridge.ValidateNonEmptyTags("tags", body.Tags); err != nil {
			writeValidationError(w, err)
			return
		}
	}
	ctx := r.Context()
	for _, tag := range body.Tags {
		_ = s.protoRemoveInbound(ctx, tag)
	}
	s.finishMutation(w, map[string]string{"status": "ok"}, func() error {
		if ConfigBridge.PatchConfigInbounds == nil {
			return nil
		}
		return ConfigBridge.PatchConfigInbounds(s.configPath, nil, body.Tags)
	})
}

func (s *Server) handleListInbounds(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	ctx := r.Context()
	tagsOnly := r.URL.Query().Get("tags_only") == "1" || r.URL.Query().Get("isOnlyTags") == "true"
	if tagsOnly {
		var inbounds []interface{}
		for _, h := range s.ihm.ListHandlers(ctx) {
			inbounds = append(inbounds, map[string]string{"tag": h.Tag()})
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"inbounds": inbounds})
		return
	}
	if s.configPath == "" || ConfigBridge.ListConfigInbounds == nil {
		writeAPIErrorMsg(w, http.StatusInternalServerError, "config path not set or list not registered")
		return
	}
	inbounds, err := ConfigBridge.ListConfigInbounds(s.configPath, s.runtimeInboundTags(ctx))
	if err != nil {
		writeAPIError(w, err)
		return
	}
	s.overlayInboundClients(ctx, inbounds)
	writeJSON(w, http.StatusOK, map[string]interface{}{"inbounds": inbounds})
}

// handleAddInboundUsers accepts only tag and settings (with clients); protocol is inferred from the existing inbound.
func (s *Server) handleAddInboundUsers(w http.ResponseWriter, r *http.Request) {
	s.handleInboundUsersMutation(w, r, "add")
}

func (s *Server) handleEditInboundUsers(w http.ResponseWriter, r *http.Request) {
	s.handleInboundUsersMutation(w, r, "edit")
}

func (s *Server) handleUpsertInboundUsers(w http.ResponseWriter, r *http.Request) {
	s.handleInboundUsersMutation(w, r, "upsert")
}

func (s *Server) handleInboundUsersMutation(w http.ResponseWriter, r *http.Request, mode string) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	start := time.Now()
	var body struct {
		Atomic   bool `json:"atomic"`
		Inbounds []struct {
			Tag      string           `json:"tag"`
			Settings *json.RawMessage `json:"settings"`
		} `json:"inbounds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDecodeError(w, err)
		return
	}
	if len(body.Inbounds) == 0 {
		writeValidationError(w, errors.New("inbounds array is required"))
		return
	}
	totalClients, err := countRequestClients(body.Inbounds)
	if err != nil {
		writeValidationError(w, err)
		return
	}
	if totalClients > maxBulkUsersPerRequest {
		writeBulkTooLarge(w, totalClients)
		return
	}
	ctx := r.Context()
	type userPatch struct {
		tag      string
		protocol string
		settings *json.RawMessage
	}
	pending := make([]userPatch, 0, len(body.Inbounds))
	for _, inb := range body.Inbounds {
		if inb.Tag == "" {
			writeValidationError(w, errors.New("inbound tag is required"))
			return
		}
		if inb.Settings == nil {
			writeValidationError(w, errors.New("settings is required for inbound ", inb.Tag))
			return
		}
		handler, err := s.ihm.GetHandler(ctx, inb.Tag)
		if err != nil {
			writeAPIErrorStatus(w, http.StatusNotFound, errors.New("inbound "+inb.Tag+": "+err.Error()))
			return
		}
		protocol, err := getInboundProtocol(handler)
		if err != nil {
			writeValidationError(w, errors.New("inbound "+inb.Tag+": "+err.Error()))
			return
		}
		if ConfigBridge.ValidateInboundUserSettings != nil {
			if err := ConfigBridge.ValidateInboundUserSettings(protocol, inb.Settings); err != nil {
				writeValidationError(w, err)
				return
			}
		}
		pending = append(pending, userPatch{tag: inb.Tag, protocol: protocol, settings: inb.Settings})
	}

	agg := bulkUserResult{}
	var filePatches []InboundUserFilePatch
	for _, inb := range pending {
		part := s.applyUserBatch(ctx, inb.tag, inb.protocol, inb.settings, mode, body.Atomic)
		agg.Succeeded += part.Succeeded
		agg.Failed += part.Failed
		agg.Errors = append(agg.Errors, part.Errors...)
		if part.Succeeded > 0 {
			filePatches = append(filePatches, InboundUserFilePatch{Tag: inb.tag, Settings: inb.settings})
		}
		if body.Atomic && part.Failed > 0 {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"succeeded": agg.Succeeded,
				"failed":    agg.Failed,
				"errors":    agg.Errors,
				"atomic":    true,
			})
			logMutation(ctx, "inbounds/users/"+mode, inb.tag, totalClients, nil, time.Since(start), agg.Succeeded, agg.Failed)
			return
		}
		logMutation(ctx, "inbounds/users/"+mode, inb.tag, part.Succeeded+part.Failed, nil, time.Since(start), part.Succeeded, part.Failed)
	}
	switch mode {
	case "add", "upsert":
		agg.AddedUsers = agg.Succeeded
	case "edit":
		agg.UpdatedUsers = agg.Succeeded
	}
	s.finishMutation(w, map[string]interface{}{
		"succeeded":      agg.Succeeded,
		"failed":         agg.Failed,
		"errors":         agg.Errors,
		"added_users":    agg.AddedUsers,
		"updated_users":  agg.UpdatedUsers,
	}, func() error {
		if ConfigBridge.PatchConfigInboundUsers == nil || len(filePatches) == 0 {
			return nil
		}
		return ConfigBridge.PatchConfigInboundUsers(s.configPath, filePatches)
	})
}

func extractInboundUsers(inb *core.InboundHandlerConfig) []*protocol.User {
	if inb == nil {
		return nil
	}
	inst, err := inb.ProxySettings.GetInstance()
	if err != nil || inst == nil {
		return nil
	}
	switch ty := inst.(type) {
	case *vmessin.Config:
		return ty.User
	case *vlessin.Config:
		return ty.Users
	case *trojan.ServerConfig:
		return ty.Users
	case *shadowsocks.ServerConfig:
		return ty.Users
	case *shadowsocks_2022.MultiUserServerConfig:
		return ty.Users
	default:
		return nil
	}
}

func (s *Server) handleRemoveInboundUsers(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	start := time.Now()
	var body struct {
		Atomic bool     `json:"atomic"`
		Tag    string   `json:"tag"`
		Emails []string `json:"emails"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDecodeError(w, err)
		return
	}
	if body.Tag == "" || len(body.Emails) == 0 {
		writeValidationError(w, errors.New("tag and emails are required"))
		return
	}
	if len(body.Emails) > maxBulkUsersPerRequest {
		writeBulkTooLarge(w, len(body.Emails))
		return
	}
	if ConfigBridge.ValidateNonEmptyEmails != nil {
		if err := ConfigBridge.ValidateNonEmptyEmails(body.Emails); err != nil {
			writeValidationError(w, err)
			return
		}
	}
	ctx := r.Context()
	if _, err := s.ihm.GetHandler(ctx, body.Tag); err != nil {
		writeAPIErrorStatus(w, http.StatusNotFound, err)
		return
	}
	res := s.removeUsersPartial(ctx, body.Tag, body.Emails, body.Atomic)
	logMutation(ctx, "inbounds/users/remove", body.Tag, len(body.Emails), nil, time.Since(start), res.Succeeded, res.Failed)
	if body.Atomic && res.Failed > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"succeeded":           res.Succeeded,
			"failed":              res.Failed,
			"errors":              res.Errors,
			"removed_users":       res.RemovedUsers,
			"dropped_connections": res.DroppedConns,
			"atomic":              true,
		})
		return
	}
	succeededEmails := make([]string, 0, res.Succeeded)
	failedSet := make(map[string]struct{}, len(res.Errors))
	for _, e := range res.Errors {
		if e.Email != "" {
			failedSet[e.Email] = struct{}{}
		}
	}
	for _, email := range body.Emails {
		if _, bad := failedSet[email]; !bad {
			succeededEmails = append(succeededEmails, email)
		}
	}
	s.finishMutation(w, map[string]interface{}{
		"succeeded":           res.Succeeded,
		"failed":              res.Failed,
		"errors":              res.Errors,
		"removed_users":       res.RemovedUsers,
		"dropped_connections": res.DroppedConns,
	}, func() error {
		if ConfigBridge.PatchConfigInboundUsersRemove == nil || len(succeededEmails) == 0 {
			return nil
		}
		return ConfigBridge.PatchConfigInboundUsersRemove(s.configPath, body.Tag, succeededEmails)
	})
}

func (s *Server) handleGetInboundUsers(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	tag := r.URL.Query().Get("tag")
	if tag == "" {
		writeAPIErrorMsg(w, http.StatusBadRequest, "tag required")
		return
	}
	email := r.URL.Query().Get("email")
	ctx := r.Context()
	handler, err := s.ihm.GetHandler(ctx, tag)
	if err != nil {
		writeAPIErrorStatus(w, http.StatusNotFound, err)
		return
	}
	p, err := getInbound(handler)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	um, ok := p.(proxy.UserManager)
	if !ok {
		writeAPIErrorMsg(w, http.StatusBadRequest, "proxy is not a UserManager")
		return
	}
	users, err := s.configClientsFromManager(ctx, handler, um, email)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"users": users})
}

func (s *Server) configClientsFromManager(ctx context.Context, handler inbound.Handler, um proxy.UserManager, email string) ([]interface{}, error) {
	protocolName, err := getInboundProtocol(handler)
	if err != nil {
		return nil, err
	}
	var mem []*protocol.MemoryUser
	if email != "" {
		if u := um.GetUser(ctx, email); u != nil {
			mem = []*protocol.MemoryUser{u}
		}
	} else {
		mem = um.GetUsers(ctx)
	}
	if ConfigBridge.ConfigClientsFromMemoryUsers == nil {
		return []interface{}{}, nil
	}
	return ConfigBridge.ConfigClientsFromMemoryUsers(protocolName, mem)
}

func (s *Server) overlayInboundClients(ctx context.Context, inbounds []interface{}) {
	if ConfigBridge.OverlayInboundClients == nil {
		return
	}
	for _, item := range inbounds {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		tag, _ := m["tag"].(string)
		if tag == "" {
			continue
		}
		handler, err := s.ihm.GetHandler(ctx, tag)
		if err != nil {
			continue
		}
		protocolName, err := getInboundProtocol(handler)
		if err != nil {
			continue
		}
		p, err := getInbound(handler)
		if err != nil {
			continue
		}
		um, ok := p.(proxy.UserManager)
		if !ok {
			continue
		}
		_ = ConfigBridge.OverlayInboundClients(m, protocolName, um.GetUsers(ctx))
	}
}

func (s *Server) handleGetInboundUsersCount(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	tag := r.URL.Query().Get("tag")
	ctx := r.Context()
	handler, err := s.ihm.GetHandler(ctx, tag)
	if err != nil {
		writeAPIErrorStatus(w, http.StatusNotFound, err)
		return
	}
	p, err := getInbound(handler)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	um, ok := p.(proxy.UserManager)
	if !ok {
		writeAPIErrorMsg(w, http.StatusBadRequest, "proxy is not a UserManager")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"count": um.GetUsersCount(ctx)})
}

// --- Outbounds ---
func (s *Server) handleAddOutbounds(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		Outbounds []json.RawMessage `json:"outbounds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDecodeError(w, err)
		return
	}
	if ConfigBridge.ValidateOutboundBatch != nil {
		if err := ConfigBridge.ValidateOutboundBatch(body.Outbounds); err != nil {
			writeValidationError(w, err)
			return
		}
	}
	ctx := r.Context()
	for _, raw := range body.Outbounds {
		built, err := ConfigBridge.BuildOutboundHandler(raw)
		if err != nil {
			writeValidationError(w, err)
			return
		}
		if built.Tag == "" {
			continue
		}
		if err := s.protoAddOutbound(ctx, built); err != nil {
			writeAPIError(w, err)
			return
		}
	}
	s.finishMutation(w, map[string]string{"status": "ok"}, func() error {
		if ConfigBridge.PatchConfigOutbounds == nil {
			return nil
		}
		return ConfigBridge.PatchConfigOutbounds(s.configPath, body.Outbounds, nil)
	})
}

func (s *Server) handleEditOutbounds(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		Outbounds []json.RawMessage `json:"outbounds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDecodeError(w, err)
		return
	}
	if len(body.Outbounds) == 0 {
		writeValidationError(w, errors.New("outbounds array is required"))
		return
	}
	if ConfigBridge.ValidateOutboundBatch != nil {
		if err := ConfigBridge.ValidateOutboundBatch(body.Outbounds); err != nil {
			writeValidationError(w, err)
			return
		}
	}
	ctx := r.Context()
	for _, raw := range body.Outbounds {
		built, err := ConfigBridge.BuildOutboundHandler(raw)
		if err != nil {
			writeValidationError(w, err)
			return
		}
		if err := s.protoReplaceOutbound(ctx, built); err != nil {
			writeAPIError(w, err)
			return
		}
	}
	s.finishMutation(w, map[string]string{"status": "ok"}, func() error {
		if ConfigBridge.PatchConfigOutbounds == nil {
			return nil
		}
		return ConfigBridge.PatchConfigOutbounds(s.configPath, body.Outbounds, nil)
	})
}

func (s *Server) handleRemoveOutbounds(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDecodeError(w, err)
		return
	}
	if ConfigBridge.ValidateNonEmptyTags != nil {
		if err := ConfigBridge.ValidateNonEmptyTags("tags", body.Tags); err != nil {
			writeValidationError(w, err)
			return
		}
	}
	ctx := r.Context()
	for _, tag := range body.Tags {
		_ = s.protoRemoveOutbound(ctx, tag)
	}
	s.finishMutation(w, map[string]string{"status": "ok"}, func() error {
		if ConfigBridge.PatchConfigOutbounds == nil {
			return nil
		}
		return ConfigBridge.PatchConfigOutbounds(s.configPath, nil, body.Tags)
	})
}

func (s *Server) handleListOutbounds(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	ctx := r.Context()
	tagsOnly := r.URL.Query().Get("tags_only") == "1" || r.URL.Query().Get("isOnlyTags") == "true"
	if tagsOnly {
		var outbounds []interface{}
		for _, h := range s.ohm.ListHandlers(ctx) {
			if _, ok := h.(*commander.Outbound); ok {
				continue
			}
			outbounds = append(outbounds, map[string]string{"tag": h.Tag()})
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"outbounds": outbounds})
		return
	}
	if s.configPath == "" || ConfigBridge.ListConfigOutbounds == nil {
		writeAPIErrorMsg(w, http.StatusInternalServerError, "config path not set or list not registered")
		return
	}
	outbounds, err := ConfigBridge.ListConfigOutbounds(s.configPath, s.runtimeOutboundTags(ctx))
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"outbounds": outbounds})
}

// --- Rules / Balancer / SourceIP ---
func (s *Server) handleAddRules(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		Routing json.RawMessage `json:"routing"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDecodeError(w, err)
		return
	}
	if len(body.Routing) == 0 {
		writeValidationError(w, errors.New("routing is required"))
		return
	}
	if ConfigBridge.ValidateRoutingRules != nil {
		if err := ConfigBridge.ValidateRoutingRules(body.Routing); err != nil {
			writeValidationError(w, err)
			return
		}
	}
	shouldAppend := r.URL.Query().Get("should_append") == "true" || r.URL.Query().Get("should_append") == "1"
	config, err := ConfigBridge.BuildRouterRules(body.Routing)
	if err != nil {
		writeValidationError(w, err)
		return
	}
	tmsg := cserial.ToTypedMessage(config)
	if tmsg == nil {
		writeAPIErrorMsg(w, http.StatusInternalServerError, "failed to build TypedMessage")
		return
	}
	if err := s.protoAddRule(r.Context(), tmsg, shouldAppend); err != nil {
		writeAPIError(w, err)
		return
	}
	s.finishMutation(w, map[string]string{"status": "ok"}, func() error {
		if ConfigBridge.PatchConfigRulesAdd == nil {
			return nil
		}
		return ConfigBridge.PatchConfigRulesAdd(s.configPath, body.Routing, !shouldAppend)
	})
}

func (s *Server) handleEditRules(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		RuleTag  string          `json:"rule_tag"`
		RuleTags []string        `json:"ruleTags"`
		Routing  json.RawMessage `json:"routing"`
		Rule     json.RawMessage `json:"rule"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDecodeError(w, err)
		return
	}
	ruleTag := strings.TrimSpace(body.RuleTag)
	if ruleTag == "" && len(body.RuleTags) > 0 {
		ruleTag = strings.TrimSpace(body.RuleTags[0])
	}
	if ruleTag == "" {
		writeValidationError(w, errors.New("rule_tag is required"))
		return
	}
	ruleInput := body.Routing
	if len(ruleInput) == 0 {
		ruleInput = body.Rule
	}
	if len(ruleInput) == 0 {
		writeValidationError(w, errors.New("routing or rule is required"))
		return
	}
	if ConfigBridge.ValidateRoutingRules != nil {
		wrapped, err := wrapSingleRoutingRule(ruleInput)
		if err != nil {
			writeValidationError(w, err)
			return
		}
		if err := ConfigBridge.ValidateRoutingRules(wrapped); err != nil {
			writeValidationError(w, err)
			return
		}
		ruleInput = wrapped
	}
	if err := s.protoRemoveRule(r.Context(), ruleTag); err != nil {
		writeAPIError(w, err)
		return
	}
	config, err := ConfigBridge.BuildRouterRules(ruleInput)
	if err != nil {
		writeValidationError(w, err)
		return
	}
	tmsg := cserial.ToTypedMessage(config)
	if tmsg == nil {
		writeAPIErrorMsg(w, http.StatusInternalServerError, "failed to build TypedMessage")
		return
	}
	if err := s.protoAddRule(r.Context(), tmsg, true); err != nil {
		writeAPIError(w, err)
		return
	}
	s.finishMutation(w, map[string]string{"status": "ok"}, func() error {
		if ConfigBridge.PatchConfigRulesEdit == nil {
			return nil
		}
		return ConfigBridge.PatchConfigRulesEdit(s.configPath, ruleTag, ruleInput)
	})
}

func (s *Server) handleReplaceRules(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	start := time.Now()
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		writeDecodeError(w, err)
		return
	}
	routing, rulesCount, err := normalizeReplaceRulesBody(rawBody)
	if err != nil {
		writeValidationError(w, err)
		return
	}
	if ConfigBridge.ValidateRoutingRules != nil {
		if err := ConfigBridge.ValidateRoutingRules(routing); err != nil {
			writeValidationError(w, err)
			return
		}
	}
	config, err := ConfigBridge.BuildRouterRules(routing)
	if err != nil {
		writeValidationError(w, err)
		return
	}
	tmsg := cserial.ToTypedMessage(config)
	if tmsg == nil {
		writeAPIErrorMsg(w, http.StatusInternalServerError, "failed to build TypedMessage")
		return
	}
	// shouldAppend=false → ReloadRules replaces the entire rule set atomically.
	if err := s.protoAddRule(r.Context(), tmsg, false); err != nil {
		writeAPIError(w, err)
		return
	}
	logMutation(r.Context(), "rules/replace", "", rulesCount, nil, time.Since(start), rulesCount, 0)
	s.finishMutation(w, map[string]interface{}{
		"status": "ok",
		"count":  rulesCount,
	}, func() error {
		if ConfigBridge.PatchConfigRulesReplace == nil {
			return nil
		}
		return ConfigBridge.PatchConfigRulesReplace(s.configPath, routing)
	})
}

func normalizeReplaceRulesBody(raw []byte) (json.RawMessage, int, error) {
	if len(raw) == 0 {
		return nil, 0, errors.New("request body is required")
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, 0, errors.New("invalid json").Base(err)
	}
	var routing json.RawMessage
	if v, ok := probe["routing"]; ok && len(v) > 0 {
		routing = v
	} else if _, ok := probe["rules"]; ok {
		routing = raw
	} else {
		return nil, 0, errors.New("rules or routing.rules is required")
	}
	var rc struct {
		Rules []json.RawMessage `json:"rules"`
	}
	if err := json.Unmarshal(routing, &rc); err != nil {
		return nil, 0, errors.New("invalid routing json").Base(err)
	}
	if len(rc.Rules) == 0 {
		return nil, 0, errors.New("routing.rules is required")
	}
	return routing, len(rc.Rules), nil
}

func wrapSingleRoutingRule(rule json.RawMessage) (json.RawMessage, error) {
	if len(rule) == 0 {
		return nil, errors.New("rule is required")
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(rule, &probe); err != nil {
		return nil, errors.New("invalid rule json").Base(err)
	}
	if raw, ok := probe["rules"]; ok {
		return json.RawMessage(`{"rules":` + string(raw) + `}`), nil
	}
	if raw, ok := probe["routing"]; ok {
		return raw, nil
	}
	return json.Marshal(map[string]interface{}{"rules": []json.RawMessage{rule}})
}

func mergeRuleTags(ruleTags, ruleTags2, tags []string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, tag := range append(append(ruleTags, ruleTags2...), tags...) {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out
}

func sortRuleIndicesDesc(indices []int) []int {
	if len(indices) == 0 {
		return nil
	}
	out := append([]int(nil), indices...)
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] > out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func (s *Server) handleRemoveRules(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		RuleTags  []string `json:"rule_tags"`
		RuleTags2 []string `json:"ruleTags"`
		Tags      []string `json:"tags"`
		Indices   []int    `json:"indices"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDecodeError(w, err)
		return
	}
	tags := mergeRuleTags(body.RuleTags, body.RuleTags2, body.Tags)
	if len(tags) == 0 && len(body.Indices) == 0 {
		writeValidationError(w, errors.New("rule_tags or indices required"))
		return
	}
	removed := 0
	var warnings []string
	for _, tag := range tags {
		if err := s.protoRemoveRule(r.Context(), tag); err != nil {
			warnings = append(warnings, "rule_tag "+tag+": "+err.Error())
		} else {
			removed++
		}
	}
	if len(body.Indices) > 0 {
		ri, ok := s.router.(interface {
			RemoveRuleAt(index int) error
		})
		if !ok {
			writeAPIErrorMsg(w, http.StatusInternalServerError, "router does not support index removal")
			return
		}
		for _, idx := range sortRuleIndicesDesc(body.Indices) {
			if err := ri.RemoveRuleAt(idx); err != nil {
				warnings = append(warnings, "index "+strconv.Itoa(idx)+": "+err.Error())
			} else {
				removed++
			}
		}
	}
	resp := map[string]interface{}{"removed": removed}
	if len(warnings) > 0 {
		resp["warnings"] = warnings
	}
	s.finishMutation(w, resp, func() error {
		if ConfigBridge.PatchConfigRulesRemove == nil {
			return nil
		}
		return ConfigBridge.PatchConfigRulesRemove(s.configPath, tags, body.Indices)
	})
}

func (s *Server) handleListRules(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	if s.configPath != "" && ConfigBridge.ListConfigRules != nil {
		rules, err := ConfigBridge.ListConfigRules(s.configPath)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		if rules != nil {
			writeJSON(w, http.StatusOK, map[string]interface{}{"rules": rules})
			return
		}
	}
	rules := s.router.ListRule()
	var list []map[string]string
	for _, v := range rules {
		list = append(list, map[string]string{"tag": v.GetOutboundTag(), "ruleTag": v.GetRuleTag()})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"rules": list})
}

func (s *Server) handleBalancerInfo(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	tag := r.URL.Query().Get("tag")
	balancer := make(map[string]interface{})
	if bo, ok := s.router.(routing.BalancerOverrider); ok {
		res, err := bo.GetOverrideTarget(tag)
		if err == nil {
			balancer["override"] = map[string]string{"target": res}
		}
	}
	if pt, ok := s.router.(routing.BalancerPrincipleTarget); ok {
		res, err := pt.GetPrincipleTarget(tag)
		if err == nil {
			balancer["principle_target"] = map[string]interface{}{"tag": res}
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"balancer": balancer})
}

// handleConfigExport writes a configuration file to disk.
// Usage (multipart/form-data):
//   file: uploaded config.json file (required)
//   path: optional target path; if empty, uses server.configPath
func (s *Server) handleConfigExport(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeAPIError(w, err)
		return
	}
	path := strings.TrimSpace(r.FormValue("path"))
	file, _, err := r.FormFile("file")
	if err != nil {
		writeAPIError(w, err)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	if path == "" {
		path = strings.TrimSpace(s.configPath)
	}
	if path == "" {
		writeAPIErrorMsg(w, http.StatusBadRequest, "no config path specified and default path is unknown")
		return
	}
	// Full Xray config validation before writing to disk.
	if ConfigBridge.ValidateConfigBytes != nil {
		if err := ConfigBridge.ValidateConfigBytes(data); err != nil {
			writeValidationError(w, err)
			return
		}
	} else {
		var tmp interface{}
		if err := json.Unmarshal(data, &tmp); err != nil {
			writeDecodeError(w, err)
			return
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "path": path})
}

func (s *Server) handleBalancerOverride(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		BalancerTag string `json:"balancer_tag"`
		Target     string `json:"target"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDecodeError(w, err)
		return
	}
	bo, ok := s.router.(routing.BalancerOverrider)
	if !ok {
		writeAPIErrorMsg(w, http.StatusInternalServerError, "unsupported router implementation")
		return
	}
	if err := bo.SetOverrideTarget(body.BalancerTag, body.Target); err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleSourceIpBlock(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		Outbound string   `json:"outbound"`
		Inbound  string   `json:"inbound"`
		RuleTag  string   `json:"rule_tag"`
		Reset    bool     `json:"reset"`
		SourceIPs []string `json:"source_ips"`
	}
	if body.RuleTag == "" {
		body.RuleTag = "sourceIpBlock"
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDecodeError(w, err)
		return
	}
	if body.Outbound == "" || len(body.SourceIPs) == 0 {
		writeAPIErrorMsg(w, http.StatusBadRequest, "outbound and source_ips required")
		return
	}
	inboundTags := []string{}
	if body.Inbound != "" {
		inboundTags = []string{body.Inbound}
	}
	jsonIps, _ := json.Marshal(body.SourceIPs)
	jsonInbound, _ := json.Marshal(inboundTags)
	stringConfig := `{"routing":{"rules":[{"ruleTag":"` + body.RuleTag + `","inboundTag":` + string(jsonInbound) + `,"outboundTag":"` + body.Outbound + `","source":` + string(jsonIps) + `}]}}`
	config, err := ConfigBridge.BuildRouterRulesFromStr(stringConfig)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	if body.Reset {
		_ = s.protoRemoveRule(r.Context(), body.RuleTag)
	}
	tmsg := cserial.ToTypedMessage(config)
	if tmsg == nil {
		writeAPIErrorMsg(w, http.StatusInternalServerError, "failed to build TypedMessage")
		return
	}
	if err := s.protoAddRule(r.Context(), tmsg, true); err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
