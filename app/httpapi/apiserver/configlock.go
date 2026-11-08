package apiserver

import "sync"

// configWriteMu serializes read-modify-write of the startup config file.
// User, inbound, outbound, and routing mutations all merge into that one file.
// Overlapping merges used to keep only the last writer's clients.
var configWriteMu sync.Mutex

// LockConfigWrite guards a config.json mutation. Pair it with UnlockConfigWrite.
func LockConfigWrite() {
	configWriteMu.Lock()
}

// UnlockConfigWrite releases the config file mutation lock.
func UnlockConfigWrite() {
	configWriteMu.Unlock()
}
