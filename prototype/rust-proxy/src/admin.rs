//! Token-mode administration subset of Go proxy at 0821cc18f9b6bc69cdf6014c69eb6b837863ceee.
//!
//! Operator-file caller/grant persistence uses Go's v6 SQLite schema. Browser
//! sessions are process-local (as in Go token mode). UI, native custody and
//! active-stream revocation are outside this slice.

use crate::{config::Route, store::Store};
use std::{
    collections::{BTreeMap, HashMap, HashSet},
    fs::File,
    io::{self, Read},
    net::{IpAddr, SocketAddr},
    path::Path,
    sync::Mutex,
    time::{Duration, Instant, SystemTime, UNIX_EPOCH},
};

use axum::{
    body::to_bytes,
    extract::{ConnectInfo, Request},
    http::{HeaderMap, HeaderValue, Method, StatusCode, header},
    response::{IntoResponse, Response},
};
use serde::{Deserialize, Serialize, de::DeserializeOwned};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use subtle::ConstantTimeEq;

const BODY_LIMIT: usize = 1 << 20;
const INITIAL_REVISION: i64 = 1;
const SESSION_AGE: Duration = Duration::from_secs(8 * 3600);
const ATTEMPT_AGE: Duration = Duration::from_secs(60);

pub struct Admin {
    admin_hash: [u8; 32],
    routes: HashSet<String>,
    revision: i64,
    state: Mutex<State>,
}

#[derive(Default)]
struct State {
    sessions: HashMap<[u8; 32], Session>,
    attempts: HashMap<Option<IpAddr>, Attempt>,
    callers: HashMap<String, StoredCaller>,
    store: Option<Store>,
}

#[derive(Clone)]
struct Session {
    csrf: String,
    expires: Instant,
}

struct Attempt {
    since: Instant,
    count: usize,
}

struct StoredCaller {
    caller: Caller,
    hash: [u8; 32],
}

