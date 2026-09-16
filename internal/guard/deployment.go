package guard

import (
	"regexp"
	"strings"
)

type Decision struct {
	Blocked bool
	Target  string
}

var deploymentAction = regexp.MustCompile(`\b(deploy|deployment|host|provision|ship|run)\b`)

var remoteTargets = []struct {
	pattern *regexp.Regexp
	name    string
}{
	{regexp.MustCompile(`\baws\b|\bec2\b|\becs\b|\beks\b|\blambda\b`), "AWS"},
	{regexp.MustCompile(`\bazure\b|\baks\b`), "Azure"},
	{regexp.MustCompile(`\bgcp\b|google cloud|cloud run|\bgke\b`), "Google Cloud"},
	{regexp.MustCompile(`\bkubernetes\b|\bk8s\b`), "Kubernetes"},
	{regexp.MustCompile(`remote (host|server|docker)|another (host|server|csp)`), "a remote runtime"},
}

var localIntent = regexp.MustCompile(`\b(local|locally|localhost|docker compose)\b`)

var explicitRemoteIntent = regexp.MustCompile(`\b(to|on|onto|in|using)\s+(aws|azure|gcp|google cloud|kubernetes|k8s|ec2|ecs|eks|lambda|aks|gke|cloud run|a remote|another host|another server|another csp)\b`)

func CheckDeploymentRequest(content string) Decision {
	normalized := strings.ToLower(strings.TrimSpace(content))
	if normalized == "" || !deploymentAction.MatchString(normalized) {
		return Decision{}
	}
	if localIntent.MatchString(normalized) && !explicitRemoteIntent.MatchString(normalized) {
		return Decision{}
	}
	for _, target := range remoteTargets {
		if target.pattern.MatchString(normalized) {
			return Decision{Blocked: true, Target: target.name}
		}
	}
	return Decision{}
}
