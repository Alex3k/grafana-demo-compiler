package guidance

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

//go:embed *.md
var documents embed.FS

// Catalog is generated from the embedded files: adding guidance needs no registry edit.
func Catalog() string {
	files, _ := fs.Glob(documents, "*.md")
	return strings.Join(files, ", ")
}

func Read(name string) (string, error) {
	if strings.ContainsAny(name, "/\\") {
		return "", fmt.Errorf("use a name from the guidance catalog")
	}
	data, err := documents.ReadFile(name)
	return string(data), err
}
