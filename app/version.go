// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package app

import (
	"runtime/debug"
	"strconv"
	"strings"
)

const devVersion = "dev"

// resolveVersion falls back to the module version so `go install` builds, which
// carry no ldflags, report the tag they came from. Pseudo-versions stay "dev":
// they name a commit rather than a release.
func resolveVersion(embedded string, info *debug.BuildInfo, ok bool) string {
	if embedded == "" {
		embedded = devVersion
	}
	if embedded != devVersion {
		return embedded
	}
	if !ok || info == nil {
		return devVersion
	}
	moduleVersion := strings.TrimPrefix(info.Main.Version, "v")
	if strings.ContainsAny(moduleVersion, "-+") || strings.Count(moduleVersion, ".") != 2 {
		return devVersion
	}
	for _, field := range strings.Split(moduleVersion, ".") {
		if _, err := strconv.Atoi(field); err != nil {
			return devVersion
		}
	}
	return moduleVersion
}
