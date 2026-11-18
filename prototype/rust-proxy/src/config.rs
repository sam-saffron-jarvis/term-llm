//! Operator-file compilation for the loopback-only prototype, not release parity.
//! Unsupported Go capabilities are rejected rather than deserialized and ignored.

use axum::http::{HeaderMap, HeaderName, HeaderValue, Method};
use reqwest::Url;
use serde::{Deserialize, Deserializer, Serialize, de};
use sha2::{Digest, Sha256};
use std::collections::{BTreeMap, HashSet};
use std::fs::{self, File, Metadata, OpenOptions};
use std::io::{self, Read};
use std::os::unix::fs::{MetadataExt, OpenOptionsExt};
use std::path::{Component, Path, PathBuf};

const CONFIG_LIMIT: u64 = 1 << 20;
const SECRET_LIMIT: u64 = 16 << 10;

pub(crate) struct Route {
    pub(crate) name: String,
    pub(crate) authority: String,
    pub(crate) enabled: bool,
    pub(crate) prefix: String,
    pub(crate) upstream: Url,
    pub(crate) caller_header: HeaderName,
    pub(crate) caller_prefix: String,
    pub(crate) credentials: HeaderMap,
    pub(crate) refuse_redirects: bool,
    allow: Vec<(Method, String)>,
}

impl Route {
    /// Go allow rules are method-specific string prefixes, not exact endpoints
    /// or segment-boundary matches. No rules means unrestricted method/path.
    pub(crate) fn permits(&self, method: &Method, suffix: &str) -> bool {
        self.allow.is_empty()
            || (policy_path(suffix)
                && self
                    .allow
                    .iter()
                    .any(|(m, p)| m == method && suffix.starts_with(p)))
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Policy {
    #[serde(default)]
    routes: Vec<RouteSpec>,
    #[serde(default, deserialize_with = "unique_map")]
    secrets: BTreeMap<String, SecretSpec>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct SecretSpec {
    file: PathBuf,
    origin: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RouteSpec {
    name: String,
    local_prefix: String,
    upstream: String,
    #[serde(default = "enabled_default")]
    enabled: bool,
    #[serde(default)]
    allow_insecure_http: bool,
    #[serde(default)]
    caller_auth: CallerAuth,
    #[serde(default, deserialize_with = "unique_map")]
    set_headers: BTreeMap<String, HeaderRule>,
    #[serde(default)]
    allow: Vec<String>,
    #[serde(default)]
    redirects: String,
}

fn enabled_default() -> bool {
    true
}

#[derive(Default, Deserialize)]
#[serde(deny_unknown_fields)]
struct CallerAuth {
    #[serde(default)]
    header: String,
    #[serde(default)]
    prefix: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct HeaderRule {
    #[serde(default)]
    secret: String,
    #[serde(default)]
    prefix: String,
    value: Option<String>,
}

// serde's ordinary map collection overwrites duplicate YAML/JSON keys. Operator
// credentials must never depend on which duplicate happens to be read last.
fn unique_map<'de, D, V>(deserializer: D) -> Result<BTreeMap<String, V>, D::Error>
where
    D: Deserializer<'de>,
    V: Deserialize<'de>,
{
    struct Visitor<V>(std::marker::PhantomData<V>);
    impl<'de, V: Deserialize<'de>> de::Visitor<'de> for Visitor<V> {
        type Value = BTreeMap<String, V>;
        fn expecting(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
            f.write_str("a map with unique keys")
        }
        fn visit_map<A: de::MapAccess<'de>>(self, mut map: A) -> Result<Self::Value, A::Error> {
            let mut result = BTreeMap::new();
            while let Some((key, value)) = map.next_entry::<String, V>()? {
                if result.insert(key, value).is_some() {
                    return Err(de::Error::custom("duplicate mapping key"));
                }
            }
            Ok(result)
        }
    }
    deserializer.deserialize_map(Visitor(std::marker::PhantomData))
}

fn bad(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidInput, message)
}

fn slug(name: &str) -> bool {
    !name.is_empty()
        && name.len() <= 64
        && name.as_bytes()[0].is_ascii_alphanumeric()
        && name
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'_' | b'-'))
}

