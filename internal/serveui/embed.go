package serveui

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

//go:embed static/*
var staticFiles embed.FS

// IndexHTML returns the embedded UI page.
func IndexHTML() []byte {
	data, err := StaticAsset("index.html")
	if err != nil {
		return nil
	}
	return data
}

// StaticAsset returns a copy of an embedded serve-ui asset.
func StaticAsset(name string) ([]byte, error) {
	cleanName := strings.TrimSpace(strings.TrimPrefix(name, "/"))
	if cleanName == "" || strings.Contains(cleanName, "..") {
		return nil, fmt.Errorf("invalid asset name %q", name)
	}
	data, err := fs.ReadFile(staticFiles, "static/"+cleanName)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(data))
	copy(out, data)
	return out, nil
}
