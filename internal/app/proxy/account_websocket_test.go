package proxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAccountWebsocketCustodyAndAccountRevocation(t *testing.T) {
	for _, action := range []string{"signout", "replacement", "needs_login"} {
		t.Run(action, func(t *testing.T) {
			var issuer, inference atomic.Int32
			up := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/oauth/token" {
					issuer.Add(1)
					w.WriteHeader(400)
					io.WriteString(w, `{"error":"invalid_grant"}`)
					return
				}
				inference.Add(1)
				if r.Proto != "HTTP/1.1" || r.Header.Get("Authorization") != "Bearer synthetic-old-access" || r.Header.Get("ChatGPT-Account-ID") != "synthetic-account-id" {
					t.Errorf("account upgrade identity: %s %v", r.Proto, r.Header)
				}
				conn, rw, err := http.NewResponseController(w).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
				rw.Flush()
				io.Copy(conn, rw)
			}))
			up.EnableHTTP2 = true
			up.StartTLS()
			defer up.Close()
			s := testService(t)
			a := testAccount(t, s, "chatgpt", up, false)
			r := accountRoute(up.URL)
			r.WebSocket = true
			r.Allow = []string{"GET /responses"}
			token := configureAccount(t, s, r)
			done := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.ServeHTTP(w, r); close(done) }))
			defer server.Close()
			conn, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			fmt.Fprintf(conn, "GET /account/responses HTTP/1.1\r\nHost: local\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nAuthorization: Bearer %s\r\nChatGPT-Account-ID: placeholder\r\n\r\n", token)
			reader := bufio.NewReader(conn)
			resp, err := http.ReadResponse(reader, nil)
			if err != nil || resp.StatusCode != 101 {
				t.Fatal("handshake", err)
			}
			wire := []byte{0x89, 0x80, 1, 2, 3, 4}
			if _, err = conn.Write(wire); err != nil {
				t.Fatal(err)
			}
			if _, err = io.ReadFull(reader, make([]byte, len(wire))); err != nil {
				t.Fatal(err)
			}
			if action == "needs_login" {
				if _, err = s.ensureAccount(context.Background(), a.Name, a.Generation); err == nil {
					t.Fatal("rejected refresh succeeded")
				}
			} else {
				cookie, csrf := loginAdmin(t, s)
				var rr *httptest.ResponseRecorder
				if action == "signout" {
					rr = adminRequest(s, "DELETE", "accounts", map[string]string{"name": a.Name}, cookie, csrf)
				} else {
					a.credentials.AccessToken = "synthetic-replacement-access"
					rr = adminRequest(s, "PUT", "accounts", accountInput{Name: a.Name, Kind: a.Kind, Replace: true, Credentials: a.credentials}, cookie, csrf)
				}
				if rr.Code != 200 {
					t.Fatal("account mutation", rr.Code, rr.Body.String())
				}
			}
			if _, err = reader.ReadByte(); err == nil {
				t.Fatal("account socket survived revocation")
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("account socket retained slot")
			}
			if len(s.slots) != 0 || inference.Load() != 1 {
				t.Fatal("slot leak or inference replay")
			}
			records, _, err := s.store.records(nil, 50)
			if err != nil || len(records) != 1 || records[0].Outcome != "closed_by_revocation" {
				t.Fatal("account socket ledger", err, records)
			}
		})
	}
}