/// Go safePath validation for raw escaped incoming paths. This deliberately
/// does not impose allow-policy canonicalization: ordinary escaped identifiers
/// and repeated slashes are valid unless a configured allow policy rejects them.
pub(crate) fn safe_path(path: &str) -> bool {
    if path.len() > 8192 || !path.starts_with('/') {
        return false;
    }
    let mut current = path.as_bytes().to_vec();
    for pass in 0..4 {
        if current.iter().any(|b| *b == b'\\' || *b < 32 || *b == 127)
            || current
                .split(|b| *b == b'/')
                .any(|part| part == b"." || part == b"..")
        {
            return false;
        }
        let mut decoded = Vec::with_capacity(current.len());
        let mut index = 0;
        while index < current.len() {
            if current[index] != b'%' {
                decoded.push(current[index]);
                index += 1;
                continue;
            }
            let hex = |b: u8| match b {
                b'0'..=b'9' => Some(b - b'0'),
                b'a'..=b'f' => Some(b - b'a' + 10),
                b'A'..=b'F' => Some(b - b'A' + 10),
                _ => None,
            };
            let pair = current
                .get(index + 1)
                .and_then(|a| hex(*a))
                .zip(current.get(index + 2).and_then(|b| hex(*b)));
            let Some((high, low)) = pair else {
                // A percent produced by an earlier decode is a valid identifier.
                return pass > 0;
            };
            decoded.push((high << 4) | low);
            index += 3;
        }
        if decoded == current {
            return true;
        }
        current = decoded;
    }
    false
}

// Policy paths have the Go canonicalPolicyPath behavior: no escaped paths,
// controls, backslashes, query, fragment, spaces, empty interior or dot segments.
fn policy_path(path: &str) -> bool {
    path.len() <= 8192
        && path.starts_with('/')
        && !path.contains("//")
        && !path
            .chars()
            .any(|c| c <= ' ' || c == '\u{7f}' || matches!(c, '\\' | '%' | '?' | '#'))
        && path.split('/').all(|part| part != "." && part != "..")
}

// URL parsers normalize dot segments and backslashes before exposing path().
// Limit the raw base/mount spelling to the original prototype's plain paths;
// reject unsupported encodings rather than normalize credential destinations.
fn plain_path(path: &str) -> bool {
    policy_path(path)
        && path
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'/' | b'_' | b'-' | b'.' | b'~'))
}

fn valid_header(raw: &str) -> io::Result<HeaderName> {
    let name = HeaderName::from_bytes(raw.as_bytes()).map_err(|_| bad("invalid header name"))?;
    if matches!(
        name.as_str(),
        "host"
            | "connection"
            | "proxy-connection"
            | "keep-alive"
            | "proxy-authenticate"
            | "proxy-authorization"
            | "te"
            | "trailer"
            | "transfer-encoding"
            | "upgrade"
            | "content-length"
            | "forwarded"
            | "x-forwarded-for"
            | "x-forwarded-host"
            | "x-forwarded-proto"
            | "x-real-ip"
            | "x-original-url"
            | "x-rewrite-url"
            | "cookie"
    ) || name.as_str().starts_with("x-term-llm-proxy-")
    {
        return Err(bad("forbidden proxy header"));
    }
    Ok(name)
}

fn header_value(raw: &str) -> io::Result<HeaderValue> {
    if raw.len() > SECRET_LIMIT as usize || raw.bytes().any(|b| (b < 32 && b != b'\t') || b == 127)
    {
        return Err(bad("invalid substitution header value"));
    }
    HeaderValue::from_str(raw).map_err(|_| bad("invalid substitution header value"))
}

// Deliberately narrower than Go Origin: only explicit numeric IPv4 loopback
// endpoints are implemented by this mock stage. Include the effective port in
// bindings even when Url elides the default :80 in its serialized form.
fn mock_url(raw: &str, origin_only: bool) -> io::Result<(Url, String)> {
    let rest = raw
        .strip_prefix("http://127.0.0.1:")
        .ok_or_else(|| bad("only explicit http://127.0.0.1:<port> mock origins are supported"))?;
    let (port, tail) = rest.split_once('/').unwrap_or((rest, ""));
    if port.is_empty() || !port.bytes().all(|b| b.is_ascii_digit()) {
        return Err(bad("mock origin requires a numeric port"));
    }
    let port: u16 = port.parse().map_err(|_| bad("invalid mock origin port"))?;
    if port == 0 {
        return Err(bad("invalid mock origin port"));
    }
    if origin_only && !tail.is_empty() {
        return Err(bad("secret origin cannot contain a base path"));
    }
    let raw_path = format!("/{tail}");
    if !plain_path(&raw_path) {
        return Err(bad("unsupported or noncanonical upstream path"));
    }
    let url = Url::parse(raw).map_err(|_| bad("invalid mock upstream URL"))?;
    if url.host_str() != Some("127.0.0.1")
        || url.port_or_known_default() != Some(port)
        || !url.username().is_empty()
        || url.password().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
    {
        return Err(bad("invalid mock upstream URL"));
    }
    Ok((url, format!("http://127.0.0.1:{port}")))
}

