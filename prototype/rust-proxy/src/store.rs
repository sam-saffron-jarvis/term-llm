//! Caller persistence compatible with Go 0821cc18's version-6 proxy store.
//! The owner must serialize all access under its admin mutex and commit here
//! before publishing any caller changes in memory. No credentials are logged.

use rusqlite::{Connection, OpenFlags, Transaction, TransactionBehavior, params};
use std::collections::{BTreeMap, BTreeSet};
use std::error::Error;
use std::fs::{self, OpenOptions};
use std::io;
use std::os::unix::fs::{DirBuilderExt, MetadataExt, OpenOptionsExt};
use std::path::Path;
use std::time::Duration;

// Verbatim schema components from store.go, account_store.go, money_store.go,
// claude_vault_store.go and migration.go at the pinned oracle. Keep all tables,
// including unsupported subsystems, so Go can reopen a Rust-created database.
const SCHEMA_V6: &str = r#"
CREATE TABLE configuration (id INTEGER PRIMARY KEY CHECK(id=1), revision INTEGER NOT NULL, data TEXT NOT NULL);
INSERT INTO configuration VALUES(1,0,'{"routes":[]}');
CREATE TABLE secrets (name TEXT PRIMARY KEY, origin TEXT NOT NULL, value TEXT NOT NULL);
CREATE TABLE callers (id TEXT PRIMARY KEY, name TEXT NOT NULL, hash TEXT UNIQUE NOT NULL, created INTEGER NOT NULL, revoked INTEGER NOT NULL DEFAULT 0, created_by TEXT NOT NULL DEFAULT '', external_id TEXT NOT NULL DEFAULT '');
CREATE TABLE grants (caller TEXT NOT NULL, route TEXT NOT NULL, authority TEXT NOT NULL, PRIMARY KEY(caller,route));
CREATE TABLE requests (seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT UNIQUE NOT NULL, started INTEGER NOT NULL, caller TEXT NOT NULL, route TEXT NOT NULL, method TEXT NOT NULL, path TEXT NOT NULL, outcome TEXT NOT NULL, data TEXT NOT NULL);
CREATE INDEX requests_time ON requests(started,seq);
CREATE INDEX requests_caller ON requests(caller,seq);
CREATE INDEX requests_route ON requests(route,seq);
CREATE TABLE accounts (name TEXT PRIMARY KEY, kind TEXT NOT NULL, state TEXT NOT NULL, generation INTEGER NOT NULL, expires INTEGER NOT NULL, updated INTEGER NOT NULL, data TEXT NOT NULL);
CREATE TABLE money_settings(id INTEGER PRIMARY KEY CHECK(id=1),data TEXT NOT NULL);
INSERT INTO money_settings VALUES(1,'{}');
CREATE TABLE money_history(request_id TEXT PRIMARY KEY,started INTEGER NOT NULL,caller TEXT NOT NULL,route TEXT NOT NULL,data TEXT NOT NULL);
CREATE INDEX money_history_started ON money_history(started,request_id);
CREATE TABLE claude_vault (
 id TEXT PRIMARY KEY, caller TEXT NOT NULL, route TEXT NOT NULL, authority TEXT NOT NULL,
 account TEXT NOT NULL, identity INTEGER NOT NULL, generation INTEGER NOT NULL,
 access_hash TEXT UNIQUE NOT NULL, refresh_hash TEXT UNIQUE NOT NULL,
 state TEXT NOT NULL, expires INTEGER NOT NULL, updated INTEGER NOT NULL, data BLOB NOT NULL
);
CREATE INDEX claude_vault_binding ON claude_vault(caller,route,authority,account,identity);
CREATE UNIQUE INDEX callers_external_id ON callers(external_id) WHERE external_id != '' AND revoked = 0;
CREATE TABLE admin_keys (
 id TEXT PRIMARY KEY, name TEXT NOT NULL, hash TEXT UNIQUE NOT NULL,
 created INTEGER NOT NULL, last_used INTEGER NOT NULL DEFAULT 0
);
PRAGMA user_version=6;
"#;

