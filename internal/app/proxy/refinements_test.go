package proxy

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/sqliteutil"
	"gopkg.in/yaml.v3"
)

func TestFileSecretsRequireDeclaredMatchingOrigin(t *testing.T) {
	s := testService(t)
	file := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(file, []byte("file-held-private\n"), 0600); err != nil {
		t.Fatal(err)
	}
	base := route("file-route", "/file/", "https://example.test/base/", "X-Local", "key")
	for _, tt := range []struct {
		name, origin string
		wantOK       bool
	}{{"absent", "", false}, {"path is not origin", "https://example.test/base/", false}, {"wrong origin", "https://other.test", false}, {"normalized", "https://EXAMPLE.test:0443/", true}} {
		t.Run(tt.name, func(t *testing.T) {
			c := Config{Routes: []Route{base}, Secrets: map[string]SecretRef{"key": {File: file, Origin: tt.origin}}}
			secrets, err := resolveSecrets(c, nil, s.opts.AdminTokenFile, true)
			if err == nil {
				_, err = compile(c, 1, secrets)
			}
			if (err == nil) != tt.wantOK {
				t.Fatalf("origin validation %v", err)
			}
			if tt.wantOK && (secrets["key"].Origin != "https://example.test:443" || secrets["key"].Value != "file-held-private") {
				t.Fatal("file value/binding not normalized")
			}
		})
	}
	if err := os.WriteFile(file, []byte("\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveSecrets(Config{Secrets: map[string]SecretRef{"key": {File: file, Origin: "https://example.test"}}}, nil, s.opts.AdminTokenFile, true); err == nil {
		t.Fatal("empty file credential accepted")
	}
	if _, err := compile(Config{Routes: []Route{base}}, 1, map[string]material{"key": {Value: "private"}}); err == nil {
		t.Fatal("empty material origin bypassed binding")
	}
}

func TestAuthorityFingerprintInvalidatesScopeAndRuleChanges(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Route)
	}{
		{"expanded base", func(r *Route) { r.Upstream = "https://example.test/" }},
		{"narrowed base", func(r *Route) { r.Upstream = "https://example.test/base/narrow/" }},
		{"different outbound header", func(r *Route) { r.SetHeaders = map[string]HeaderRule{"X-Other": {Secret: "key", Prefix: "Real "}} }},
		{"different secret reference", func(r *Route) {
			r.SetHeaders = map[string]HeaderRule{"X-Upstream-Key": {Secret: "admin", Prefix: "Real "}}
		}},
		{"different secret prefix", func(r *Route) {
			r.SetHeaders = map[string]HeaderRule{"X-Upstream-Key": {Secret: "key", Prefix: "Admin "}}
		}},
		{"literal authority changed", func(r *Route) { v := "admin"; r.SetHeaders = map[string]HeaderRule{"X-Upstream-Key": {Value: &v}} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := testService(t)
			r := route("r", "/r/", "https://example.test/base/", "X-Local", "key")
			materials := map[string]material{"key": boundMaterial(t, r.Upstream, "readonly"), "admin": boundMaterial(t, r.Upstream, "admin")}
			configure(t, s, Config{Routes: []Route{r}}, materials)
			token := caller(t, s, "r")
			old := s.snap.routes[0].authority
			tt.mutate(&r)
			configure(t, s, Config{Routes: []Route{r}}, materials)
			if s.snap.routes[0].authority == old {
				t.Fatal("authority change retained fingerprint")
			}
			if _, err := s.store.authorize(token, s.snap.routes[0]); err == nil {
				t.Fatal("old grant retained changed authority")
			}
			r = route("r", "/r/", "https://example.test/base/", "X-Local", "key")
			configure(t, s, Config{Routes: []Route{r}}, materials)
			if _, err := s.store.authorize(token, s.snap.routes[0]); err == nil {
				t.Fatal("reverting scope resurrected old grant")
			}
		})
	}
}