fn open_regular(path: &Path, private: bool, limit: u64) -> io::Result<(File, Metadata)> {
    // Never block on FIFOs or follow a final-component symlink. Check and read
    // the same descriptor, including when protecting inode aliases below.
    let file = OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_NONBLOCK)
        .open(path)
        .map_err(|_| bad("cannot open regular configuration or credential file"))?;
    let meta = file.metadata().map_err(|_| bad("cannot inspect file"))?;
    if !meta.is_file() || meta.len() > limit || (private && meta.mode() & 0o077 != 0) {
        return Err(bad(
            "file must be regular, within size limit, and private if a credential",
        ));
    }
    Ok((file, meta))
}

fn read_bounded(file: File, limit: u64) -> io::Result<Vec<u8>> {
    let bound = limit
        .checked_add(1)
        .ok_or_else(|| bad("invalid file size limit"))?;
    let mut bytes = Vec::new();
    file.take(bound)
        .read_to_end(&mut bytes)
        .map_err(|_| bad("cannot read file"))?;
    if bytes.len() as u64 > limit {
        return Err(bad("file exceeds size limit"));
    }
    Ok(bytes)
}

fn secret_text(file: File, limit: u64) -> io::Result<String> {
    let text = String::from_utf8(read_bounded(file, limit)?)
        .map_err(|_| bad("credential file must be UTF-8"))?;
    let text = text.trim_end_matches(['\r', '\n']);
    if text.is_empty() || text.bytes().any(|b| (b < 32 && b != b'\t') || b == 127) {
        return Err(bad(
            "credential file must contain a nonempty single-line value",
        ));
    }
    Ok(text.to_owned())
}

/// Read an owner-only regular file with a hard byte bound (before CR/LF trim).
/// Relative paths use the process cwd, as in Go operator files. No environment
/// expansion or config-directory rebasing is performed. Errors contain no data.
pub(crate) fn read_secret(path: &Path, limit: u64) -> io::Result<String> {
    let (file, _) = open_regular(path, true, limit)?;
    secret_text(file, limit)
}

fn absolute_clean(path: &Path) -> io::Result<PathBuf> {
    let path = if path.is_absolute() {
        path.to_owned()
    } else {
        std::env::current_dir()?.join(path)
    };
    let mut result = PathBuf::new();
    for component in path.components() {
        match component {
            Component::ParentDir => {
                result.pop();
            }
            Component::CurDir => {}
            other => result.push(other.as_os_str()),
        }
    }
    Ok(result)
}

fn check_protected(path: &Path, meta: &Metadata, protected: &[&Path]) -> io::Result<()> {
    let absolute = absolute_clean(path)?;
    let resolved = fs::canonicalize(path).map_err(|_| bad("cannot resolve credential file"))?;
    for candidate in protected.iter().filter(|p| !p.as_os_str().is_empty()) {
        let other = absolute_clean(candidate)?;
        if absolute.starts_with(&other) || resolved.starts_with(&other) {
            return Err(bad("credential file is protected"));
        }
        if let Ok(info) = fs::metadata(candidate) {
            if meta.dev() == info.dev() && meta.ino() == info.ino() {
                return Err(bad("credential file aliases a protected file"));
            }
            if info.is_dir() {
                let directory = fs::canonicalize(candidate)
                    .map_err(|_| bad("cannot resolve protected directory"))?;
                if resolved.starts_with(directory) {
                    return Err(bad("credential file is in a protected directory"));
                }
            }
        }
    }
    Ok(())
}

