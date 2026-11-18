use crate::{
    admin::Admin,
    config::{Route, safe_path},
};
use axum::body::Body;
use axum::extract::{Request, State};
use axum::http::{HeaderMap, HeaderValue, Method, StatusCode, header};
use axum::response::{IntoResponse, Response};
use base64::Engine;
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use futures_util::StreamExt;
use reqwest::Client;
use std::fs::File;
use std::io::{self, Read};
use std::net::SocketAddr;
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::Semaphore;

pub(crate) struct Proxy {
    pub(crate) client: Client,
    pub(crate) admin: Admin,
    pub(crate) routes: Vec<Route>,
    pub(crate) limit: usize,
    pub(crate) timeout: Duration,
    pub(crate) slots: Arc<Semaphore>,
    pub(crate) bound: SocketAddr,
}

const REQUEST_ID: &str = "x-term-llm-proxy-request-id";

pub(crate) fn random_token(prefix: &str) -> io::Result<String> {
    let mut bytes = [0u8; 32];
    File::open("/dev/urandom")?.read_exact(&mut bytes)?;
    Ok(format!("{prefix}{}", URL_SAFE_NO_PAD.encode(bytes)))
}

fn http_error(status: StatusCode, text: &'static str) -> Response {
    (status, [(header::X_CONTENT_TYPE_OPTIONS, "nosniff")], text).into_response()
}

fn strip_hop(headers: &mut HeaderMap) {
    let connection_headers: Vec<_> = headers
        .get_all(header::CONNECTION)
        .iter()
        .filter_map(|v| v.to_str().ok())
        .flat_map(|v| v.split(','))
        .filter_map(|name| header::HeaderName::from_bytes(name.trim().as_bytes()).ok())
        .collect();
    for name in connection_headers {
        headers.remove(name);
    }
    for name in [
        "connection",
        "proxy-connection",
        "keep-alive",
        "proxy-authenticate",
        "proxy-authorization",
        "te",
        "trailer",
        "transfer-encoding",
        "upgrade",
    ] {
        headers.remove(name);
    }
}

fn outbound_headers(mut headers: HeaderMap, route: &Route) -> HeaderMap {
    strip_hop(&mut headers);
    headers.remove(&route.caller_header);
    let private: Vec<_> = headers
        .keys()
        .filter(|name| {
            matches!(
                name.as_str(),
                "host"
                    | "authorization"
                    | "cookie"
                    | "forwarded"
                    | "x-real-ip"
                    | "x-original-url"
                    | "x-rewrite-url"
            ) || name.as_str().starts_with("x-forwarded-")
                || name.as_str().starts_with("x-term-llm-proxy-")
        })
        .cloned()
        .collect();
    for name in private {
        headers.remove(name);
    }
    for name in route.credentials.keys() {
        headers.remove(name);
        for value in route.credentials.get_all(name) {
            headers.append(name.clone(), value.clone());
        }
    }
    headers
}

fn authorization(req: &Request, proxy: &Proxy, route: &Route) -> Result<(), &'static str> {
    let mut values = req.headers().get_all(&route.caller_header).iter();
    let token = values
        .next()
        .and_then(|v| v.to_str().ok())
        .and_then(|v| v.strip_prefix(&route.caller_prefix));
    let Some(token) = token.filter(|s| !s.is_empty() && values.next().is_none()) else {
        return Err("unauthorized\n");
    };
    if proxy.admin.is_admin_token(token) {
        return Err("unauthorized\n");
    }
    if !proxy.admin.authorize(token, &route.name) {
        return Err("unauthorized or route not granted\n");
    }
    Ok(())
}