func TestAuthorityCanonicalizationAndRotationPreserveGrants(t *testing.T) {
	s := testService(t)
	r := route("r", "/r/", "https://EXAMPLE.test:443/base", "X-Local", "key")
	r.SetHeaders["X-Label"] = HeaderRule{Value: strPtr("public")}
	configure(t, s, Config{Routes: []Route{r}}, map[string]material{"key": boundMaterial(t, r.Upstream, "old-value")})
	token := caller(t, s, "r")
	old := s.snap.routes[0].authority
	r.Upstream = "https://example.test/base/"
	r.LocalPrefix = "/renamed-local/"
	r.CallerAuth = Auth{Header: "Authorization", Prefix: "Bearer "}
	r.Enabled = false
	r.SetHeaders = map[string]HeaderRule{"x-label": {Value: strPtr("public")}, "x-upstream-key": {Secret: "key", Prefix: "Real "}}
	configure(t, s, Config{Routes: []Route{r}}, map[string]material{"key": boundMaterial(t, r.Upstream, "rotated-value")})
	if s.snap.routes[0].authority != old {
		t.Fatal("equivalent scope or routine rotation changed fingerprint")
	}
	if _, err := s.store.authorize(token, s.snap.routes[0]); err != nil {
		t.Fatal("canonical/rotation change lost grants", err)
	}
}
func strPtr(s string) *string { return &s }

func TestUnsafeSecretReferenceIsNotEchoedInValidationError(t *testing.T) {
	s := testService(t)
	cookie, csrf := loginAdmin(t, s)
	for _, secret := range []string{"accidentally-pasted-valid-slug-key", "Bearer PRIVATE-SENSITIVE\nvalue", strings.Repeat("s", 65)} {
		r := route("r", "/r/", "https://example.test/", "Authorization", secret)
		rr := adminRequest(s, "PUT", "config", map[string]any{"revision": s.snap.revision, "routes": []Route{r}}, cookie, csrf)
		if rr.Code != 400 || strings.Contains(rr.Body.String(), secret) {
			t.Fatalf("unsafe validation %d %s", rr.Code, rr.Body.String())
		}
		var response map[string]string
		if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil || response["error"] == "" {
			t.Fatal("missing safe validation message")
		}
	}
}

func TestSecretCreateReplaceAndGrantEditing(t *testing.T) {
	s := testService(t)
	cookie, csrf := loginAdmin(t, s)
	put := func(value string, replace bool) *httptest.ResponseRecorder {
		return adminRequest(s, "PUT", "secrets", map[string]any{"name": "key", "origin": "https://example.test", "value": value, "replace": replace}, cookie, csrf)
	}
	if rr := put("original", false); rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	revision := s.snap.revision
	if rr := put("accidental overwrite", false); rr.Code != 409 || s.snap.revision != revision || s.snap.secrets["key"].Value != "original" {
		t.Fatal("duplicate create overwrote secret", rr.Body.String())
	}
	a := route("a", "/a/", "https://example.test/base/", "X-Local", "key")
	b := route("b", "/b/", "https://example.test/other/", "X-Local", "key")
	rr := adminRequest(s, "PUT", "config", map[string]any{"revision": s.snap.revision, "routes": []Route{a, b}}, cookie, csrf)
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	c, token, err := s.store.createCaller("editable", []string{"a"}, s.snap)
	if err != nil {
		t.Fatal(err)
	}
	if rr := put("rotated", true); rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	if _, err := s.store.authorize(token, s.snap.routes[0]); err != nil {
		t.Fatal("routine rotation revoked grant")
	}
	edit := func(grants []string, revision int64) *httptest.ResponseRecorder {
		return adminRequest(s, "PUT", "callers", map[string]any{"id": c.ID, "grants": grants, "revision": revision}, cookie, csrf)
	}
	if rr := edit([]string{"b"}, s.snap.revision-1); rr.Code != 409 {
		t.Fatal("stale grants edit accepted")
	}
	if rr := edit([]string{"a", "unknown"}, s.snap.revision); rr.Code != 400 {
		t.Fatal("unknown grant accepted")
	}
	if _, err := s.store.authorize(token, s.snap.routes[0]); err != nil {
		t.Fatal("failed edit did not roll back")
	}
	if rr := edit([]string{"b"}, s.snap.revision); rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	if _, err := s.store.authorize(token, s.snap.routes[0]); err == nil {
		t.Fatal("removed grant still allowed")
	}
	if _, err := s.store.authorize(token, s.snap.routes[1]); err != nil {
		t.Fatal("edited grant required token reissue", err)
	}
	var hash string
	s.store.db.QueryRow("SELECT hash FROM callers WHERE id=?", c.ID).Scan(&hash)
	if hash != hashToken(token) {
		t.Fatal("edit changed token hash")
	}
	rr = adminRequest(s, "DELETE", "callers", map[string]string{"id": c.ID}, cookie, csrf)
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	if rr := edit([]string{"a"}, s.snap.revision); rr.Code != 400 {
		t.Fatal("revoked caller granted new access")
	}
}