// Match the pinned Go encoding exactly, including its JSON HTML escaping.
// Authorities bind grants to what is sent upstream, not to the mutable secret bytes.
fn authority(spec: &RouteSpec, origin: &str, upstream: &Url) -> io::Result<String> {
    #[derive(Serialize)]
    #[serde(rename_all = "PascalCase")]
    struct Rule<'a> {
        header: String,
        secret: &'a str,
        prefix: &'a str,
        value: Option<&'a str>,
    }
    #[derive(Serialize)]
    #[serde(rename_all = "PascalCase")]
    struct Scope<'a> {
        origin: &'a str,
        base: String,
        rules: Vec<Rule<'a>>,
        #[serde(skip_serializing_if = "Vec::is_empty")]
        allow: Vec<&'a str>,
    }
    let mut rules: Vec<_> = spec
        .set_headers
        .iter()
        .map(|(header, rule)| Rule {
            header: header.to_ascii_lowercase(),
            secret: &rule.secret,
            prefix: &rule.prefix,
            value: rule.value.as_deref(),
        })
        .collect();
    rules.sort_unstable_by(|a, b| a.header.cmp(&b.header));
    let mut allow: Vec<_> = spec.allow.iter().map(String::as_str).collect();
    allow.sort_unstable();
    let base = format!(
        "{}/",
        upstream.path().strip_suffix('/').unwrap_or(upstream.path())
    );
    let wire = serde_json::to_string(&Scope {
        origin,
        base,
        rules,
        allow,
    })
    .map_err(|_| bad("cannot encode proxy route authority"))?
    .replace('<', "\\u003c")
    .replace('>', "\\u003e")
    .replace('&', "\\u0026")
    .replace('\u{2028}', "\\u2028")
    .replace('\u{2029}', "\\u2029");
    Ok(format!("{:x}", Sha256::digest(wire.as_bytes())))
}