pub struct StoredCaller {
    pub(crate) id: String,
    pub(crate) name: String,
    pub(crate) hash: [u8; 32],
    pub(crate) created: u64,
    pub(crate) last_used: u64,
    pub(crate) revoked: bool,
    pub(crate) grants: Vec<String>,
    pub(crate) created_by: String,
    pub(crate) external_id: String,
}

pub struct Store {
    db: Connection,
    authorities: BTreeMap<String, String>,
    revision: i64,
}

impl Store {
    /// Activate operator-file authorities, without replacing stored config or
    /// secrets. Old schema migrations are deliberately outside this slice.
    pub fn open(
        state_dir: &Path,
        authorities: &BTreeMap<String, String>,
    ) -> Result<Store, Box<dyn Error>> {
        fs::DirBuilder::new()
            .recursive(true)
            .mode(0o700)
            .create(state_dir)?;
        check_private(state_dir, true)?;
        check_sidecars(state_dir)?;
        let path = state_dir.join("proxy.db");
        let file = OpenOptions::new()
            .read(true)
            .write(true)
            .create(true)
            .truncate(false)
            .mode(0o600)
            .custom_flags(libc::O_NOFOLLOW | libc::O_NONBLOCK)
            .open(&path)?;
        check_private(&path, false)?;
        let meta = file.metadata()?;
        if !meta.is_file() || meta.nlink() != 1 {
            return Err(io::Error::other("proxy database must be a private regular file").into());
        }
        let mut db = Connection::open_with_flags(
            &path,
            OpenFlags::SQLITE_OPEN_READ_WRITE | OpenFlags::SQLITE_OPEN_NO_MUTEX,
        )?;
        db.busy_timeout(Duration::from_millis(3000))?;
        // Reject older/newer versions before any journal-mode change or write.
        let version: i64 = db.query_row("PRAGMA user_version", [], |r| r.get(0))?;
        if version != 0 && version != 6 {
            return Err(io::Error::other("unsupported proxy schema version (requires v6)").into());
        }
        {
            let tx = db.transaction_with_behavior(TransactionBehavior::Immediate)?;
            let version: i64 = tx.query_row("PRAGMA user_version", [], |r| r.get(0))?;
            match version {
                0 => {
                    let objects: i64 = tx.query_row(
                        "SELECT count(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'",
                        [],
                        |r| r.get(0),
                    )?;
                    if objects != 0 {
                        return Err(io::Error::other("unknown unversioned proxy schema").into());
                    }
                    tx.execute_batch(SCHEMA_V6)?;
                }
                6 => {}
                _ => {
                    return Err(
                        io::Error::other("unsupported proxy schema version (requires v6)").into(),
                    );
                }
            }
            tx.commit()?;
        }
        let journal: String = db.query_row("PRAGMA journal_mode=WAL", [], |r| r.get(0))?;
        if !journal.eq_ignore_ascii_case("wal") {
            return Err(io::Error::other("proxy database requires WAL journaling").into());
        }
        db.pragma_update(None, "synchronous", "NORMAL")?;
        check_sidecars(state_dir)?;
        let revision;
        {
            let tx = db.transaction_with_behavior(TransactionBehavior::Immediate)?;
            let pairs = {
                let mut stmt = tx.prepare("SELECT DISTINCT route,authority FROM grants")?;
                stmt.query_map([], |r| Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?)))?
                    .collect::<rusqlite::Result<Vec<_>>>()?
            };
            for (route, authority) in pairs {
                if authority.is_empty() || authorities.get(&route) != Some(&authority) {
                    tx.execute(
                        "DELETE FROM grants WHERE route=? AND authority=?",
                        params![route, authority],
                    )?;
                    tx.execute(
                        "DELETE FROM claude_vault WHERE route=? AND authority=?",
                        params![route, authority],
                    )?;
                }
            }
            let previous: i64 =
                tx.query_row("SELECT revision FROM configuration WHERE id=1", [], |r| {
                    r.get(0)
                })?;
            revision = previous
                .checked_add(1)
                .ok_or_else(|| io::Error::other("proxy revision exhausted"))?;
            tx.execute("UPDATE configuration SET revision=? WHERE id=1", [revision])?;
            tx.commit()?;
        }
        Ok(Store {
            db,
            authorities: authorities.clone(),
            revision,
        })
    }

    /// Startup activation advances the revision; caller mutations do not (Go).
    pub fn revision(&self) -> i64 {
        self.revision
    }

    pub fn load(&self) -> rusqlite::Result<Vec<StoredCaller>> {
        // A single read snapshot keeps caller and grant metadata consistent.
        let tx = self.db.unchecked_transaction()?;
        let mut callers = {
            let mut stmt = tx.prepare(
                "SELECT c.id,c.name,c.hash,c.created,\
                 COALESCE((SELECT r.started FROM requests r WHERE r.caller=c.id ORDER BY r.seq DESC LIMIT 1),0),\
                 c.revoked,c.created_by,c.external_id FROM callers c ORDER BY c.created,c.id",
            )?;
            stmt.query_map([], |r| {
                let encoded: String = r.get(2)?;
                Ok(StoredCaller {
                    id: r.get(0)?,
                    name: r.get(1)?,
                    hash: decode_hash(&encoded)?,
                    created: positive_time(r.get(3)?)?,
                    last_used: positive_time(r.get(4)?)?,
                    revoked: r.get(5)?,
                    grants: Vec::new(),
                    created_by: r.get(6)?,
                    external_id: r.get(7)?,
                })
            })?
            .collect::<rusqlite::Result<Vec<_>>>()?
        };
        {
            let mut stmt =
                tx.prepare("SELECT route,authority FROM grants WHERE caller=? ORDER BY route")?;
            for caller in &mut callers {
                let rows = stmt.query_map([&caller.id], |r| {
                    Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?))
                })?;
                for row in rows {
                    let (route, authority) = row?;
                    // Fail closed even if an external writer adds a stale proof
                    // after activation. Revoked callers still retain metadata.
                    if !authority.is_empty() && self.authorities.get(&route) == Some(&authority) {
                        caller.grants.push(route);
                    }
                }
            }
        }
        tx.commit()?;
        Ok(callers)
    }

    pub fn insert(
        &self,
        id: &str,
        name: &str,
        created: u64,
        hash: &[u8; 32],
        grants: &[String],
    ) -> rusqlite::Result<()> {
        let created =
            i64::try_from(created).map_err(|_| invalid("invalid caller creation time"))?;
        let tx = Transaction::new_unchecked(&self.db, TransactionBehavior::Immediate)?;
        make_caller_room(&tx)?;
        tx.execute(
            "INSERT INTO callers(id,name,hash,created) VALUES(?,?,?,?)",
            params![id, name, encode_hash(hash), created],
        )?;
        self.write_grants(&tx, id, grants)?;
        tx.commit()
    }

    pub fn update_grants(&self, id: &str, grants: &[String]) -> rusqlite::Result<()> {
        let tx = Transaction::new_unchecked(&self.db, TransactionBehavior::Immediate)?;
        let revoked: bool =
            tx.query_row("SELECT revoked FROM callers WHERE id=?", [id], |r| r.get(0))?;
        if revoked {
            return Err(invalid("revoked clients cannot receive new grants"));
        }
        tx.execute("DELETE FROM grants WHERE caller=?", [id])?;
        self.write_grants(&tx, id, grants)?;
        tx.execute(
            "DELETE FROM claude_vault WHERE caller=? AND NOT EXISTS \
             (SELECT 1 FROM grants g WHERE g.caller=claude_vault.caller \
             AND g.route=claude_vault.route AND g.authority=claude_vault.authority)",
            [id],
        )?;
        tx.commit()
    }

    pub fn revoke(&self, id: &str) -> rusqlite::Result<()> {
        let tx = Transaction::new_unchecked(&self.db, TransactionBehavior::Immediate)?;
        tx.execute("UPDATE callers SET revoked=1 WHERE id=?", [id])?;
        tx.execute("DELETE FROM claude_vault WHERE caller=?", [id])?;
        // Go intentionally retains grants, attribution and history on revoke.
        tx.commit()
    }

    fn write_grants(
        &self,
        tx: &Transaction<'_>,
        id: &str,
        grants: &[String],
    ) -> rusqlite::Result<()> {
        if grants.len() > 128 {
            return Err(invalid("at most 128 route grants per client"));
        }
        let mut seen = BTreeSet::new();
        for route in grants {
            let bytes = route.as_bytes();
            if bytes.is_empty()
                || bytes.len() > 64
                || !bytes[0].is_ascii_alphanumeric()
                || !bytes
                    .iter()
                    .all(|c| c.is_ascii_alphanumeric() || *c == b'_' || *c == b'-')
                || !seen.insert(route)
            {
                return Err(invalid(
                    "route grants must contain unique valid route names",
                ));
            }
            let authority = self
                .authorities
                .get(route)
                .filter(|authority| !authority.is_empty())
                .ok_or_else(|| invalid("route grant is not in the active configuration"))?;
            tx.execute(
                "INSERT INTO grants(caller,route,authority) VALUES(?,?,?)",
                params![id, route, authority],
            )?;
        }
        Ok(())
    }
}