func TestSlashPrefixedPathsAndFullLocalFilters(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	defer up.Close()
	s := testService(t)
	configure(t, s, Config{Routes: []Route{route("r", "/local/", up.URL, "X-Local", "key")}}, map[string]material{"key": boundMaterial(t, up.URL, "real")})
	token := caller(t, s, "r")
	if rr := call(s, "GET", "/local/v1/item?private=never-log", "X-Local", token, nil); rr.Code != 200 {
		t.Fatal(rr.Code)
	}
	for _, path := range []string{"/v1/", "/local/v1/"} {
		rows, _, err := s.store.records(url.Values{"path": {path}}, 50)
		if err != nil || len(rows) != 1 || rows[0].Path != "/v1/item" || rows[0].LocalPrefix != "/local/" {
			t.Fatalf("path filter %s %+v %v", path, rows, err)
		}
	}
	legacy := Record{ID: "legacy", Path: "v1/old", Outcome: "unknown"}
	if err := s.store.start(context.Background(), &legacy); err != nil {
		t.Fatal(err)
	}
	rows, _, err := s.store.records(url.Values{"path": {"/v1/old"}}, 50)
	if err != nil || len(rows) != 1 || rows[0].Path != "/v1/old" {
		t.Fatal("legacy path not normalized", rows, err)
	}
}

func TestGrantSchemaMigrationIsFailClosedAndPreservesCaller(t *testing.T) {
	if err := sqliteutil.ValidateMigrations(migrations, 2, schemaVersion, true); err != nil {
		t.Fatal(err)
	}
	s := testService(t)
	// Faithful synthetic fixture of the initial feature's v1 schema and data.
	if _, err := s.store.db.Exec("ALTER TABLE grants RENAME COLUMN authority TO origin; PRAGMA user_version=1;"); err != nil {
		t.Fatal(err)
	}
	token := "sk-tlp-synthetic-original-token"
	_, err := s.store.db.Exec("INSERT INTO callers(id,name,hash,created) VALUES('legacy','Legacy',?,1);INSERT INTO grants VALUES('legacy','route','https://example.test:443')", hashToken(token))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.initialize(); err != nil {
		t.Fatal(err)
	}
	var grants int
	var hash string
	s.store.db.QueryRow("SELECT count(*) FROM grants").Scan(&grants)
	s.store.db.QueryRow("SELECT hash FROM callers WHERE id='legacy'").Scan(&hash)
	if grants != 0 || hash != hashToken(token) {
		t.Fatal("migration retained unsafe grants or changed caller")
	}
	fresh, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if _, err = fresh.Exec(schema); err != nil {
		t.Fatal(err)
	}
	want, err := sqliteutil.SchemaSignature(context.Background(), fresh)
	if err != nil {
		t.Fatal(err)
	}
	got, err := sqliteutil.SchemaSignature(context.Background(), s.store.db)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("fresh/migrated schema mismatch %v\n%v\n%v", err, got, want)
	}
}

