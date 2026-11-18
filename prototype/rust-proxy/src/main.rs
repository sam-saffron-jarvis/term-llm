mod admin;
mod config;
mod forward;
mod store;

use admin::Admin;
use axum::Router;
use axum::http::StatusCode;
use clap::{Parser, Subcommand};
use config::{load_routes, read_secret};
use reqwest::Client;
use std::fs;
use std::io;
use std::net::{IpAddr, Ipv4Addr, Ipv6Addr, SocketAddr};
use std::os::unix::fs::{DirBuilderExt, MetadataExt};
use std::path::PathBuf;
use std::sync::Arc;
use std::time::Duration;
use tokio::net::TcpListener;
use tokio::signal::unix::{SignalKind, signal};
use tokio::sync::Semaphore;

#[derive(Parser)]
#[command(
    name = "term-llm serve proxy",
    disable_help_subcommand = true,
    about = "Run a standalone configuration-driven credential proxy (Rust parity candidate)"
)]
struct Args {
    #[arg(long)]
    config: Option<PathBuf>,
    #[arg(long, default_value = "127.0.0.1", global = true)]
    host: String,
    #[arg(long, default_value_t = 8082, global = true)]
    port: u16,
    #[arg(long, default_value_t = 64 << 20)]
    max_body_bytes: usize,
    #[arg(long, default_value_t = 64)]
    max_concurrent: usize,
    #[arg(long, default_value = "15m")]
    request_timeout: String,
    #[arg(long, default_value_t = 30)]
    retention_days: u16,
    #[arg(long, default_value = "passkey")]
    auth: String,
    #[arg(long)]
    state_dir: Option<PathBuf>,
    #[arg(long)]
    admin_token_file: Option<PathBuf>,
    #[arg(long)]
    passkey_bootstrap_token_file: Option<PathBuf>,
    #[arg(long)]
    passkey_recovery_token_file: Option<PathBuf>,
    #[arg(long)]
    public_url: Option<String>,
    #[arg(long)]
    debug_payload_dir: Option<PathBuf>,
    #[arg(long)]
    debug_payload_max_bytes: Option<i64>,
    #[arg(long)]
    debug_payload_max_total_bytes: Option<i64>,
    #[arg(long, global = true, num_args = 0..=1, require_equals = true, default_missing_value = "true", default_value = "false", action = clap::ArgAction::Set)]
    debug_raw: bool,
    #[arg(long, global = true)]
    cpuprofile: Option<String>,
    #[arg(long, global = true)]
    memprofile: Option<String>,
    #[arg(long, global = true, num_args = 0..=1, require_equals = true, default_missing_value = "0")]
    pprof: Option<String>,
    #[command(subcommand)]
    command: Option<Command>,
}

#[derive(Subcommand)]
enum Command {
    /// Probe local proxy/store readiness without contacting upstreams.
    Health,
}

pub(crate) fn bad(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidInput, message)
}

fn address(host: &str, port: u16) -> io::Result<SocketAddr> {
    let ip: IpAddr = host.parse().map_err(|_| bad("host must be loopback"))?;
    if !ip.is_loopback() {
        return Err(bad("host must be loopback"));
    }
    Ok(SocketAddr::new(ip, port))
}

fn client() -> reqwest::Result<Client> {
    Client::builder()
        .no_proxy()
        .redirect(reqwest::redirect::Policy::none())
        .retry(reqwest::retry::never())
        .resolve_to_addrs(
            "localhost",
            &[
                SocketAddr::new(Ipv6Addr::LOCALHOST.into(), 0),
                SocketAddr::new(Ipv4Addr::LOCALHOST.into(), 0),
            ],
        )
        .http1_only()
        .connect_timeout(Duration::from_secs(15))
        .build()
}

async fn health(client: &Client, host: &str, port: u16) -> Result<(), Box<dyn std::error::Error>> {
    let addr = if host == "localhost" {
        format!("localhost:{port}")
    } else {
        address(host, port)?.to_string()
    };
    let response = client
        .get(format!("http://{addr}/healthz"))
        .timeout(Duration::from_secs(3))
        .send()
        .await
        .map_err(|e| io::Error::other(format!("proxy not ready: {e}")))?;
    if response.status() != StatusCode::OK {
        return Err(io::Error::other(format!(
            "proxy not ready: HTTP {}",
            response.status().as_u16()
        ))
        .into());
    }
    Ok(())
}