#[derive(Clone, Serialize)]
struct Caller {
    id: String,
    name: String,
    created: u64,
    last_used: u64,
    revoked: bool,
    grants: Vec<String>,
    created_by: String,
    external_id: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Login {
    token: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct CallerInput {
    #[serde(default)]
    id: String,
    #[serde(default)]
    name: String,
    #[serde(default)]
    grants: Option<Vec<String>>,
    revision: Option<i64>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct DeleteCaller {
    id: String,
}

impl Admin {
    fn new(token: &str, route_names: Vec<String>) -> io::Result<Self> {
        if token.len() < 16 || token.len() > 512 || !token.bytes().all(|b| b.is_ascii_graphic()) {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "admin token must be 16–512 visible ASCII characters",
            ));
        }
        let mut routes = HashSet::new();
        for route in route_names {
            if !slug(&route) || !routes.insert(route) {
                return Err(io::Error::new(
                    io::ErrorKind::InvalidInput,
                    "invalid or duplicate route name",
                ));
            }
        }
        // Fail startup rather than discover missing system entropy after login.
        random_token("")?;
        Ok(Self {
            admin_hash: hash(token),
            routes,
            revision: INITIAL_REVISION,
            state: Mutex::new(State::default()),
        })
    }

    pub(crate) fn open_persistent(
        token: &str,
        state_dir: &Path,
        routes: &[Route],
    ) -> Result<Self, Box<dyn std::error::Error>> {
        let authorities: BTreeMap<_, _> = routes
            .iter()
            .map(|route| (route.name.clone(), route.authority.clone()))
            .collect();
        let mut admin = Self::new(token, authorities.keys().cloned().collect())?;
        let store = Store::open(state_dir, &authorities)?;
        admin.revision = store.revision();
        let state = admin
            .state
            .get_mut()
            .map_err(|_| io::Error::other("administration state unavailable"))?;
        for row in store.load()? {
            state.callers.insert(
                row.id.clone(),
                StoredCaller {
                    hash: row.hash,
                    caller: Caller {
                        id: row.id,
                        name: row.name,
                        created: row.created,
                        last_used: row.last_used,
                        revoked: row.revoked,
                        grants: row.grants,
                        created_by: row.created_by,
                        external_id: row.external_id,
                    },
                },
            );
        }
        state.store = Some(store);
        Ok(admin)
    }

    /// Distinguish an admin bearer from an unknown caller before store admission.
    pub fn is_admin_token(&self, token: &str) -> bool {
        bool::from(hash(token).ct_eq(&self.admin_hash))
    }

    pub fn authorize(&self, token: &str, route: &str) -> bool {
        if token.len() > 512 || !self.routes.contains(route) {
            return false;
        }
        let Ok(state) = self.state.lock() else {
            return false;
        };
        let token_hash = hash(token);
        state.callers.values().any(|stored| {
            bool::from(stored.hash.ct_eq(&token_hash))
                && !stored.caller.revoked
                && stored.caller.grants.iter().any(|grant| grant == route)
        })
    }

    pub async fn response(&self, req: Request, bound: SocketAddr) -> Response {
        let mut response = self.dispatch(req, bound).await;
        let headers = response.headers_mut();
        headers.insert(header::CACHE_CONTROL, HeaderValue::from_static("no-store"));
        headers.insert(
            "x-content-type-options",
            HeaderValue::from_static("nosniff"),
        );
        headers.insert("referrer-policy", HeaderValue::from_static("no-referrer"));
        headers.insert(
            "content-security-policy",
            HeaderValue::from_static(
                "default-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'",
            ),
        );
        response
    }

    async fn dispatch(&self, req: Request, bound: SocketAddr) -> Response {
        let Some(origin) = trusted_origin(req.headers(), bound) else {
            return fail(
                403,
                "Admin Host is not allowed; use the bound loopback listener",
            );
        };
        let supplied_origin = single_header(req.headers(), "origin");
        // Reject duplicate/malformed Origin even for safe methods.
        if req.headers().contains_key("origin")
            && supplied_origin != Some("")
            && supplied_origin != Some(origin.as_str())
        {
            return fail(403, "Cross-origin administration is not allowed");
        }
        let mutation = req.method() != Method::GET && req.method() != Method::HEAD;
        if mutation && supplied_origin != Some(origin.as_str()) {
            return fail(403, "A matching Origin is required");
        }
        let path = req.uri().path().to_owned();
        let method = req.method().clone();
        if path == "/_proxy/api/auth/status" && method == Method::GET {
            return reply(200, json!({"auth": "token", "csrf": "", "origin": ""}));
        }
        if path == "/_proxy/api/login" && method == Method::POST {
            return self.login(req).await;
        }
        let cookie = session_cookie(req.headers());
        // Never retain a synchronous mutex guard across body collection/await.
        let session = {
            let Ok(mut state) = self.state.lock() else {
                return unavailable();
            };
            state
                .sessions
                .retain(|_, session| session.expires > Instant::now());
            cookie
                .as_ref()
                .and_then(|value| state.sessions.get(&hash(value)))
                .cloned()
        };
        let Some(session) = session else {
            return fail(401, "Sign in to administer this proxy");
        };
        if mutation
            && !single_header(req.headers(), "x-csrf-token")
                .is_some_and(|csrf| bool::from(csrf.as_bytes().ct_eq(session.csrf.as_bytes())))
        {
            return fail(403, "Session changed; refresh and try again");
        }
        match (path.as_str(), method) {
            ("/_proxy/api/state", Method::GET) => {
                let Ok(state) = self.state.lock() else {
                    return unavailable();
                };
                let mut callers: Vec<Caller> = state
                    .callers
                    .values()
                    .map(|stored| stored.caller.clone())
                    .collect();
                callers.sort_by(|a, b| (a.created, &a.id).cmp(&(b.created, &b.id)));
                for caller in &mut callers {
                    caller.grants.sort();
                }
                reply(
                    200,
                    json!({"mode": "file", "auth": "token", "revision": self.revision,
                    "csrf": session.csrf, "callers": callers}),
                )
            }
            ("/_proxy/api/logout", Method::POST) => {
                let Ok(mut state) = self.state.lock() else {
                    return unavailable();
                };
                if let Some(cookie) = cookie {
                    state.sessions.remove(&hash(&cookie));
                }
                with_cookie(
                    reply(200, json!({"ok": true})),
                    "tlp_admin=; Path=/_proxy/; Max-Age=0; HttpOnly; SameSite=Strict",
                )
            }
            ("/_proxy/api/callers", method)
                if method == Method::POST || method == Method::PUT || method == Method::DELETE =>
            {
                self.change_caller(req, method).await
            }
            _ => fail(404, "Unknown administration endpoint or method"),
        }
    }

    async fn login(&self, req: Request) -> Response {
        let peer = req
            .extensions()
            .get::<ConnectInfo<SocketAddr>>()
            .map(|info| info.0.ip());
        let input: Login = match read_json(req).await {
            Ok(input) => input,
            Err(response) => return response,
        };
        let valid = self.is_admin_token(&input.token);
        let now = Instant::now();
        let Ok(mut state) = self.state.lock() else {
            return unavailable();
        };
        state
            .attempts
            .retain(|_, attempt| now.duration_since(attempt.since) <= ATTEMPT_AGE);
        if !valid {
            let count = state.attempts.get(&peer).map_or(0, |attempt| attempt.count);
            if count >= 8 || (count == 0 && state.attempts.len() >= 1024) {
                let mut response = fail(429, "Too many invalid sign-in attempts; wait one minute");
                response
                    .headers_mut()
                    .insert(header::RETRY_AFTER, HeaderValue::from_static("60"));
                return response;
            }
            state
                .attempts
                .entry(peer)
                .or_insert(Attempt {
                    since: now,
                    count: 0,
                })
                .count += 1;
            return fail(401, "Admin credential not recognized");
        }
        state.attempts.remove(&peer);
        state.sessions.retain(|_, session| session.expires > now);
        if state.sessions.len() >= 64 {
            return fail(
                429,
                "Session limit reached; sign out another browser or restart",
            );
        }
        let (Ok(token), Ok(csrf)) = (random_token("admin_"), random_token("csrf_")) else {
            return unavailable();
        };
        state.sessions.insert(
            hash(&token),
            Session {
                csrf: csrf.clone(),
                expires: now + SESSION_AGE,
            },
        );
        with_cookie(
            reply(200, json!({"csrf": csrf})),
            &format!("tlp_admin={token}; Path=/_proxy/; Max-Age=28800; HttpOnly; SameSite=Strict"),
        )
    }

    async fn change_caller(&self, req: Request, method: Method) -> Response {
        if method == Method::DELETE {
            let input: DeleteCaller = match read_json(req).await {
                Ok(input) => input,
                Err(response) => return response,
            };
            let Ok(mut state) = self.state.lock() else {
                return unavailable();
            };
            if state
                .store
                .as_ref()
                .is_some_and(|store| store.revoke(&input.id).is_err())
            {
                return unavailable();
            }
            if let Some(stored) = state.callers.get_mut(&input.id) {
                stored.caller.revoked = true;
            }
            return reply(200, json!({"ok": true}));
        }
        let input: CallerInput = match read_json(req).await {
            Ok(input) => input,
            Err(response) => return response,
        };
        let name = input.name.trim();
        let grants = input.grants.unwrap_or_default();
        if grants.len() > 128 || (method == Method::POST && (name.is_empty() || name.len() > 80)) {
            return fail(
                400,
                "Enter a client name (1–80 characters) and at most 128 explicit route grants",
            );
        }
        if input
            .revision
            .is_some_and(|revision| revision != self.revision)
        {
            return fail(
                409,
                "Route configuration changed; refresh before granting access",
            );
        }
        let Ok(mut state) = self.state.lock() else {
            return unavailable();
        };
        if method == Method::PUT {
            let Some(stored) = state.callers.get(&input.id) else {
                return fail(
                    400,
                    "client no longer exists; refresh before editing grants",
                );
            };
            if stored.caller.revoked {
                return fail(
                    400,
                    "revoked clients cannot receive new grants; create a new client",
                );
            }
        }
        let mut seen = HashSet::new();
        for grant in &grants {
            if !slug(grant) || !seen.insert(grant) {
                return fail(400, "route grants must contain unique valid route names");
            }
            if !self.routes.contains(grant) {
                return fail(
                    400,
                    "route grant is not in the active configuration; refresh before saving",
                );
            }
        }
        if method == Method::PUT {
            if state
                .store
                .as_ref()
                .is_some_and(|store| store.update_grants(&input.id, &grants).is_err())
            {
                return unavailable();
            }
            if let Some(stored) = state.callers.get_mut(&input.id) {
                stored.caller.grants = grants;
                return reply(200, json!({"ok": true}));
            }
            return unavailable();
        }
        // Conservative bounded memory; unlike Go there is no retained-history purge.
        if state.callers.len() >= 4096
            || state.callers.values().filter(|c| !c.caller.revoked).count() >= 1024
        {
            return fail(409, "Prototype client limit reached");
        }
        let (Ok(id), Ok(token)) = (random_token("c_"), random_token("sk-tlp-")) else {
            return unavailable();
        };
        let id = id[..26].to_owned();
        if state.callers.contains_key(&id) {
            return unavailable();
        }
        let caller = Caller {
            id: id.clone(),
            name: name.to_owned(),
            created: SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap_or_default()
                .as_millis() as u64,
            last_used: 0,
            revoked: false,
            grants,
            created_by: String::new(),
            external_id: String::new(),
        };
        let token_hash = hash(&token);
        if state.store.as_ref().is_some_and(|store| {
            store
                .insert(
                    &id,
                    &caller.name,
                    caller.created,
                    &token_hash,
                    &caller.grants,
                )
                .is_err()
        }) {
            return unavailable();
        }
        let response = reply(201, json!({"caller": caller, "token": token}));
        state.callers.insert(
            id,
            StoredCaller {
                caller,
                hash: token_hash,
            },
        );
        response
    }
}

fn hash(token: &str) -> [u8; 32] {
    Sha256::digest(token.as_bytes()).into()
}

fn random_token(prefix: &str) -> io::Result<String> {
    let mut bytes = [0u8; 32];
    File::open("/dev/urandom")?.read_exact(&mut bytes)?;
    let mut token = String::with_capacity(prefix.len() + 64);
    token.push_str(prefix);
    const HEX: &[u8; 16] = b"0123456789abcdef";
    for byte in bytes {
        token.push(HEX[(byte >> 4) as usize] as char);
        token.push(HEX[(byte & 15) as usize] as char);
    }
    Ok(token)
}

fn slug(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 64
        && value.as_bytes()[0].is_ascii_alphanumeric()
        && value
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b == b'_' || b == b'-')
}

