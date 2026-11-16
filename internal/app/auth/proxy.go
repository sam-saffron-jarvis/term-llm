package auth

import (
	"fmt"
	"github.com/term-llm/term-llm/internal/config"
)

func managedProxy(provider string) (string, error) {
	cfg, err := config.LoadWithoutCredentials()
	if err != nil {
		return "", err
	}
	if cfg.Proxy.URL == "" {
		return "", nil
	}
	if p, ok := cfg.Providers[provider]; ok && p.ProxyBinding != "" {
		return cfg.Proxy.URL + "/_proxy/", nil
	}
	return "", nil
}
func refuseManagedAuth(provider string) error {
	url, err := managedProxy(provider)
	if err != nil {
		return err
	}
	if url != "" {
		return fmt.Errorf("%s sign-in is managed by your credential proxy; open %s (local login was not changed)", provider, url)
	}
	return nil
}