func TestCompatibleFileModeGrantsRemainButMissingRoutesNeverResurrect(t *testing.T) {
	s := testService(t)
	a := route("a", "/a/", "https://example.test/a/", "X-Local", "key")
	b := route("b", "/b/", "https://example.test/b/", "X-Local", "key")
	materials := map[string]material{"key": boundMaterial(t, a.Upstream, "real")}
	configure(t, s, Config{Routes: []Route{a, b}}, materials)
	token := caller(t, s, "a", "b")
	next, err := compile(Config{Routes: []Route{a}}, s.snap.revision+1, materials)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.store.saveConfig(next, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.authorize(token, next.routes[0]); err != nil {
		t.Fatal("compatible file-mode grant lost", err)
	}
	configure(t, s, Config{Routes: []Route{a, b}}, materials)
	if _, err := s.store.authorize(token, s.snap.routes[0]); err != nil {
		t.Fatal("compatible grant lost after returning to UI")
	}
	if _, err := s.store.authorize(token, s.snap.routes[1]); err == nil {
		t.Fatal("route absent in file mode resurrected its grant")
	}
}

func TestCallerRevocationAndRouteChangesAffectOnlyNewRequests(t *testing.T) {
	for _, action := range []string{"revoke", "disable", "delete"} {
		t.Run(action, func(t *testing.T) {
			release := make(chan struct{})
			released := false
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: admitted\n\n")
				w.(http.Flusher).Flush()
				select {
				case <-release:
					io.WriteString(w, "data: completed\n\n")
				case <-r.Context().Done():
				}
			}))
			defer up.Close()
			defer func() {
				if !released {
					close(release)
				}
			}()
			s := testService(t)
			r := route("r", "/r/", up.URL, "X-Local", "key")
			configure(t, s, Config{Routes: []Route{r}}, map[string]material{"key": boundMaterial(t, up.URL, "real")})
			token := caller(t, s, "r")
			server := httptest.NewServer(s)
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/r/events", nil)
			req.Header.Set("X-Local", "Local "+token)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			reader := bufio.NewReader(resp.Body)
			if _, err = reader.ReadString('\n'); err != nil {
				t.Fatal(err)
			}
			cookie, csrf := loginAdmin(t, s)
			switch action {
			case "revoke":
				callers, err := s.store.callers()
				if err != nil {
					t.Fatal(err)
				}
				if rr := adminRequest(s, "DELETE", "callers", map[string]string{"id": callers[0].ID}, cookie, csrf); rr.Code != 200 {
					t.Fatal(rr.Body.String())
				}
			case "disable":
				r.Enabled = false
				if rr := adminRequest(s, "PUT", "config", map[string]any{"revision": s.snap.revision, "routes": []Route{r}}, cookie, csrf); rr.Code != 200 {
					t.Fatal(rr.Body.String())
				}
			case "delete":
				if rr := adminRequest(s, "PUT", "config", map[string]any{"revision": s.snap.revision, "routes": []Route{}}, cookie, csrf); rr.Code != 200 {
					t.Fatal(rr.Body.String())
				}
			}
			next := call(s, "GET", "/r/new", "X-Local", token, nil)
			if next.Code != 401 && next.Code != 404 {
				t.Fatal("new request not blocked", next.Code)
			}
			close(release)
			released = true
			rest, err := io.ReadAll(reader)
			if err != nil || !strings.Contains(string(rest), "data: completed") {
				t.Fatalf("already dispatched stream canceled %q %v", rest, err)
			}
		})
	}
}

func TestValidAdminBypassesFullInvalidPeerMap(t *testing.T) {
	s := testService(t)
	s.authMu.Lock()
	for i := 0; i < 1024; i++ {
		s.attempts[strconv.Itoa(i)] = attempt{count: 8, since: time.Now()}
	}
	s.authMu.Unlock()
	loginAdmin(t, s)
	if len(s.attempts) > 1024 {
		t.Fatal("invalid login map exceeded bound")
	}
}

