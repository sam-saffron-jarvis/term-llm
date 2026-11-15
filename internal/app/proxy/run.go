package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

// Run starts only the proxy, without loading agent/provider configuration.
func Run(ctx context.Context, opts Options, out io.Writer) error {
	s, err := Open(opts)
	if err != nil {
		return err
	}
	defer s.Close()
	listener, err := net.Listen("tcp", net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port)))
	if err != nil {
		return fmt.Errorf("listen for proxy: %w", err)
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	serving, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := &http.Server{Handler: s, ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 64 << 10, BaseContext: func(net.Listener) context.Context { return serving }}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	adminURL := "http://" + listener.Addr().String() + "/_proxy/"
	if s.opts.PublicURL != "" {
		adminURL = s.opts.PublicURL + "/_proxy/"
	}
	fmt.Fprintf(out, "Credential proxy ready at %s (%s mode; %s admin; HTTP backend %s)\n", adminURL, map[bool]string{true: "operator-file", false: "UI-managed"}[opts.ConfigFile != ""], s.opts.Auth, listener.Addr())
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			if err == http.ErrServerClosed {
				return nil
			}
			return err
		case <-tick.C:
			cleanup, c := context.WithTimeout(context.Background(), time.Second)
			_ = s.store.retain(cleanup, s.opts.RetentionDays)
			c()
		case <-ctx.Done():
			drain, c := context.WithTimeout(context.Background(), 20*time.Second)
			err := server.Shutdown(drain)
			c()
			if err != nil {
				cancel()
				_ = server.Close()
				<-done
				wait := time.NewTimer(3 * time.Second)
				defer wait.Stop()
				for len(s.slots) > 0 {
					select {
					case <-wait.C:
						return fmt.Errorf("proxy drain deadline exceeded; unfinished requests remain unknown")
					case <-time.After(10 * time.Millisecond):
					}
				}
			}
			return nil
		}
	}
}

// Health checks the local readiness endpoint only, never an upstream.
func Health(ctx context.Context, host string, port int) error {
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("health host must be loopback")
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, "GET", "http://"+net.JoinHostPort(host, strconv.Itoa(port))+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("proxy not ready: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("proxy not ready: HTTP %d", resp.StatusCode)
	}
	return nil
}