fn single_header<'a>(headers: &'a HeaderMap, name: &str) -> Option<&'a str> {
    let mut values = headers.get_all(name).iter();
    let value = values.next()?.to_str().ok()?;
    if values.next().is_some() {
        return None;
    }
    Some(value)
}

fn trusted_origin(headers: &HeaderMap, bound: SocketAddr) -> Option<String> {
    if !bound.ip().is_loopback() {
        return None;
    }
    let host = single_header(headers, "host")?;
    let authority: axum::http::uri::Authority = host.parse().ok()?;
    let port = authority.port_u16().unwrap_or(80);
    if port != bound.port() {
        return None;
    }
    let name = authority.host();
    let ip = name
        .trim_start_matches('[')
        .trim_end_matches(']')
        .parse::<IpAddr>();
    if !name.eq_ignore_ascii_case("localhost") && ip.ok() != Some(bound.ip()) {
        return None;
    }
    // Authority parsing alone allows userinfo; whitelist the exact representation.
    let canonical = if name.eq_ignore_ascii_case("localhost") {
        "localhost".to_owned()
    } else {
        match bound.ip() {
            IpAddr::V4(ip) => ip.to_string(),
            IpAddr::V6(ip) => format!("[{ip}]"),
        }
    };
    let expected = if authority.port().is_some() {
        format!("{canonical}:{}", bound.port())
    } else {
        canonical
    };
    if !host.eq_ignore_ascii_case(&expected) {
        return None;
    }
    Some(format!("http://{host}"))
}

