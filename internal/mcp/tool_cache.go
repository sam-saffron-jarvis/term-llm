package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// toolCache is the on-disk format for cached tool lists per server.
type toolCache struct {
	Servers map[string][]ToolSpec `json:"servers"`
}

func toolCachePath() (string, error) {
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		configDir = filepath.Join(home, ".config")
	}
	return filepath.Join(configDir, "term-llm", "mcp-tools-cache.json"), nil
}

// CacheTools writes the tool list for a server to the cache file.
func CacheTools(serverName string, tools []ToolSpec) {
	path, err := toolCachePath()
	if err != nil {
		return
	}

	cache := loadToolCache(path)
	cache.Servers[serverName] = tools

	data, err := json.Marshal(cache)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0644)
}

// LoadCachedTools returns the cached tool list for a server, or nil if not cached.
func LoadCachedTools(serverName string) []ToolSpec {
	path, err := toolCachePath()
	if err != nil {
		return nil
	}
	cache := loadToolCache(path)
	return cache.Servers[serverName]
}

func loadToolCache(path string) toolCache {
	cache := toolCache{Servers: make(map[string][]ToolSpec)}
	data, err := os.ReadFile(path)
	if err != nil {
		return cache
	}
	_ = json.Unmarshal(data, &cache)
	if cache.Servers == nil {
		cache.Servers = make(map[string][]ToolSpec)
	}
	return cache
}