fn state_dir(explicit: Option<PathBuf>) -> io::Result<PathBuf> {
    let path = match explicit {
        Some(path) => path,
        None => {
            let config = std::env::var_os("XDG_CONFIG_HOME")
                .filter(|value| !value.is_empty())
                .map(PathBuf::from)
                .or_else(|| {
                    std::env::var_os("HOME")
                        .filter(|value| !value.is_empty())
                        .map(|home| PathBuf::from(home).join(".config"))
                })
                .ok_or_else(|| bad("locate private proxy state (or set --state-dir)"))?;
            config.join("term-llm/proxy")
        }
    };
    fs::DirBuilder::new()
        .recursive(true)
        .mode(0o700)
        .create(&path)?;
    let meta = fs::symlink_metadata(&path)?;
    if !meta.is_dir() || meta.mode() & 0o077 != 0 || meta.uid() != unsafe { libc::geteuid() } {
        return Err(bad(
            "proxy state directory must be an owned private non-symlink directory",
        ));
    }
    Ok(path)
}

async fn run(args: Args) -> Result<(), Box<dyn std::error::Error>> {
    if args.debug_raw
        || args.cpuprofile.as_ref().is_some_and(|v| !v.is_empty())
        || args.memprofile.as_ref().is_some_and(|v| !v.is_empty())
        || args.pprof.as_ref().is_some_and(|v| !v.is_empty())
        || std::env::var_os("TERM_LLM_PPROF").is_some_and(|v| !v.is_empty())
    {
        return Err(bad("standalone credential proxy refuses --debug-raw, CPU/heap profiles and TERM_LLM_PPROF/--pprof").into());
    }
    let client = client()?;
    if matches!(args.command, Some(Command::Health)) {
        return health(&client, &args.host, args.port).await;
    }
    if args.auth == "passkey" {
        if args.admin_token_file.is_some() {
            return Err(bad("--admin-token-file requires explicit --auth token; there is no token bypass in passkey mode").into());
        }
        return Err(bad(
            "passkey support is not yet ported; Rust candidate does not start in passkey mode",
        )
        .into());
    }
    if args.auth != "token" {
        return Err(bad("--auth must be passkey or token").into());
    }
    if args.passkey_bootstrap_token_file.is_some() || args.passkey_recovery_token_file.is_some() {
        return Err(bad("passkey enrollment/recovery files require --auth passkey").into());
    }
    let token_file = args
        .admin_token_file
        .ok_or_else(|| bad("--admin-token-file is required with --auth token"))?;
    let token = read_secret(&token_file, 4096)?;
    let token = token.trim();
    if token.len() < 32 || token.contains([' ', '\t', '\r', '\n']) {
        return Err(bad("admin token must have at least 32 non-whitespace characters; provision 256 random bits").into());
    }
    if args.public_url.is_some()
        || args.debug_payload_dir.is_some()
        || args.debug_payload_max_bytes.is_some()
        || args.debug_payload_max_total_bytes.is_some()
        || !matches!(args.retention_days, 0 | 30)
    {
        return Err(bad(
            "public URL, payload capture and custom retention require unfinished Rust slices",
        )
        .into());
    }
    let timeout = humantime::parse_duration(&args.request_timeout)?;
    let timeout = if timeout.is_zero() {
        Duration::from_secs(900)
    } else {
        timeout
    };
    let limit = if args.max_body_bytes == 0 {
        64 << 20
    } else {
        args.max_body_bytes
    };
    let concurrency = if args.max_concurrent == 0 {
        64
    } else {
        args.max_concurrent
    };
    if limit > 1 << 30 || concurrency > 1024 || timeout > Duration::from_secs(86_400) {
        return Err(bad(
            "proxy bounds exceed maximum (1 GiB body, 1024 concurrency, 24h deadline)",
        )
        .into());
    }
    let state = state_dir(args.state_dir)?;
    let cfg = args
        .config
        .ok_or_else(|| bad("editable UI-managed routes are not yet ported; --config required"))?;
    let routes = load_routes(&cfg, &[&token_file, &state])?;
    let bind = if args.host == "localhost" {
        SocketAddr::new(Ipv4Addr::LOCALHOST.into(), args.port)
    } else {
        address(&args.host, args.port)?
    };
    let listener = TcpListener::bind(bind).await?;
    let bound = listener.local_addr()?;
    let admin = Admin::open_persistent(token, &state, &routes)?;
    let proxy = forward::Proxy {
        client,
        admin,
        routes,
        limit,
        timeout,
        slots: Arc::new(Semaphore::new(concurrency)),
        bound,
    };
    let mut term = signal(SignalKind::terminate())?;
    let mut interrupt = signal(SignalKind::interrupt())?;
    println!("Proxy ready · http://{bound}");
    axum::serve(
        listener,
        Router::new()
            .fallback(forward::serve)
            .with_state(Arc::new(proxy))
            .into_make_service_with_connect_info::<SocketAddr>(),
    )
    .with_graceful_shutdown(async move {
        tokio::select! { _ = term.recv() => {}, _ = interrupt.recv() => {} }
    })
    .await?;
    Ok(())
}

#[tokio::main(worker_threads = 2)]
async fn main() {
    if let Err(error) = run(Args::parse()).await {
        eprintln!("Error: {error}");
        std::process::exit(1);
    }
}
