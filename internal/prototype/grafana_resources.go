package prototype

import (
	"path"
	"strings"
)

// IsGrafanaResourcePath separates known resource manifests from app source.
// Legacy files can still be read/copied, but model writes belong to gcx.
func IsGrafanaResourcePath(name string) bool {
	name = path.Clean(name)
	if path.Base(name) == "alloy.yaml" || path.Base(name) == "alloy.yml" {
		return false
	}
	switch path.Ext(name) {
	case ".json", ".yaml", ".yml":
	default:
		return false
	}
	for _, prefix := range []string{"grafana/", "dashboards/", "alerts/", "slos/", "datasources/"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
