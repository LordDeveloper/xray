package apiserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/xtls/xray-core/app/commander"
	"github.com/xtls/xray-core/app/log"
	"github.com/xtls/xray-core/app/stats"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/strmatcher"
	cserial "github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/platform"
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

	mux := http.NewServeMux()

	// Logger
	mux.HandleFunc("/api/logger/restart", s.handleLoggerRestart)

	// Stats
	mux.HandleFunc("/api/stats", s.handleGetStats)
	mux.HandleFunc("/api/stats/query", s.handleQueryStats)
	mux.HandleFunc("/api/stats/sys", s.handleSysStats)
	mux.HandleFunc("/api/stats/online", s.handleStatsOnline)
	mux.HandleFunc("/api/stats/online/iplist", s.handleStatsOnlineIpList)
	mux.HandleFunc("/api/stats/online/users", s.handleGetAllOnlineUsers)
	mux.HandleFunc("/api/stats/online/all", s.handleGetAllOnlineUsersWithIps)

	// Inbounds
	mux.HandleFunc("/api/inbounds/add", s.handleAddInbounds)
	mux.HandleFunc("/api/inbounds/remove", s.handleRemoveInbounds)
	mux.HandleFunc("/api/inbounds/list", s.handleListInbounds)
	mux.HandleFunc("/api/inbounds/users/add", s.handleAddInboundUsers)
	mux.HandleFunc("/api/inbounds/users/remove", s.handleRemoveInboundUsers)
	mux.HandleFunc("/api/inbounds/users", s.handleGetInboundUsers)
	mux.HandleFunc("/api/inbounds/users/count", s.handleGetInboundUsersCount)

	// Outbounds
	mux.HandleFunc("/api/outbounds/add", s.handleAddOutbounds)
	mux.HandleFunc("/api/outbounds/remove", s.handleRemoveOutbounds)
	mux.HandleFunc("/api/outbounds/list", s.handleListOutbounds)

	// Router / Rules / Balancer
	mux.HandleFunc("/api/rules/add", s.handleAddRules)
	mux.HandleFunc("/api/rules/remove", s.handleRemoveRules)
	mux.HandleFunc("/api/rules/list", s.handleListRules)
	mux.HandleFunc("/api/balancer/info", s.handleBalancerInfo)
	mux.HandleFunc("/api/balancer/override", s.handleBalancerOverride)
	mux.HandleFunc("/api/sourceip/block", s.handleSourceIpBlock)

	// Config file utilities
	mux.HandleFunc("/api/config/import", s.handleConfigExport)

	handler := s.withAuth(mux)
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
		user, pass, ok := r.BasicAuth()
		if !ok || user != s.authUser || pass != s.authPass {
			w.Header().Set("WWW-Authenticate", "Basic realm='Xray HTTP API'")
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
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

func allowMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		w.WriteHeader(http.StatusMethodNotAllowed)
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
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to get logger instance"})
		return
	}
	if err := logger.Close(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := logger.Start(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
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
		writeJSON(w, http.StatusNotFound, map[string]string{"error": name + " not found"})
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
	matcher, err := strmatcher.Substr.New(pattern)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	manager, ok := s.statsManager.(*stats.Manager)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "QueryStats only works with stats.Manager"})
		return
	}
	var statList []map[string]interface{}
	manager.VisitCounters(func(name string, c feature_stats.Counter) bool {
		if matcher.Match(name) {
			var value int64
			if reset {
				value = c.Set(0)
			} else {
				value = c.Value()
			}
			statList = append(statList, map[string]interface{}{"name": name, "value": value})
		}
		return true
	})
	writeJSON(w, http.StatusOK, map[string]interface{}{"stat": statList})
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
		writeJSON(w, http.StatusNotFound, map[string]string{"error": name + " not found"})
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
		writeJSON(w, http.StatusNotFound, map[string]string{"error": name + " not found"})
		return
	}
	ips := make(map[string]int64)
	for ip, t := range c.IpTimeMap() {
		ips[ip] = t.Unix()
	}
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