pub(crate) async fn serve(State(proxy): State<Arc<Proxy>>, req: Request) -> Response {
    let path = req.uri().path();
    if path == "/healthz" {
        return if req.method() == Method::GET {
            (
                StatusCode::OK,
                [(header::CACHE_CONTROL, "no-store")],
                "ok\n",
            )
                .into_response()
        } else {
            http_error(StatusCode::METHOD_NOT_ALLOWED, "method not allowed\n")
        };
    }
    if path.starts_with("/_proxy/api/") {
        return proxy.admin.response(req, proxy.bound).await;
    }
    if req.method() == Method::CONNECT || req.uri().scheme().is_some() || !safe_path(path) {
        return http_error(
            StatusCode::BAD_REQUEST,
            "unsupported request target or upgrade\n",
        );
    }
    if req.headers().contains_key(header::UPGRADE)
        || req.headers().get_all(header::CONNECTION).iter().any(|v| {
            v.to_str().is_ok_and(|v| {
                v.split(',')
                    .any(|s| s.trim().eq_ignore_ascii_case("upgrade"))
            })
        })
    {
        return http_error(StatusCode::BAD_REQUEST, "unsupported upgrade\n");
    }
    let Ok(permit) = proxy.slots.clone().try_acquire_owned() else {
        return http_error(StatusCode::SERVICE_UNAVAILABLE, "proxy concurrency limit\n");
    };
    let Some(route) = proxy
        .routes
        .iter()
        .find(|route| route.enabled && path.starts_with(&route.prefix))
    else {
        return http_error(StatusCode::NOT_FOUND, "404 page not found\n");
    };
    if let Err(message) = authorization(&req, &proxy, route) {
        return http_error(StatusCode::UNAUTHORIZED, message);
    }
    if route
        .credentials
        .keys()
        .any(|name| req.headers().get_all(name).iter().count() > 1)
    {
        return http_error(StatusCode::BAD_REQUEST, "duplicate credential header\n");
    }
    let len = req
        .headers()
        .get(header::CONTENT_LENGTH)
        .and_then(|v| v.to_str().ok())
        .and_then(|n| n.parse::<u64>().ok());
    if len.is_some_and(|n| n > proxy.limit as u64) {
        return http_error(StatusCode::PAYLOAD_TOO_LARGE, "request body too large\n");
    }
    let Ok(id) = random_token("px_") else {
        return http_error(
            StatusCode::SERVICE_UNAVAILABLE,
            "request ID unavailable; not dispatched\n",
        );
    };
    let id = HeaderValue::from_str(&id[..35]).expect("URL-safe request ID");
    let suffix = &path[route.prefix.len() - 1..];
    let mut response = if !route.permits(req.method(), suffix) {
        http_error(StatusCode::FORBIDDEN, "route policy denied request\n")
    } else {
        dispatch(&proxy, route, suffix.to_owned(), req, len, permit).await
    };
    response.headers_mut().insert(REQUEST_ID, id);
    response
}

async fn dispatch(
    proxy: &Proxy,
    route: &Route,
    suffix: String,
    req: Request,
    len: Option<u64>,
    permit: tokio::sync::OwnedSemaphorePermit,
) -> Response {
    let mut url = route.upstream.clone();
    url.set_path(&format!("{}{suffix}", url.path().trim_end_matches('/')));
    url.set_query(req.uri().query());
    let (parts, body) = req.into_parts();
    let headers = outbound_headers(parts.headers, route);
    let mut request = proxy
        .client
        .request(parts.method, url)
        .timeout(proxy.timeout)
        .headers(headers);
    if len != Some(0) && (len.is_some() || !http_body_empty(&body)) {
        let mut remaining = proxy.limit;
        let mut exceeded = false;
        let stream = body.into_data_stream().map(move |item| match item {
            Ok(chunk) if !exceeded && chunk.len() <= remaining => {
                remaining -= chunk.len();
                Ok(chunk)
            }
            Ok(_) => {
                exceeded = true;
                Err(io::Error::other("request body too large"))
            }
            Err(err) => Err(io::Error::other(err)),
        });
        request = request.body(reqwest::Body::wrap_stream(stream));
    }
    let Ok(mut upstream) = request.send().await else {
        return http_error(
            StatusCode::BAD_GATEWAY,
            "upstream connection failed; outcome may be unknown\n",
        );
    };
    if route.refuse_redirects && upstream.status().is_redirection() {
        let mut response = (StatusCode::BAD_GATEWAY, "upstream redirect refused\n").into_response();
        response.headers_mut().insert(
            "x-term-llm-proxy-redirect",
            HeaderValue::from_static("refused"),
        );
        return response;
    }
    if upstream.status().is_informational() {
        return http_error(
            StatusCode::BAD_GATEWAY,
            "upstream connection failed; outcome may be unknown\n",
        );
    }
    let status = upstream.status();
    let mut headers = std::mem::take(upstream.headers_mut());
    strip_hop(&mut headers);
    headers.remove(header::SET_COOKIE);
    let stream = upstream.bytes_stream().map(move |chunk| {
        let _ = &permit;
        chunk
    });
    let mut response = Response::new(Body::from_stream(stream));
    *response.status_mut() = status;
    *response.headers_mut() = headers;
    response
}

fn http_body_empty(body: &Body) -> bool {
    http_body::Body::is_end_stream(body)
}
