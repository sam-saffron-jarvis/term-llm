package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestClaudeVaultInitialExchangeRejectsRefreshMetadataOmissions(t *testing.T) {
	for _, omission := range []string{"scope", "refresh_token", "both"} {
		t.Run(omission, func(t *testing.T) {
			f := newClaudeForwardProbeFixture(t)
			f.oauth = func(w http.ResponseWriter, r *http.Request) {
				response := map[string]any{"access_token": "real-access", "refresh_token": "real-refresh", "token_type": "Bearer", "expires_in": 3600, "scope": "user:profile user:inference"}
				if omission != "scope" {
					delete(response, "refresh_token")
				}
				if omission != "refresh_token" {
					delete(response, "scope")
				}
				json.NewEncoder(w).Encode(response)
			}
			response := claudeVaultDirect(t, f, claudeVaultCodePayload)
			if response.Code == 200 || f.oauthCalls.Load() != 1 || strings.Contains(response.Body.String(), "real-access") || strings.Contains(response.Body.String(), "real-refresh") {
				t.Fatal("initial code exchange accepted omitted grant metadata", response.Code)
			}
		})
	}
}

func TestClaudeVaultRefreshOmissionsRetainCustodyWithoutScopeEscalation(t *testing.T) {
	for _, omission := range []string{"scope", "refresh_token", "both"} {
		t.Run(omission, func(t *testing.T) {
			f := newClaudeForwardProbeFixture(t)
			const granted = "user:profile user:inference user:mcp_servers"
			f.oauth = func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]string
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				response := map[string]any{"access_token": "synthetic-vendor-access", "refresh_token": "first-real-refresh", "token_type": "Bearer", "expires_in": 3600, "scope": granted}
				if payload["grant_type"] == "refresh_token" {
					if payload["refresh_token"] != "first-real-refresh" {
						t.Error("refresh placeholder not resolved from prior custody")
					}
					response["refresh_token"] = "next-real-refresh"
					if omission != "scope" {
						delete(response, "refresh_token")
					}
					if omission != "refresh_token" {
						delete(response, "scope")
					}
				}
				json.NewEncoder(w).Encode(response)
			}
			initial := claudeVaultDecode(t, claudeVaultDirect(t, f, claudeVaultCodePayload))
			execProxySQL(t, f.service.store, "UPDATE accounts SET expires=1 WHERE name='claude'")
			conn, reader := f.native(t)
			if code, _ := claudeForwardNativeCall(t, conn, reader, "GET", "/v1/models", claudeAPIHost, initial.AccessToken, ""); code != 200 {
				t.Fatal("shared request could not renew expiring custody", code)
			}
			conn.Close()
			claudeForwardWaitSockets(t, f.service)
			a, err := f.service.store.account("claude")
			if err != nil {
				t.Fatal(err)
			}
			forwarder, err := f.service.claudeForwarder()
			if err != nil {
				t.Fatal(err)
			}
			secret, err := claudeSharedSecret(forwarder.ca.vaultKey, a)
			expectedRefresh := "first-real-refresh"
			if omission == "scope" {
				expectedRefresh = "next-real-refresh"
			}
			if err != nil || secret.Scope != granted || secret.Refresh != expectedRefresh {
				t.Fatal("fallback custody not persisted for subsequent generations", secret.Scope, err)
			}
			payload, _ := json.Marshal(map[string]string{"grant_type": "refresh_token", "client_id": claudeClientID, "refresh_token": initial.RefreshToken, "scope": "user:profile user:inference user:plugins org:create_api_key"})
			response := claudeVaultDirect(t, f, string(payload))
			reply := claudeVaultDecode(t, response)
			if reply.AccessToken != initial.AccessToken || reply.RefreshToken != initial.RefreshToken || reply.Scope != granted || f.oauthCalls.Load() != 2 {
				t.Fatal("local refresh rotated aliases, changed granted scopes, or contacted the issuer")
			}
			if strings.Contains(response.Body.String(), "real-refresh") || strings.Contains(response.Body.String(), "synthetic-vendor-access") {
				t.Fatal("local refresh exposed the issuer's credentials")
			}
		})
	}
}

func TestClaudeVaultRefreshFallbackDoesNotMaskExplicitInvalidGrant(t *testing.T) {
	previous := &claudeVaultRecord{state: "ready", private: claudeVaultPrivate{Refresh: "real-refresh", Scope: "user:profile user:inference"}}
	for _, invalid := range []string{`"scope":"org:create_api_key"`, `"scope":"user:profile user:inference org:create_api_key"`, `"scope":""`, `"scope":null`, `"refresh_token":""`, `"refresh_token":null`} {
		response := []byte(`{"access_token":"real-access","token_type":"Bearer","expires_in":3600,` + invalid + `}`)
		if _, err := parseClaudeVaultReplyForRefresh(response, previous); err == nil {
			t.Fatal("explicit invalid issuer grant masked by fallback", invalid)
		}
	}
	missingScope := *previous
	missingScope.private.Scope = ""
	if _, err := parseClaudeVaultReplyForRefresh([]byte(`{"access_token":"real-access","token_type":"Bearer","expires_in":3600}`), &missingScope); err == nil {
		t.Fatal("scope manufactured for old custody lacking validated scope")
	}
	reservation := *previous
	reservation.state = "exchanging"
	if _, err := parseClaudeVaultReplyForRefresh([]byte(`{"access_token":"real-access","token_type":"Bearer","expires_in":3600}`), &reservation); err == nil {
		t.Fatal("initial code reservation supplied refresh fallback")
	}
}

func TestClaudeVaultExplicitConsoleScopeStillRejectsWithRefreshFallback(t *testing.T) {
	f := newClaudeForwardFixture(t)
	f.oauth = func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"access_token":"real-access","token_type":"Bearer","expires_in":3600,"scope":"user:profile user:inference org:create_api_key"}`)
	}
	execProxySQL(t, f.service.store, "UPDATE accounts SET expires=1 WHERE name='claude'")
	conn, reader := f.native(t)
	defer conn.Close()
	for range 2 {
		if code, _ := claudeForwardNativeCall(t, conn, reader, "GET", "/v1/models", claudeAPIHost, f.access, ""); code == 200 {
			t.Fatal("Console grant accepted for native access")
		}
	}
	if f.oauthCalls.Load() != 1 || f.vendorCalls.Load() != 0 {
		t.Fatal("uncertain refresh replayed or request forwarded")
	}
}
