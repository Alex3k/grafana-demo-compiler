package prototype

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"sort"

	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
	"github.com/Alex3k/grafana-demo-compiler/internal/telemetryconfig"
)

// ReadSource only exposes recorded generated artifacts, never runtime .env files.
func ReadSource(base domain.PrototypeIteration, path string) (string, error) {
	clean, err := safePath(path)
	if err != nil || !allowedFile(clean) {
		return "", errors.New("not a generated source file")
	}
	found := false
	for _, a := range base.Artifacts {
		if a.Path == clean {
			found = true
			break
		}
	}
	if !found {
		return "", errors.New("file is not in this iteration's artifact inventory")
	}
	root, err := os.OpenRoot(base.RootPath)
	if err != nil {
		return "", err
	}
	defer root.Close()
	f, err := root.Open(clean)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("not a regular source file")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if len(data) > maxFileBytes {
		return "", errors.New("source file exceeds size limit")
	}
	return string(data), err
}

func SourceSnapshot(base domain.PrototypeIteration) (map[string]string, string, error) {
	files := map[string]string{}
	var paths []string
	total := 0
	for _, a := range base.Artifacts {
		if !allowedFile(a.Path) {
			continue
		}
		content, err := ReadSource(base, a.Path)
		if err != nil {
			return nil, "", err
		}
		if _, ok := files[a.Path]; ok {
			continue
		}
		files[a.Path] = content
		paths = append(paths, a.Path)
		total += len(content)
		if len(files) > maxFiles || total > maxTotal {
			return nil, "", errors.New("source snapshot exceeds prototype limits")
		}
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		hash.Write([]byte(path))
		hash.Write([]byte{0})
		hash.Write([]byte(files[path]))
		hash.Write([]byte{0})
	}
	return files, hex.EncodeToString(hash.Sum(nil)), nil
}

func ValidateRevisionPath(path string) error {
	if telemetryconfig.IsFoundationPath(path) {
		return errors.New("compiler-owned telemetry cannot be edited by a prototype revision; regenerate to update the foundation")
	}
	clean, err := safePath(path)
	if err != nil {
		return err
	}
	if clean != path || !allowedFile(path) {
		return errors.New("revision path must be a canonical generated source path")
	}
	return nil
}