/// Compile only fixed, explicit, origin-bound file credentials. Accounts, stored
/// secrets, money, subpath rules, WebSocket, billing, usage and request ID
/// configuration are intentionally absent from the schema and rejected even
/// on disabled routes. Disabled routes retain their authority but are never dispatched.
pub(crate) fn load_routes(path: &Path, protected: &[&Path]) -> io::Result<Vec<Route>> {
    let (file, _) = open_regular(path, false, CONFIG_LIMIT)?;
    let policy: Policy = serde_yaml_ng::from_slice(&read_bounded(file, CONFIG_LIMIT)?)
        .map_err(|_| bad("invalid operator config: unknown/unsupported field, duplicate key, type or document"))?;
    if policy.routes.len() > 128 || policy.secrets.len() > 256 {
        return Err(bad("at most 128 routes and 256 secret references"));
    }
    let mut secrets = BTreeMap::new();
    for (name, spec) in policy.secrets {
        if !slug(&name) || spec.file.as_os_str().is_empty() {
            return Err(bad("invalid secret name or file path"));
        }
        let (_, origin) = mock_url(&spec.origin, true)?;
        let (file, meta) = open_regular(&spec.file, true, SECRET_LIMIT)?;
        check_protected(&spec.file, &meta, protected)?;
        secrets.insert(name, (origin, secret_text(file, SECRET_LIMIT)?));
    }
    let mut names = HashSet::new();
    let mut prefixes: Vec<String> = Vec::new();
    let mut routes = Vec::new();
    for spec in policy.routes {
        if !slug(&spec.name) || !names.insert(spec.name.clone()) {
            return Err(bad(
                "route names must be unique slugs of at most 64 characters",
            ));
        }
        let prefix = &spec.local_prefix;
        if !plain_path(prefix)
            || prefix == "/"
            || !prefix.ends_with('/')
            || ["/_proxy/", "/healthz/", "/assets/"]
                .iter()
                .any(|p| prefix.starts_with(*p) || p.starts_with(prefix.as_str()))
            || prefixes
                .iter()
                .any(|p| prefix.starts_with(p) || p.starts_with(prefix.as_str()))
        {
            return Err(bad(
                "route prefixes must be canonical, non-root, nonreserved and nonoverlapping",
            ));
        }
        prefixes.push(prefix.clone());
        if !spec.allow_insecure_http {
            return Err(bad("mock HTTP upstream requires allow_insecure_http"));
        }
        let refuse_redirects = match spec.redirects.as_str() {
            "" | "passthrough" => false,
            "refuse" => true,
            _ => return Err(bad("redirects must be passthrough or refuse")),
        };
        let (upstream, origin) = mock_url(&spec.upstream, false)?;
        let authority = authority(&spec, &origin, &upstream)?;
        let auth = if spec.caller_auth.header.is_empty() {
            CallerAuth {
                header: "Authorization".to_owned(),
                prefix: "Bearer ".to_owned(),
            }
        } else {
            spec.caller_auth
        };
        let caller_header = valid_header(&auth.header)?;
        header_value(&auth.prefix)?;
        if spec.allow.len() > 128 || spec.set_headers.len() > 32 {
            return Err(bad("at most 128 allow rules and 32 header substitutions"));
        }
        let mut allow = Vec::new();
        let mut seen = HashSet::new();
        for entry in spec.allow {
            let (method, path) = entry
                .split_once(' ')
                .ok_or_else(|| bad("allow rule requires METHOD /prefix"))?;
            let parsed =
                Method::from_bytes(method.as_bytes()).map_err(|_| bad("invalid allow method"))?;
            if method != method.to_ascii_uppercase()
                || parsed == Method::CONNECT
                || !policy_path(path)
                || !seen.insert(entry.clone())
            {
                return Err(bad(
                    "allow rules must be unique canonical METHOD /prefix entries, excluding CONNECT",
                ));
            }
            allow.push((parsed, path.to_owned()));
        }
        let mut credentials = HeaderMap::new();
        for (key, rule) in spec.set_headers {
            let name = valid_header(&key)?;
            if credentials.contains_key(&name) || (!rule.secret.is_empty()) == rule.value.is_some()
            {
                return Err(bad(
                    "header rules need unique names and exactly one secret or literal value",
                ));
            }
            let value = if let Some(value) = rule.value {
                if !rule.prefix.is_empty() {
                    return Err(bad("literal headers cannot have a prefix"));
                }
                value
            } else {
                if !slug(&rule.secret) {
                    return Err(bad("invalid substitution secret reference"));
                }
                let value = match secrets.get(&rule.secret) {
                    Some((bound, value)) if bound == &origin => value.as_str(),
                    Some(_) => {
                        return Err(bad(
                            "substitution secret is not bound to this upstream origin",
                        ));
                    }
                    None if !spec.enabled => "",
                    None => {
                        return Err(bad("header substitution references an unavailable secret"));
                    }
                };
                format!("{}{value}", rule.prefix)
            };
            let mut value = header_value(&value)?;
            value.set_sensitive(true);
            credentials.insert(name, value);
        }
        routes.push(Route {
            name: spec.name,
            authority,
            enabled: spec.enabled,
            prefix: spec.local_prefix,
            upstream,
            caller_header,
            caller_prefix: auth.prefix,
            credentials,
            refuse_redirects,
            allow,
        });
    }
    Ok(routes)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::{PermissionsExt, symlink};
    use std::sync::atomic::{AtomicU64, Ordering};

    static NEXT: AtomicU64 = AtomicU64::new(0);

    struct Fixture(PathBuf);
    impl Fixture {
        fn new() -> Self {
            // The test runner supplies isolated TMPDIR. Never consult user config.
            let root = std::env::temp_dir().join(format!(
                "rust-proxy-config-{}-{}",
                std::process::id(),
                NEXT.fetch_add(1, Ordering::Relaxed)
            ));
            fs::create_dir(&root).unwrap();
            fs::set_permissions(&root, fs::Permissions::from_mode(0o700)).unwrap();
            let fixture = Self(root);
            fixture.write("secret", "synthetic-key\r\n", 0o600);
            fixture
        }
        fn write(&self, name: &str, contents: &str, mode: u32) -> PathBuf {
            let path = self.0.join(name);
            fs::write(&path, contents).unwrap();
            fs::set_permissions(&path, fs::Permissions::from_mode(mode)).unwrap();
            path
        }
        fn config(&self, extra: &str) -> String {
            format!(
                "secrets:\n  key:\n    file: '{}'\n    origin: http://127.0.0.1:12345\nroutes:\n  - name: mock\n    local_prefix: /mock/\n    upstream: http://127.0.0.1:12345/backend\n    allow_insecure_http: true\n    set_headers:\n      Authorization:\n        secret: key\n        prefix: 'Bearer '\n{extra}",
                self.0.join("secret").display()
            )
        }
        fn load(&self, contents: &str) -> io::Result<Vec<Route>> {
            load_routes(&self.write("config.yaml", contents, 0o600), &[])
        }
    }
    impl Drop for Fixture {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.0);
        }
    }

    #[test]
    fn operator_defaults_and_optional_allow() {
        let f = Fixture::new();
        let routes = f.load(&f.config("")).unwrap();
        let route = &routes[0];
        assert_eq!(route.name, "mock");
        assert_eq!(route.caller_header, "authorization");
        assert_eq!(route.caller_prefix, "Bearer ");
        assert_eq!(route.credentials["authorization"], "Bearer synthetic-key");
        assert!(route.credentials["authorization"].is_sensitive());
        assert!(route.permits(&Method::DELETE, "/anything"));
        let disabled = f.load(&f.config("    enabled: false\n")).unwrap();
        assert_eq!(disabled.len(), 1);
        assert!(!disabled[0].enabled);
        assert_eq!(disabled[0].authority, route.authority);
        assert!(f.load("routes: []\n").unwrap().is_empty());
    }

    #[test]
    fn allow_uses_go_method_and_string_prefix_semantics() {
        let f = Fixture::new();
        let routes = f
            .load(&f.config("    allow: ['GET /v1/models', 'DELETE /items/']\n"))
            .unwrap();
        let route = &routes[0];
        assert!(route.permits(&Method::GET, "/v1/models-extra"));
        assert!(route.permits(&Method::DELETE, "/items/42"));
        for suffix in [
            "/v1/%6dodels",
            "/v1/models/../x",
            "/v1/models//x",
            "/v1/models?q=x",
        ] {
            assert!(!route.permits(&Method::GET, suffix));
        }
        assert!(!route.permits(&Method::POST, "/v1/models"));
        for rule in [
            "CONNECT /",
            "get /",
            "GET /a//b",
            "GET /a/../b",
            "GET /%61",
            "GET /a?b",
        ] {
            assert!(
                f.load(&f.config(&format!("    allow: ['{rule}']\n")))
                    .is_err()
            );
        }
        assert!(
            f.load(&f.config("    allow: ['GET /', 'GET /']\n"))
                .is_err()
        );
    }

    #[test]
    fn origin_is_required_fixed_and_checked_even_when_disabled() {
        let f = Fixture::new();
        let config = f.config("");
        for changed in [
            config.replace("    origin: http://127.0.0.1:12345\n", ""),
            config.replace(
                "origin: http://127.0.0.1:12345",
                "origin: http://127.0.0.1:12346",
            ),
            config.replace(
                "origin: http://127.0.0.1:12345",
                "origin: http://127.0.0.1:12345/base",
            ),
            config.replace("127.0.0.1", "localhost"),
            config.replace("allow_insecure_http: true", "allow_insecure_http: false"),
            config.replace("12345/backend", "12345/a/../backend"),
        ] {
            assert!(f.load(&changed).is_err());
            assert!(f.load(&(changed + "    enabled: false\n")).is_err());
        }
        let (_, origin) = mock_url("http://127.0.0.1:80/", true).unwrap();
        assert_eq!(origin, "http://127.0.0.1:80");
        assert!(mock_url("http://127.0.0.1:0", true).is_err());
    }

    #[test]
    fn unsupported_fields_types_documents_and_duplicates_fail_closed() {
        let f = Fixture::new();
        for extra in [
            "    websocket: false\n",
            "    rules: []\n",
            "    account: ''\n",
            "    billing: local\n",
            "    usage: off\n",
            "    redirects: follow\n",
            "    request_id_header: X-Request-ID\n",
            "    enabled: 'false'\n",
            "    allow: GET /\n",
            "    enabled: true\n    enabled: false\n",
        ] {
            assert!(f.load(&f.config(extra)).is_err());
        }
        for extra in ["accounts: {}\n", "money: {}\n", "---\nroutes: []\n"] {
            assert!(f.load(&(f.config("") + extra)).is_err());
        }
        assert!(f.load(&f.config("").replace("file:", "stored:")).is_err());
        assert!(
            f.load(&f.config("").replace(
                "        secret: key",
                "        secret: key\n        secret: key"
            ))
            .is_err()
        );
        assert!(
            f.load(
                &f.config("")
                    .replace("    origin:", "    extra: 1\n    origin:")
            )
            .is_err()
        );
    }

    #[test]
    fn headers_support_literals_and_custom_caller_format_but_not_proxy_controls() {
        let f = Fixture::new();
        let config = f
            .config("    caller_auth:\n      header: X-Local-Key\n      prefix: 'Custom '\n")
            .replace(
                "        secret: key\n        prefix: 'Bearer '",
                "        value: literal",
            );
        let routes = f.load(&config).unwrap();
        assert_eq!(routes[0].caller_header, "x-local-key");
        assert_eq!(routes[0].caller_prefix, "Custom ");
        assert_eq!(routes[0].credentials["authorization"], "literal");
        for name in [
            "Host",
            "Cookie",
            "X-Term-Llm-Proxy-Key",
            "Connection",
            "Content-Length",
        ] {
            assert!(
                f.load(&config.replace("      Authorization:", &format!("      {name}:")))
                    .is_err()
            );
        }
        assert!(
            f.load(
                &f.config("")
                    .replace("        secret: key", "        value: literal")
            )
            .is_err()
        );
        assert!(
            f.load(&f.config("").replace(
                "        secret: key",
                "        secret: key\n        value: literal"
            ))
            .is_err()
        );
        let duplicate = f.config("").replace(
            "    set_headers:",
            "    set_headers:\n      authorization:\n        value: literal",
        );
        assert!(f.load(&duplicate).is_err());
    }

    #[test]
    fn prefixes_and_names_are_validated_across_disabled_routes() {
        let f = Fixture::new();
        for prefix in [
            "/",
            "/assets/",
            "/_proxy/x/",
            "/healthz/",
            "/a/../b/",
            "/a//b/",
            "/a%20/",
            "/a",
        ] {
            assert!(f.load(&f.config("").replace("/mock/", prefix)).is_err());
        }
        let first = f.config("");
        let second = "  - name: second\n    local_prefix: /mock/nested/\n    upstream: http://127.0.0.1:12345\n    allow_insecure_http: true\n    enabled: false\n";
        assert!(f.load(&(first.clone() + second)).is_err());
        assert!(
            f.load(
                &(first
                    + &second
                        .replace("second", "mock")
                        .replace("/mock/nested/", "/other/"))
            )
            .is_err()
        );
    }

    #[test]
    fn raw_path_validation_and_redirect_defaults() {
        for path in [
            "/",
            "/a//b",
            "/ordinary%20identifier",
            "/literal%25",
            "/a%2fb",
        ] {
            assert!(safe_path(path));
        }
        for path in [
            "relative",
            "/../a",
            "/a/%2E%2e/b",
            "/%252e%252e/b",
            "/a%5cb",
            "/a%00b",
            "/bad%",
            "/bad%zz",
        ] {
            assert!(!safe_path(path));
        }
        let f = Fixture::new();
        assert!(!f.load(&f.config("")).unwrap()[0].refuse_redirects);
        assert!(!f.load(&f.config("    redirects: passthrough\n")).unwrap()[0].refuse_redirects);
        assert!(f.load(&f.config("    redirects: refuse\n")).unwrap()[0].refuse_redirects);
    }

    #[test]
    fn bounded_private_files_and_protected_aliases() {
        let f = Fixture::new();
        let secret = f.0.join("secret");
        assert_eq!(read_secret(&secret, 15).unwrap(), "synthetic-key");
        assert!(read_secret(&secret, 14).is_err());
        fs::set_permissions(&secret, fs::Permissions::from_mode(0o640)).unwrap();
        assert!(read_secret(&secret, 100).is_err());
        fs::set_permissions(&secret, fs::Permissions::from_mode(0o600)).unwrap();
        assert!(read_secret(&f.0, 100).is_err());
        let alias = f.0.join("alias");
        symlink(&secret, &alias).unwrap();
        assert!(read_secret(&alias, 100).is_err());
        let hard = f.0.join("hard");
        fs::hard_link(&secret, &hard).unwrap();
        let config = f.write("config.yaml", &f.config(""), 0o600);
        assert!(load_routes(&config, &[&secret]).is_err());
        assert!(load_routes(&config, &[&hard]).is_err());
        assert!(load_routes(&config, &[&f.0]).is_err());
        let directory_alias = f.0.join("dir-alias");
        symlink(&f.0, &directory_alias).unwrap();
        assert!(load_routes(&config, &[&directory_alias]).is_err());
        for value in ["", "\r\n", "bad\nvalue", "bad\0value"] {
            let path = f.write("invalid", value, 0o600);
            assert!(read_secret(&path, 100).is_err());
        }
        let spaces = f.write("spaces", "a b\r\n", 0o400);
        assert_eq!(read_secret(&spaces, 100).unwrap(), "a b");
    }
}
