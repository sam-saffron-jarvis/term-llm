# redis

Redis dev cache in a single Compose service. Named volume for AOF/RDB
snapshots, env-var override for the loopback-only host port (`REDIS_PORT`, default 6379).

## Connect

From the host (the port is bound to `127.0.0.1` only):

```sh
redis-cli -p ${REDIS_PORT:-6379}
```

From another container on the same Docker network: use the service name
(`cache`) as the host.