fn session_cookie(headers: &HeaderMap) -> Option<String> {
    let mut found = None;
    for header in headers.get_all(header::COOKIE) {
        for pair in header.to_str().ok()?.split(';') {
            let Some((name, value)) = pair.trim().split_once('=') else {
                continue;
            };
            if name == "tlp_admin" {
                if found.is_some()
                    || value.len() != 70
                    || !value.starts_with("admin_")
                    || !value[6..].bytes().all(|b| b.is_ascii_hexdigit())
                {
                    return None;
                }
                found = Some(value.to_owned());
            }
        }
    }
    found
}

async fn read_json<T: DeserializeOwned>(req: Request) -> Result<T, Response> {
    if !single_header(req.headers(), "content-type")
        .is_some_and(|value| value.starts_with("application/json"))
    {
        return Err(fail(415, "Use application/json"));
    }
    let bytes = match tokio::time::timeout(
        Duration::from_secs(15),
        to_bytes(req.into_body(), BODY_LIMIT),
    )
    .await
    {
        Ok(Ok(bytes)) => bytes,
        _ => return Err(fail(400, "Invalid JSON fields or body size")),
    };
    let mut decoder = serde_json::Deserializer::from_slice(&bytes);
    let value =
        T::deserialize(&mut decoder).map_err(|_| fail(400, "Invalid JSON fields or body size"))?;
    decoder
        .end()
        .map_err(|_| fail(400, "Expected one JSON value"))?;
    Ok(value)
}