// onlineUserNameToEmail extracts email from stats counter name "user>>>email>>>online".
func onlineUserNameToEmail(name string) string {
	const prefix = "user>>>"
	const suffix = ">>>online"
	if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, suffix) && len(name) > len(prefix)+len(suffix) {
		return name[len(prefix) : len(name)-len(suffix)]
	}
	return name
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
				for ip, t := range om.IpTimeMap() {
					ips[ip] = t.Unix()
				}
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

// --- Inbounds ---
func (s *Server) handleAddInbounds(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		Inbounds []json.RawMessage `json:"inbounds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	for _, raw := range body.Inbounds {
		built, err := ConfigBridge.BuildInboundHandler(raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "build inbound: " + err.Error()})
			return
		}
		if built.Tag == "" {
			continue
		}
		if err := core.AddInboundHandler(s.instance, built); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleRemoveInbounds(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	ctx := r.Context()
	for _, tag := range body.Tags {
		_ = s.ihm.RemoveHandler(ctx, tag)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleListInbounds(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	ctx := r.Context()
	tagsOnly := r.URL.Query().Get("tags_only") == "1" || r.URL.Query().Get("isOnlyTags") == "true"
	handlers := s.ihm.ListHandlers(ctx)
	var inbounds []interface{}
	for _, h := range handlers {
		if tagsOnly {
			inbounds = append(inbounds, map[string]string{"tag": h.Tag()})
		} else {
			inbounds = append(inbounds, map[string]interface{}{
				"tag":               h.Tag(),
				"receiverSettings": h.ReceiverSettings(),
				"proxySettings":    h.ProxySettings(),
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"inbounds": inbounds})
}

// handleAddInboundUsers accepts only tag and settings (with clients); protocol is inferred from the existing inbound.
func (s *Server) handleAddInboundUsers(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		Inbounds []struct {
			Tag     string           `json:"tag"`
			Settings *json.RawMessage `json:"settings"`
		} `json:"inbounds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	ctx := r.Context()
	added := 0
	for _, inb := range body.Inbounds {
		if inb.Tag == "" {
			continue
		}
		handler, err := s.ihm.GetHandler(ctx, inb.Tag)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "inbound " + inb.Tag + ": " + err.Error()})
			return
		}
		protocol, err := getInboundProtocol(handler)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "inbound " + inb.Tag + ": " + err.Error()})
			return
		}
		built, err := ConfigBridge.BuildInboundProxyOnly(inb.Tag, protocol, inb.Settings)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "build settings for " + inb.Tag + ": " + err.Error()})
			return
		}
		users := extractInboundUsers(built)
		for _, user := range users {
			if user.Email == "" {
				continue
			}
			if err := s.addUser(ctx, inb.Tag, user); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			added++
		}
	}
	writeJSON(w, http.StatusOK, map[string]int{"added_users": added})
}

