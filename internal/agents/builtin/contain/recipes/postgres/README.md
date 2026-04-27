# postgres

A Postgres dev database in a single Compose service. Named volume for data,
env-var override for the loopback-only host port (`PG_PORT`, default 5432).
The generated password is stored in `.env` as `PG_PASSWORD`; the rendered `.env`
is written with 0600 permissions.

## Connect

From the host (the port is bound to `127.0.0.1` only):

```sh
psql "postgresql://${PG_USER:-postgres}:<password>@localhost:${PG_PORT:-5432}/${PG_DB:-app}"
```

From another container on the same Docker network: use the service name
(`db`) as the host.
