package userconn

import (
	"context"
	"net"
	"sync"
)

type sessionSet struct {
	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

// Registry tracks live inbound TCP connections by inbound tag and user email.
type Registry struct {
	mu    sync.Mutex
	byKey map[string]*sessionSet
}

var global = NewRegistry()

func NewRegistry() *Registry {
	return &Registry{byKey: make(map[string]*sessionSet)}
}

func sessionKey(inboundTag, email string) string {
	return inboundTag + "\x00" + email
}

// Track registers a connection until ctx is cancelled.
func Track(ctx context.Context, inboundTag, email string, conn net.Conn) {
	global.track(ctx, inboundTag, email, conn)
}

// Kick closes all tracked connections for the given inbound tag and user email.
func Kick(inboundTag, email string) int {
	return global.kick(inboundTag, email)
}

func (r *Registry) track(ctx context.Context, inboundTag, email string, conn net.Conn) {
	if conn == nil || email == "" {
		return
	}
	k := sessionKey(inboundTag, email)
	r.mu.Lock()
	set, ok := r.byKey[k]
	if !ok {
		set = &sessionSet{conns: make(map[net.Conn]struct{})}
		r.byKey[k] = set
	}
	r.mu.Unlock()

	set.mu.Lock()
	set.conns[conn] = struct{}{}
	set.mu.Unlock()

	context.AfterFunc(ctx, func() {
		r.untrack(k, conn)
	})
}

func (r *Registry) untrack(k string, conn net.Conn) {
	r.mu.Lock()
	set := r.byKey[k]
	r.mu.Unlock()
	if set == nil {
		return
	}
	set.mu.Lock()
	delete(set.conns, conn)
	empty := len(set.conns) == 0
	set.mu.Unlock()
	if empty {
		r.mu.Lock()
		if cur := r.byKey[k]; cur == set {
			delete(r.byKey, k)
		}
		r.mu.Unlock()
	}
}

func (r *Registry) kick(inboundTag, email string) int {
	k := sessionKey(inboundTag, email)
	r.mu.Lock()
	set := r.byKey[k]
	delete(r.byKey, k)
	r.mu.Unlock()
	if set == nil {
		return 0
	}
	set.mu.Lock()
	conns := make([]net.Conn, 0, len(set.conns))
	for c := range set.conns {
		conns = append(conns, c)
	}
	set.conns = make(map[net.Conn]struct{})
	set.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
	return len(conns)
}