func (s *Server) addUser(ctx context.Context, tag string, user *protocol.User) error {
	handler, err := s.ihm.GetHandler(ctx, tag)
	if err != nil {
		return err
	}
	p, err := getInbound(handler)
	if err != nil {
		return err
	}
	um, ok := p.(proxy.UserManager)
	if !ok {
		return errors.New("proxy is not a UserManager")
	}
	mUser, err := user.ToMemoryUser()
	if err != nil {
		return errors.New("failed to parse user").Base(err)
	}
	return um.AddUser(ctx, mUser)
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
		return ty.Clients
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
	var body struct {
		Tag    string   `json:"tag"`
		Emails []string `json:"emails"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	if body.Tag == "" || len(body.Emails) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tag and emails required"})
		return
	}
	ctx := r.Context()
	handler, err := s.ihm.GetHandler(ctx, body.Tag)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	p, err := getInbound(handler)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	um, ok := p.(proxy.UserManager)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "proxy is not a UserManager"})
		return
	}
	removed := 0
	for _, email := range body.Emails {
		if err := um.RemoveUser(ctx, email); err == nil {
			removed++
		}
	}
	writeJSON(w, http.StatusOK, map[string]int{"removed_users": removed})
}

func (s *Server) handleGetInboundUsers(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	tag := r.URL.Query().Get("tag")
	email := r.URL.Query().Get("email")
	ctx := r.Context()
	handler, err := s.ihm.GetHandler(ctx, tag)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	p, err := getInbound(handler)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	um, ok := p.(proxy.UserManager)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "proxy is not a UserManager"})
		return
	}
	var users []*protocol.User
	if email != "" {
		u := um.GetUser(ctx, email)
		if u != nil {
			users = []*protocol.User{protocol.ToProtoUser(u)}
		}
	} else {
		for _, u := range um.GetUsers(ctx) {
			users = append(users, protocol.ToProtoUser(u))
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"users": users})
}

func (s *Server) handleGetInboundUsersCount(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	tag := r.URL.Query().Get("tag")
	ctx := r.Context()
	handler, err := s.ihm.GetHandler(ctx, tag)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	p, err := getInbound(handler)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	um, ok := p.(proxy.UserManager)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "proxy is not a UserManager"})
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
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	for _, raw := range body.Outbounds {
		built, err := ConfigBridge.BuildOutboundHandler(raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "build outbound: " + err.Error()})
			return
		}
		if built.Tag == "" {
			continue
		}
		if err := core.AddOutboundHandler(s.instance, built); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleRemoveOutbounds(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	ctx := r.Context()
	for _, tag := range body.Tags {
		_ = s.ohm.RemoveHandler(ctx, tag)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleListOutbounds(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	ctx := r.Context()
	handlers := s.ohm.ListHandlers(ctx)
	var outbounds []interface{}
	for _, h := range handlers {
		if _, ok := h.(*commander.Outbound); ok {
			continue
		}
		outbounds = append(outbounds, map[string]interface{}{
			"tag":             h.Tag(),
			"senderSettings":  h.SenderSettings(),
			"proxySettings":   h.ProxySettings(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"outbounds": outbounds})
}

// --- Rules / Balancer / SourceIP ---
func (s *Server) handleAddRules(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		Routing      json.RawMessage `json:"routing"`
		ShouldAppend bool            `json:"should_append"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	if len(body.Routing) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "routing required"})
		return
	}
	config, err := ConfigBridge.BuildRouterRules(body.Routing)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	tmsg := cserial.ToTypedMessage(config)
	if tmsg == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to build TypedMessage"})
		return
	}
	if err := s.router.AddRule(tmsg, body.ShouldAppend); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleRemoveRules(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		RuleTags []string `json:"rule_tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	for _, tag := range body.RuleTags {
		_ = s.router.RemoveRule(tag)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleListRules(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
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
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid multipart form: " + err.Error()})
		return
	}
	path := strings.TrimSpace(r.FormValue("path"))
	file, _, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file field is required: " + err.Error()})
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read uploaded file: " + err.Error()})
		return
	}
	if path == "" {
		path = strings.TrimSpace(s.configPath)
	}
	if path == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no config path specified and default path is unknown"})
		return
	}
	// Validate JSON syntax to catch malformed configs early.
	var tmp interface{}
	if err := json.Unmarshal(data, &tmp); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid config json: " + err.Error()})
		return
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to write config: " + err.Error()})
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
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	bo, ok := s.router.(routing.BalancerOverrider)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unsupported router implementation"})
		return
	}
	if err := bo.SetOverrideTarget(body.BalancerTag, body.Target); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
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
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	if body.Outbound == "" || len(body.SourceIPs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "outbound and source_ips required"})
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
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if body.Reset {
		_ = s.router.RemoveRule(body.RuleTag)
	}
	tmsg := cserial.ToTypedMessage(config)
	if tmsg == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to build TypedMessage"})
		return
	}
	if err := s.router.AddRule(tmsg, true); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