fn reply(status: u16, value: Value) -> Response {
    (
        StatusCode::from_u16(status).unwrap_or(StatusCode::INTERNAL_SERVER_ERROR),
        [(header::CONTENT_TYPE, "application/json")],
        format!("{value}\n"),
    )
        .into_response()
}

fn fail(status: u16, message: &str) -> Response {
    reply(status, json!({"error": message}))
}
fn unavailable() -> Response {
    fail(
        503,
        "Administration state unavailable; existing access is unchanged",
    )
}

fn with_cookie(mut response: Response, cookie: &str) -> Response {
    match HeaderValue::from_str(cookie) {
        Ok(value) => {
            response.headers_mut().insert(header::SET_COOKIE, value);
            response
        }
        Err(_) => unavailable(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::body::Body;

    const TOKEN: &str = "synthetic-admin-credential-only";
    const ORIGIN: &str = "http://127.0.0.1:8089";

    fn admin() -> Admin {
        Admin::new(TOKEN, vec!["alpha".into(), "beta".into()]).unwrap()
    }

    fn bound() -> SocketAddr {
        "127.0.0.1:8089".parse().unwrap()
    }

    fn request(
        method: &str,
        path: &str,
        body: Value,
        session: Option<&(String, String)>,
    ) -> Request {
        let mut req = Request::builder()
            .method(method)
            .uri(format!("/_proxy/api/{path}"))
            .header("host", "127.0.0.1:8089")
            .header("origin", ORIGIN)
            .header("content-type", "application/json");
        if let Some((cookie, csrf)) = session {
            req = req.header("cookie", cookie).header("x-csrf-token", csrf);
        }
        req.body(Body::from(body.to_string())).unwrap()
    }

    async fn value(response: Response) -> Value {
        serde_json::from_slice(&to_bytes(response.into_body(), BODY_LIMIT).await.unwrap()).unwrap()
    }

    async fn login(admin: &Admin) -> (String, String) {
        let response = admin
            .response(
                request("POST", "login", json!({"token": TOKEN}), None),
                bound(),
            )
            .await;
        assert_eq!(response.status(), 200);
        let cookie = response.headers()[header::SET_COOKIE]
            .to_str()
            .unwrap()
            .to_owned();
        assert!(cookie.contains("HttpOnly; SameSite=Strict"));
        let cookie = cookie.split(';').next().unwrap().to_owned();
        (
            cookie,
            value(response).await["csrf"].as_str().unwrap().to_owned(),
        )
    }

    #[tokio::test]
    async fn caller_scope_edit_revoke_and_logout() {
        let admin = admin();
        let session = login(&admin).await;
        let unscoped = admin
            .response(
                request(
                    "POST",
                    "callers",
                    json!({"name": "No grants"}),
                    Some(&session),
                ),
                bound(),
            )
            .await;
        assert_eq!(unscoped.status(), 201);
        let unscoped = value(unscoped).await;
        let unscoped_token = unscoped["token"].as_str().unwrap();
        assert!(!admin.authorize(unscoped_token, "alpha"));
        assert!(!admin.authorize(unscoped_token, "beta"));
        let response = admin
            .response(
                request(
                    "POST",
                    "callers",
                    json!({"name": " Test ", "grants": ["alpha"]}),
                    Some(&session),
                ),
                bound(),
            )
            .await;
        assert_eq!(response.status(), 201);
        let issued = value(response).await;
        let token = issued["token"].as_str().unwrap();
        let id = issued["caller"]["id"].as_str().unwrap();
        assert_eq!(issued["caller"]["name"], "Test");
        assert!(admin.authorize(token, "alpha"));
        assert!(!admin.authorize(token, "beta"));
        assert!(!admin.authorize(TOKEN, "alpha"));
        assert!(!admin.authorize(&format!("Bearer {token}"), "alpha"));
        let response = admin
            .response(
                request(
                    "PUT",
                    "callers",
                    json!({"id": id, "grants": ["beta"]}),
                    Some(&session),
                ),
                bound(),
            )
            .await;
        assert_eq!(response.status(), 200);
        assert!(!admin.authorize(token, "alpha"));
        assert!(admin.authorize(token, "beta"));
        let response = admin
            .response(
                request("DELETE", "callers", json!({"id": id}), Some(&session)),
                bound(),
            )
            .await;
        assert_eq!(response.status(), 200);
        assert!(!admin.authorize(token, "beta"));
        assert_eq!(
            admin
                .response(
                    request(
                        "PUT",
                        "callers",
                        json!({"id": id, "grants": ["alpha"]}),
                        Some(&session)
                    ),
                    bound()
                )
                .await
                .status(),
            400
        );
        assert_eq!(
            admin
                .response(
                    request("POST", "logout", json!({}), Some(&session)),
                    bound()
                )
                .await
                .status(),
            200
        );
        assert_eq!(
            admin
                .response(
                    request("GET", "unknown", json!({}), Some(&session)),
                    bound()
                )
                .await
                .status(),
            401
        );
    }

    #[tokio::test]
    async fn status_is_public_but_no_fake_ui_or_authenticated_endpoint() {
        let admin = admin();
        let response = admin
            .response(request("GET", "auth/status", json!({}), None), bound())
            .await;
        assert_eq!(response.status(), 200);
        assert_eq!(response.headers()[header::CACHE_CONTROL], "no-store");
        assert_eq!(
            value(response).await,
            json!({"auth": "token", "csrf": "", "origin": ""})
        );
        assert_eq!(
            admin
                .response(request("GET", "state", json!({}), None), bound())
                .await
                .status(),
            401
        );
        let session = login(&admin).await;
        for path in ["auth/login/begin", "callers"] {
            assert_eq!(
                admin
                    .response(request("GET", path, json!({}), Some(&session)), bound())
                    .await
                    .status(),
                404
            );
        }
    }

    #[tokio::test]
    async fn host_origin_csrf_and_duplicate_headers_fail_closed() {
        let admin = admin();
        let session = login(&admin).await;
        for (name, bad) in [
            ("host", "evil.test:8089"),
            ("host", "127.0.0.1:9999"),
            ("origin", "http://evil.test"),
            ("x-csrf-token", "wrong"),
        ] {
            let mut req = request("POST", "callers", json!({"name": "Denied"}), Some(&session));
            req.headers_mut()
                .insert(name, HeaderValue::from_static(bad));
            assert_eq!(admin.response(req, bound()).await.status(), 403);
        }
        for name in ["host", "origin", "x-csrf-token"] {
            let mut req = request("POST", "callers", json!({"name": "Denied"}), Some(&session));
            let duplicate = req.headers()[name].clone();
            req.headers_mut().append(name, duplicate);
            assert_eq!(admin.response(req, bound()).await.status(), 403);
        }
        for name in ["origin", "x-csrf-token"] {
            let mut req = request("POST", "logout", json!({}), Some(&session));
            req.headers_mut().remove(name);
            assert_eq!(admin.response(req, bound()).await.status(), 403);
        }
        let mut req = request("POST", "logout", json!({}), Some(&session));
        req.headers_mut()
            .append("cookie", HeaderValue::from_str(&session.0).unwrap());
        assert_eq!(admin.response(req, bound()).await.status(), 401);
    }

    #[tokio::test]
    async fn invalid_grants_do_not_replace_existing_scope() {
        let admin = admin();
        let session = login(&admin).await;
        let issued = value(
            admin
                .response(
                    request(
                        "POST",
                        "callers",
                        json!({"name": "test", "grants": ["alpha"]}),
                        Some(&session),
                    ),
                    bound(),
                )
                .await,
        )
        .await;
        let id = &issued["caller"]["id"];
        let token = issued["token"].as_str().unwrap();
        for grants in [json!(["unknown"]), json!(["beta", "beta"]), json!(["*"])] {
            assert_eq!(
                admin
                    .response(
                        request(
                            "PUT",
                            "callers",
                            json!({"id": id, "grants": grants}),
                            Some(&session)
                        ),
                        bound()
                    )
                    .await
                    .status(),
                400
            );
            assert!(admin.authorize(token, "alpha"));
        }
        assert_eq!(
            admin
                .response(
                    request(
                        "PUT",
                        "callers",
                        json!({"id": id, "grants": [], "revision": 2}),
                        Some(&session)
                    ),
                    bound()
                )
                .await
                .status(),
            409
        );
        assert!(admin.authorize(token, "alpha"));
        assert_eq!(
            admin
                .response(
                    request(
                        "PUT",
                        "callers",
                        json!({"id": id, "grants": [], "revision": 1}),
                        Some(&session)
                    ),
                    bound()
                )
                .await
                .status(),
            200
        );
        assert!(!admin.authorize(token, "alpha"));
    }

    #[tokio::test]
    async fn login_throttles_bad_guesses_not_valid_credentials() {
        let admin = admin();
        for _ in 0..8 {
            assert_eq!(
                admin
                    .response(
                        request("POST", "login", json!({"token": "wrong"}), None),
                        bound()
                    )
                    .await
                    .status(),
                401
            );
        }
        let response = admin
            .response(
                request("POST", "login", json!({"token": "wrong"}), None),
                bound(),
            )
            .await;
        assert_eq!(response.status(), 429);
        assert_eq!(response.headers()[header::RETRY_AFTER], "60");
        login(&admin).await;
        assert_eq!(
            admin
                .response(
                    request("POST", "login", json!({"token": "wrong"}), None),
                    bound()
                )
                .await
                .status(),
            401
        );
    }

    #[tokio::test]
    async fn strict_json_and_body_limits() {
        let admin = admin();
        for body in [
            r#"{"token":"x","extra":true}"#,
            r#"{"token":"x"} {}"#,
            r#"{"token":"x","token":"y"}"#,
        ] {
            let mut req = request("POST", "login", json!({}), None);
            *req.body_mut() = Body::from(body);
            assert_eq!(admin.response(req, bound()).await.status(), 400);
        }
        let mut req = request("POST", "login", json!({"token": TOKEN}), None);
        req.headers_mut().remove("content-type");
        assert_eq!(admin.response(req, bound()).await.status(), 415);
        let mut req = request("POST", "login", json!({}), None);
        *req.body_mut() = Body::from(vec![b' '; BODY_LIMIT + 1]);
        assert_eq!(admin.response(req, bound()).await.status(), 400);
    }

    #[tokio::test]
    async fn expired_session_is_rejected_and_session_count_bounded() {
        let admin = admin();
        let session = login(&admin).await;
        {
            let mut state = admin.state.lock().unwrap();
            for session in state.sessions.values_mut() {
                session.expires = Instant::now() - Duration::from_secs(1);
            }
        }
        assert_eq!(
            admin
                .response(
                    request("POST", "logout", json!({}), Some(&session)),
                    bound()
                )
                .await
                .status(),
            401
        );
        for _ in 0..64 {
            login(&admin).await;
        }
        assert_eq!(
            admin
                .response(
                    request("POST", "login", json!({"token": TOKEN}), None),
                    bound()
                )
                .await
                .status(),
            429
        );
    }

    #[tokio::test]
    async fn go_error_bytes_origin_semantics_and_state_subset() {
        let admin = admin();
        assert!(admin.is_admin_token(TOKEN));
        assert!(!admin.is_admin_token("not-admin"));
        assert!(!admin.is_admin_token(&format!("Bearer {TOKEN}")));
        let denied = admin
            .response(request("GET", "state", json!({}), None), bound())
            .await;
        assert_eq!(denied.status(), 401);
        assert_eq!(
            to_bytes(denied.into_body(), BODY_LIMIT)
                .await
                .unwrap()
                .as_ref(),
            b"{\"error\":\"Sign in to administer this proxy\"}\n"
        );
        let mut req = request("POST", "login", json!({"token": TOKEN}), None);
        req.headers_mut()
            .insert("origin", HeaderValue::from_static(""));
        let denied = admin.response(req, bound()).await;
        assert_eq!(denied.status(), 403);
        assert_eq!(
            to_bytes(denied.into_body(), BODY_LIMIT)
                .await
                .unwrap()
                .as_ref(),
            b"{\"error\":\"A matching Origin is required\"}\n"
        );
        let mut req = request("GET", "auth/status", json!({}), None);
        req.headers_mut()
            .insert("origin", HeaderValue::from_static(""));
        assert_eq!(admin.response(req, bound()).await.status(), 200);
        let session = login(&admin).await;
        let state = admin
            .response(request("GET", "state", json!({}), Some(&session)), bound())
            .await;
        assert_eq!(state.status(), 200);
        assert_eq!(
            value(state).await,
            json!({"mode": "file", "auth": "token", "revision": 1,
            "csrf": session.1, "callers": []})
        );
        let issued = admin
            .response(
                request(
                    "POST",
                    "callers",
                    json!({"name": "test", "grants": ["beta", "alpha"], "revision": 1}),
                    Some(&session),
                ),
                bound(),
            )
            .await;
        assert_eq!(issued.status(), 201);
        let issued = value(issued).await;
        let state = value(
            admin
                .response(request("GET", "state", json!({}), Some(&session)), bound())
                .await,
        )
        .await;
        assert_eq!(state["callers"][0]["id"], issued["caller"]["id"]);
        assert_eq!(state["callers"][0]["grants"], json!(["alpha", "beta"]));
        assert!(state.get("routes").is_none());
        assert!(state.get("has_requests").is_none());
        let stale = admin
            .response(
                request(
                    "PUT",
                    "callers",
                    json!({"id": issued["caller"]["id"], "grants": [], "revision": 0}),
                    Some(&session),
                ),
                bound(),
            )
            .await;
        assert_eq!(stale.status(), 409);
        assert_eq!(
            to_bytes(stale.into_body(), BODY_LIMIT)
                .await
                .unwrap()
                .as_ref(),
            b"{\"error\":\"Route configuration changed; refresh before granting access\"}\n"
        );
        let ok = reply(200, json!({"ok": true}));
        assert_eq!(
            to_bytes(ok.into_body(), BODY_LIMIT).await.unwrap().as_ref(),
            b"{\"ok\":true}\n"
        );
    }

    #[test]
    fn trust_is_bound_and_startup_validated() {
        assert!(Admin::new("", vec![]).is_err());
        assert!(Admin::new(TOKEN, vec!["*".into()]).is_err());
        assert!(Admin::new(TOKEN, vec!["a".into(), "a".into()]).is_err());
        let mut headers = HeaderMap::new();
        for host in ["localhost:8089", "127.0.0.1:8089"] {
            headers.insert("host", HeaderValue::from_str(host).unwrap());
            assert!(trusted_origin(&headers, bound()).is_some());
        }
        for host in [
            "localhost",
            "localhost:9999",
            "evil.test:8089",
            "evil@localhost:8089",
            "127.0.0.2:8089",
            "localhost:08089",
        ] {
            headers.insert("host", HeaderValue::from_str(host).unwrap());
            assert!(trusted_origin(&headers, bound()).is_none(), "{host}");
        }
        headers.insert("host", HeaderValue::from_static("[::1]:8089"));
        assert!(trusted_origin(&headers, "[::1]:8089".parse().unwrap()).is_some());
        assert!(trusted_origin(&headers, "0.0.0.0:8089".parse().unwrap()).is_none());
    }
}
