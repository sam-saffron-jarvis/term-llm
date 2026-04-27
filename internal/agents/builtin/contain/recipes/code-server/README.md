# code-server

VS Code in the browser with the project source bind-mounted at
`/home/coder/project`. Editor settings/extensions live in a named volume so
they survive `rebuild`.

The host port is bound to `127.0.0.1` only — open
`http://localhost:${CODE_PORT:-8443}` from the same machine. For remote
access, prefer an SSH tunnel:

```sh
ssh -N -L ${CODE_PORT:-8443}:127.0.0.1:${CODE_PORT:-8443} <host>
```

The web UI password is generated into `.env` as `CODE_PASSWORD` at workspace
creation time. The rendered `.env` is written with 0600 permissions.