fn make_caller_room(tx: &Transaction<'_>) -> rusqlite::Result<()> {
    let (total, active): (i64, i64) = tx.query_row(
        "SELECT count(*),count(*) FILTER (WHERE revoked=0) FROM callers",
        [],
        |r| Ok((r.get(0)?, r.get(1)?)),
    )?;
    if active >= 1024 {
        return Err(invalid("client limit reached"));
    }
    if total < 4096 {
        return Ok(());
    }
    let count = total - 4096 + 1;
    let ids = {
        let mut stmt =
            tx.prepare("SELECT id FROM callers WHERE revoked!=0 ORDER BY created,id LIMIT ?")?;
        stmt.query_map([count], |r| r.get::<_, String>(0))?
            .collect::<rusqlite::Result<Vec<_>>>()?
    };
    if ids.len() as i64 != count {
        return Err(invalid("client limit reached"));
    }
    for id in ids {
        tx.execute("DELETE FROM grants WHERE caller=?", [&id])?;
        tx.execute("DELETE FROM claude_vault WHERE caller=?", [&id])?;
        tx.execute("DELETE FROM callers WHERE id=? AND revoked!=0", [&id])?;
    }
    Ok(())
}

fn invalid(message: &'static str) -> rusqlite::Error {
    rusqlite::Error::ToSqlConversionFailure(Box::new(io::Error::new(
        io::ErrorKind::InvalidInput,
        message,
    )))
}

