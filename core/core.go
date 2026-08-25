// Package core provides an entry point to use Xray core functionalities.
//
// Xray makes it possible to accept incoming network connections with certain
// protocol, process the data, and send them through another connection with
// the same or a difference protocol on demand.
//
// It may be configured to work with multiple protocols at the same time, and
// uses the internal router to tunnel through different inbound and outbound
// connections.
package core

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/xtls/xray-core/common/serial"
)

var (
	// Default matches the latest fork release tag (vX.Y.Z). CI overrides via versionOverride.
	Version_x byte = 1
	Version_y byte = 0
	Version_z byte = 7
)

var (
	build    = "Custom"
	codename = "Xray, Penetrates Everything."
	intro    = "A unified platform for anti-censorship."
	// versionOverride is set at link time from the git release tag, e.g.:
	// -X github.com/xtls/xray-core/core.versionOverride=1.0.7
	versionOverride = ""
)

func init() {
	if versionOverride != "" {
		applyVersionOverride(versionOverride)
	}
	// Manually injected
	if build != "Custom" {
		return
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	var isDirty bool
	var foundBuild bool
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			if len(setting.Value) < 7 {
				return
			}
			build = setting.Value[:7]
			foundBuild = true
		case "vcs.modified":
			isDirty = setting.Value == "true"
		}
	}
	if isDirty && foundBuild {
		build += "-dirty"
	}
}

func applyVersionOverride(raw string) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "v")
	raw = strings.TrimPrefix(raw, "V")
	parts := strings.Split(raw, ".")
	if len(parts) < 3 {
		return
	}
	x, errX := strconv.Atoi(parts[0])
	y, errY := strconv.Atoi(parts[1])
	z, errZ := strconv.Atoi(parts[2])
	if errX != nil || errY != nil || errZ != nil {
		return
	}
	if x < 0 || x > 255 || y < 0 || y > 255 || z < 0 || z > 255 {
		return
	}
	Version_x = byte(x)
	Version_y = byte(y)
	Version_z = byte(z)
}

// Version returns Xray's version as a string, in the form of "x.y.z" where x, y and z are numbers.
// ".z" part may be omitted in regular releases.
func Version() string {
	return fmt.Sprintf("%v.%v.%v", Version_x, Version_y, Version_z)
}

// VersionStatement returns a list of strings representing the full version info.
func VersionStatement() []string {
	return []string{
		serial.Concat("Xray ", Version(), " (", codename, ") ", build, " (", runtime.Version(), " ", runtime.GOOS, "/", runtime.GOARCH, ")"),
		intro,
	}
}
