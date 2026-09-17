// Package gcxtool provides a session-bound CLI capability, not a tool per resource.
package gcxtool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Request struct {
	Args     []string `json:"args" jsonschema:"description=gcx arguments without executable or context. Discover exact commands with help-tree and --help. Use @manifest for an immutable supplied manifest file."`
	Manifest string   `json:"manifest,omitempty" jsonschema:"description=Optional JSON or YAML file content. Refer to it with @manifest in args. Never include secrets."`
	Reason   string   `json:"reason" jsonschema:"description=Brief user-visible purpose and expected impact, not private reasoning."`
}

type Action struct {
	ID        string  `json:"id"`
	SessionID string  `json:"sessionId"`
	Stack     string  `json:"stack"`
	Request   Request `json:"request"`
	Status    string  `json:"status"`
	Output    string  `json:"output"`
}

var slugPattern = regexp.MustCompile(`^democompiler[a-f0-9]{12}$`)
var secretPattern = regexp.MustCompile(`(?i)(glsa_[a-z0-9_=-]+|glc_[a-z0-9_=-]+|Bearer\s+[^\s"']+|Basic\s+[^\s"']+)`)
var secretField = regexp.MustCompile(`(?i)("(?:token|password|secret|apiKey|authorization|access_token|refresh_token)"\s*:\s*)"[^"\n]*"`)
var localReference = regexp.MustCompile(`(?i)fromFile|fromEnv`)

func Redact(value string) string {
	return secretField.ReplaceAllString(secretPattern.ReplaceAllString(value, "[REDACTED]"), `${1}"[REDACTED]"`)
}

type flag struct {
	Name      string `json:"name"`
	Shorthand string `json:"shorthand"`
	Type      string `json:"type"`
}
type command struct {
	Path  string `json:"full_path"`
	Flags []flag `json:"flags"`
}

// Classify validates against the installed CLI catalog. Only known read semantics
// bypass approval; new commands automatically remain usable with approval.
func Classify(ctx context.Context, req Request) (read bool, err error) {
	if len(req.Args) == 0 || len(req.Args) > 64 || len(req.Manifest) > 128*1024 {
		return false, errors.New("invalid command size")
	}
	for _, arg := range req.Args {
		if len(arg) > 16384 || strings.ContainsAny(arg, "\x00\r\n") || Redact(arg) != arg {
			return false, errors.New("invalid argument or secret in command")
		}
	}
	if Redact(req.Manifest) != req.Manifest || localReference.MatchString(req.Manifest) {
		return false, errors.New("do not include credentials in manifests")
	}
	root := req.Args[0]
	if !strings.Contains("|resources|dashboards|datasources|metrics|logs|traces|profiles|alert|slo|synthetic-monitoring|fleet|frontend|appo11y|kg|irm|k6|agento11y|help-tree|", "|"+root+"|") {
		return false, errors.New("this tool supports session Grafana resource commands, not Cloud account, auth, config, shell, or other agent operations")
	}
	payload, err := run(ctx, "", []string{"commands", "--flat", "-o", "json"}, "", 4*1024*1024)
	if err != nil {
		return false, errors.New("could not load gcx command catalog")
	}
	var catalog struct {
		Commands []command `json:"commands"`
	}
	if err = json.Unmarshal([]byte(payload), &catalog); err != nil {
		return false, err
	}
	var selected command
	var count int
	for _, candidate := range catalog.Commands {
		parts := strings.Fields(strings.TrimPrefix(candidate.Path, "gcx "))
		if len(parts) > len(req.Args) || len(parts) <= count {
			continue
		}
		if strings.Join(req.Args[:len(parts)], " ") == strings.Join(parts, " ") {
			selected = candidate
			count = len(parts)
		}
	}
	if count == 0 {
		return false, errors.New("unknown gcx command; discover it with help-tree")
	}
	flags := map[string]flag{"--help": {Name: "help", Type: "bool"}, "-h": {Name: "help", Type: "bool"}}
	for _, f := range selected.Flags {
		flags["--"+f.Name] = f
		if f.Shorthand != "" {
			flags["-"+f.Shorthand] = f
		}
	}
	allowedFlags := "|help|output|json|jq|depth|datasource|expr|from|to|since|step|time|limit|start|end|query|label|labels|llm|uid|name|title|description|folder|folder-uid|namespace|selector|all-namespaces|state|status|page|per-page|force|yes|dry-run|overwrite|on-error|max-concurrent|include-managed|omit-manager-fields|file|path|"
	help, usedManifest := false, false
	for i := count; i < len(req.Args); i++ {
		a := req.Args[i]
		if strings.HasPrefix(a, "-") {
			name, value, hasValue := strings.Cut(a, "=")
			f, ok := flags[name]
			if !ok || !strings.Contains(allowedFlags, "|"+f.Name+"|") {
				return false, fmt.Errorf("flag %s is not enabled in this session tool", name)
			}
			if f.Type != "bool" {
				if !hasValue {
					i++
					if i >= len(req.Args) {
						return false, errors.New("missing flag value")
					}
					value = req.Args[i]
				}
				if f.Name == "file" || f.Name == "path" {
					if value != "@manifest" || req.Manifest == "" {
						return false, errors.New("file inputs must use @manifest and supplied manifest content")
					}
					usedManifest = true
				}
				if strings.HasPrefix(value, "@") && value != "@manifest" {
					return false, errors.New("external file references are not supported")
				}
			}
			if f.Name == "help" {
				help = !hasValue || value == "true"
			}
		} else if strings.HasPrefix(a, "/") || strings.Contains(a, "..") || strings.HasPrefix(a, "@") {
			return false, errors.New("external paths are not supported")
		}
	}
	if req.Manifest != "" && !usedManifest {
		return false, errors.New("manifest provided without a file/path @manifest argument")
	}
	leaf := strings.Fields(selected.Path)
	verb := leaf[len(leaf)-1]
	read = help || root == "help-tree" || strings.Contains("|get|list|list-types|list-examples|query|labels|tags|series|metadata|status|health|", "|"+verb+"|")
	// Generic datasource queries can execute arbitrary database queries; require approval.
	if root == "datasources" && (verb == "query" || strings.Contains(selected.Path, "generic")) {
		read = false
	}
	if usedManifest {
		read = false
	}
	return read, nil
}