func TestGrantMigrationRollsBackAndRetries(t *testing.T) {
	s := testService(t)
	_, err := s.store.db.Exec(`ALTER TABLE grants RENAME COLUMN authority TO origin;
 PRAGMA user_version=1;
 INSERT INTO grants VALUES('legacy','route','https://example.test:443');
 CREATE TRIGGER stop_grant_migration BEFORE DELETE ON grants BEGIN SELECT RAISE(FAIL,'synthetic fault'); END;`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.initialize(); err == nil {
		t.Fatal("failed migration reported success")
	}
	var version, count int
	if err := s.store.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 1 {
		t.Fatal("marker moved despite rollback", version, err)
	}
	if err := s.store.db.QueryRow("SELECT count(origin) FROM grants").Scan(&count); err != nil || count != 1 {
		t.Fatal("schema/data did not roll back", count, err)
	}
	if _, err = s.store.db.Exec("DROP TRIGGER stop_grant_migration"); err != nil {
		t.Fatal(err)
	}
	if err = s.store.initialize(); err != nil {
		t.Fatal("migration retry failed", err)
	}
	if err := s.store.db.QueryRow("SELECT count(authority) FROM grants").Scan(&count); err != nil || count != 0 {
		t.Fatal("unsafe grants survived retry", count, err)
	}
}

func TestStoredSecretRotationDoesNotImplicitlyReloadFileSecrets(t *testing.T) {
	s := testService(t)
	cookie, csrf := loginAdmin(t, s)
	rr := adminRequest(s, "PUT", "secrets", map[string]string{"name": "stored", "origin": "https://example.test", "value": "stored-old"}, cookie, csrf)
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	file := filepath.Join(t.TempDir(), "file-key")
	if err := os.WriteFile(file, []byte("file-old"), 0600); err != nil {
		t.Fatal(err)
	}
	c := Config{Routes: []Route{route("file-route", "/file/", "https://example.test/", "X-Local", "file")}, Secrets: map[string]SecretRef{"file": {File: file, Origin: "https://example.test"}, "stored": {Stored: "stored"}}}
	data, err := yaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(t.TempDir(), "proxy.yaml")
	if err := os.WriteFile(config, data, 0600); err != nil {
		t.Fatal(err)
	}
	s.opts.ConfigFile = config
	rr = adminRequest(s, "POST", "reload", nil, cookie, csrf)
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	if err := os.WriteFile(file, []byte("file-new"), 0600); err != nil {
		t.Fatal(err)
	}
	rr = adminRequest(s, "PUT", "secrets", map[string]any{"name": "stored", "origin": "https://example.test", "value": "stored-new", "replace": true}, cookie, csrf)
	if rr.Code != 200 || s.snap.secrets["file"].Value != "file-old" || s.snap.secrets["stored"].Value != "stored-new" {
		t.Fatal("stored rotation implicitly reloaded file", rr.Body.String())
	}
	rr = adminRequest(s, "POST", "reload", nil, cookie, csrf)
	if rr.Code != 200 || s.snap.secrets["file"].Value != "file-new" {
		t.Fatal("explicit reload failed", rr.Body.String())
	}
}

func TestOriginCanonicalIPAndCaseSensitiveZones(t *testing.T) {
	a, err := Origin("https://[2001:0db8:0:0:0:0:0:1]:0443/")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Origin("https://[2001:db8::1]/")
	if err != nil || a != b {
		t.Fatal("equivalent IP origins differ", a, b, err)
	}
	one, err := Origin("http://[fe80::1%25eth0]/")
	if err != nil {
		t.Fatal(err)
	}
	other, err := Origin("http://[fe80::1%25ETH0]/")
	if err != nil || one == other {
		t.Fatal("different IPv6 zones collapsed", one, other, err)
	}
	if again, err := secretOrigin(one); err != nil || again != one {
		t.Fatal("scoped origin did not roundtrip", one, again, err)
	}
}