fn encode_hash(hash: &[u8; 32]) -> String {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    let mut out = String::with_capacity(64);
    for byte in hash {
        out.push(HEX[(byte >> 4) as usize] as char);
        out.push(HEX[(byte & 15) as usize] as char);
    }
    out
}

fn decode_hash(encoded: &str) -> rusqlite::Result<[u8; 32]> {
    let mut hash = [0u8; 32];
    let bytes = encoded.as_bytes();
    if bytes.len() != 64 {
        return Err(invalid("invalid stored caller hash"));
    }
    fn digit(byte: u8) -> rusqlite::Result<u8> {
        match byte {
            b'0'..=b'9' => Ok(byte - b'0'),
            b'a'..=b'f' => Ok(byte - b'a' + 10),
            _ => Err(invalid("invalid stored caller hash")),
        }
    }
    for (i, pair) in bytes.chunks_exact(2).enumerate() {
        hash[i] = (digit(pair[0])? << 4) | digit(pair[1])?;
    }
    Ok(hash)
}

fn check_private(path: &Path, directory: bool) -> io::Result<()> {
    let meta = fs::symlink_metadata(path)?;
    let correct_type = if directory {
        meta.is_dir()
    } else {
        meta.is_file() && meta.nlink() == 1
    };
    if !correct_type || meta.mode() & 0o077 != 0 || meta.uid() != unsafe { libc::geteuid() } {
        return Err(io::Error::other(
            "proxy state must be owned, private and non-symlink",
        ));
    }
    Ok(())
}

fn check_sidecars(dir: &Path) -> io::Result<()> {
    for name in ["proxy.db-wal", "proxy.db-shm", "proxy.db-journal"] {
        let path = dir.join(name);
        match fs::symlink_metadata(&path) {
            Ok(_) => check_private(&path, false)?,
            Err(err) if err.kind() == io::ErrorKind::NotFound => {}
            Err(err) => return Err(err),
        }
    }
    Ok(())
}

fn positive_time(value: i64) -> rusqlite::Result<u64> {
    u64::try_from(value).map_err(|_| invalid("invalid stored caller timestamp"))
}