func Execute(ctx context.Context, stack string, req Request) (string, error) {
	if _, err := Classify(ctx, req); err != nil {
		return "", err
	}
	if req.Args[0] != "help-tree" {
		if err := CheckContext(ctx, stack); err != nil {
			return "", err
		}
	}
	dir, err := os.MkdirTemp("", "demo-gcx-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	args := append([]string(nil), req.Args...)
	if req.Manifest != "" {
		path := filepath.Join(dir, "manifest.yaml")
		if err := os.WriteFile(path, []byte(req.Manifest), 0600); err != nil {
			return "", err
		}
		for i, a := range args {
			if a == "@manifest" {
				args[i] = path
			}
			if strings.HasSuffix(a, "=@manifest") {
				args[i] = strings.TrimSuffix(a, "@manifest") + path
			}
		}
	}
	args = append(args, "--context", stack)
	output, err := run(ctx, dir, args, "", 6000)
	return Redact(output), err
}

func CheckContext(ctx context.Context, stack string) error {
	if !slugPattern.MatchString(stack) {
		return errors.New("no dedicated demo stack is available")
	}
	output, err := run(ctx, "", []string{"config", "view", "--context", stack, "--minify", "-o", "json"}, "", 1024*1024)
	if err != nil {
		return fmt.Errorf("one-time login required: gcx login %s --server https://%s.grafana.net --oauth", stack, stack)
	}
	var cfg struct {
		Contexts map[string]struct {
			Stack string `json:"stack"`
		} `json:"contexts"`
		Stacks map[string]struct {
			Grafana struct {
				Server string `json:"server"`
			} `json:"grafana"`
		} `json:"stacks"`
	}
	if json.Unmarshal([]byte(output), &cfg) != nil || strings.TrimRight(cfg.Stacks[cfg.Contexts[stack].Stack].Grafana.Server, "/") != "https://"+stack+".grafana.net" {
		return errors.New("gcx context must point to this session's exact Grafana Cloud URL")
	}
	return nil
}

type limitedBuffer struct {
	data      []byte
	max       int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.max - len(b.data)
	if len(p) > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	b.data = append(b.data, p...)
	return n, nil
}

func run(ctx context.Context, dir string, args []string, stdin string, limit int) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gcx", args...)
	cmd.Dir = dir
	// Do not inherit ambient Grafana endpoint/token overrides from the compiler.
	for _, key := range []string{"HOME", "PATH", "USER", "TMPDIR", "XDG_CONFIG_HOME", "SSL_CERT_FILE"} {
		if v, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+v)
		}
	}
	cmd.Env = append(cmd.Env, "GCX_AGENT_MODE=1")
	cmd.Stdin = strings.NewReader(stdin)
	buf := &limitedBuffer{max: limit}
	stderr := &limitedBuffer{max: 4000}
	cmd.Stdout = buf
	cmd.Stderr = stderr
	err := cmd.Run()
	output := string(buf.data)
	if err != nil {
		output += "\n" + string(stderr.data)
	}
	if buf.truncated {
		output += "\n[Output truncated; narrow the query.]"
	}
	return output, err
}
