# Running acd-ctx in Docker

acd-ctx also ships as a container image: one proxy for the whole machine,
running in the background, with its data in a named volume. You can run it
next to the local install (`go install` or a release binary). The host-side
`acd-ctx claude` command reuses the container when it is running.

The image is built from the repository [Dockerfile](../Dockerfile). The
compose file is [docker-compose.yml](../docker-compose.yml).

## Build

```sh
docker build \
  --build-arg VERSION="$(acd-ctx version | awk '{print $2}')" \
  -t acd-ctx:local .
```

- `VERSION` is written into the binary (`acd-ctx version`). **Build the image
  with the same version as the host binary.** A host binary from a plain
  `go install` reports `dev`, so the command above gives `dev` as well. The host launcher reuses a
  running proxy only when `/__acd-ctx/status` reports the same version. On a
  mismatch it starts its own local proxy instead.
- `GO_VERSION` (default `1.27.1`) must match the `go` directive in `go.mod`.
  CI passes it from `go.mod`.
- The build is `CGO_ENABLED=0`, `-trimpath`, `-ldflags "-s -w -X main.version=..."`.
  The final stage is `gcr.io/distroless/static-debian12:nonroot`: no shell,
  no package manager, CA certificates included, uid/gid 65532.

## Run with docker run

```sh
docker volume create acd-ctx-data
docker run -d --name acd-ctx \
  -p 127.0.0.1:8787:8787 \
  -v acd-ctx-data:/data \
  --read-only --tmpfs /tmp \
  --cap-drop ALL --security-opt no-new-privileges:true \
  acd-ctx:local

docker exec acd-ctx /acd-ctx healthcheck --port 8787   # exit 0 when healthy
curl -s http://127.0.0.1:8787/__acd-ctx/health         # {"ok":true}
```

The container's `HEALTHCHECK` runs the same probe every 30 seconds, so
`docker ps` shows `healthy` or `unhealthy`.

## Run with compose

```sh
ACD_CTX_VERSION=$(acd-ctx version | awk '{print $2}') docker compose up -d --build
docker compose logs -f acd-ctx
docker compose stop acd-ctx        # graceful: the proxy shuts down on SIGTERM
```

`docker-compose.yml` publishes **only** `127.0.0.1:8787:8787`, mounts the named
volume `acd-ctx-data` at `/data`, sets `read_only: true`, `no-new-privileges`
and `cap_drop: ALL`.

## Configuration

Two layers, as in the rest of acd-ctx (spec §18.5):

**Environment** (set in compose or with `-e`):

| Variable | Default in image | Meaning |
|---|---|---|
| `ACD_CTX_ALLOWED_HOSTS` | `localhost,127.0.0.1,[::1],host.docker.internal` | Host header values the guard accepts. Replaces the defaults; keep the loopback names. |
| `ACD_CTX_ALLOW` | empty | Extra upstream origins reachable through `/ctx/<upstream>/`, comma-separated (`serve --allow`). Empty means `https://api.anthropic.com` only. |
| `ACD_CTX_ALLOW_NON_LOOPBACK` | unset (the CMD passes `--allow-non-loopback`) | Permits a non-loopback listen address. Only effective together with a non-empty `ACD_CTX_ALLOWED_HOSTS`. |
| `ACD_CTX_DB` | `/data/acd-ctx.db` | Database used by every command in the container, the same file `serve` runs on. |

Flags beat env. `docker run ... serve ...` with your own CMD replaces the
defaults, so keep `--allow-non-loopback` and `--listen 0.0.0.0:8787` if you
override it.

**Config file.** Mount a host file read-only at `/data/config.yaml`. The CMD
reads it with `--config /data/config.yaml`. A missing file is fine: the
defaults apply.

Write the file in the same format as the global config (spec §18.1).

The same settings exist as flags of `serve`: `--allow-non-loopback` together with
a non-empty `--allowed-hosts`, and `--allow`. The flags win over the environment.

`serve` reads the mounted file. CLI commands run with `docker exec` (for
example `config show --effective`) have no `--config` flag: they read the
default config path inside the container, `/data/config/acd-ctx/config.yaml`
(`XDG_CONFIG_HOME`), so they do not see the mounted file unless you also mount
it at that path.

Uncomment the bind mount in `docker-compose.yml`:

```yaml
    volumes:
      - acd-ctx-data:/data
      - ./acd-ctx.config.yaml:/data/config.yaml:ro
```

Keep secrets out of the image and out of the file when you can. Credentials
are passed through by the proxy and never stored.

## Using the container from the host

`acd-ctx claude|codex|gemini|opencode` on the host works like this:

1. The launcher checks `127.0.0.1:8787`. Compose publishes the container there,
   so it finds the proxy at the default address.
2. If `/__acd-ctx/health` is OK and `/__acd-ctx/status` reports the same
   version, the launcher reuses the container. Otherwise it starts a local
   proxy on a free port with the host database.
3. The project id is resolved from the **host** working directory. The client
   is pointed at `http://127.0.0.1:8787/p/<project-id>/ctx/<upstream>`, so the
   container keeps sessions apart per project without seeing host paths.

Consequence: `X-Acd-Ctx-Project-Dir` is not usable through the container. The
proxy ignores the header unless the path is an existing directory inside the
container, and host paths are not visible there, so use the `/p/<id>/` prefix
(which the launcher sets for you).

Known limits when the proxy is in the container:

- `/__acd-ctx/metrics` and `/__acd-ctx/sessions/...` accept only loopback peers.
  Requests through the published port arrive from the Docker gateway, so they
  return `403`. To read them, attach a helper container to the same network
  namespace, so its requests arrive from loopback:
  `docker run --rm --network container:acd-ctx curlimages/curl -s http://127.0.0.1:8787/__acd-ctx/metrics`.
  `/__acd-ctx/health` and `/__acd-ctx/status` work from the host.
- The MITM CA lives in `/data/ca` (`mitm` mode). Clients that need it must trust
  the CA from that volume.

## Backup and restore

The database is `/data/acd-ctx.db` (plus `-wal`/`-shm` while running), and the
MITM CA is in `/data/ca`. Back up the whole `/data` directory, or let the CLI
run inside the container:

```sh
acd-ctx backup --docker acd-ctx -o acd-ctx-backup.db       # verified copy, written on the host
acd-ctx restore --docker acd-ctx acd-ctx-backup.db --dry-run
acd-ctx clean --project . --docker acd-ctx --dry-run        # same confirmation rules as on the host
```

`backup`, `restore` and `clean` accept `--docker <container>`: the command runs
inside the container and the backup file is read or written on the host. See
[backup and restore](../README.md#backup-and-restore) and [cleaning](../README.md#cleaning)
for the flags and the irreversibility warning.

Manual backup, with the proxy stopped so the database is consistent:

```sh
docker compose stop acd-ctx
docker run --rm -v acd-ctx-data:/data -v "$PWD":/backup busybox \
  tar czf /backup/acd-ctx-data-$(date +%F).tgz -C /data .
docker compose start acd-ctx
```

Restore into an empty volume, with the proxy stopped:

```sh
docker compose stop acd-ctx
docker run --rm -v acd-ctx-data:/data -v "$PWD":/backup busybox \
  sh -c 'find /data -mindepth 1 -delete && tar xzf /backup/acd-ctx-data-YYYY-MM-DD.tgz -C /data'
docker compose start acd-ctx
```

## Upgrade and persistence

- The data lives in the `acd-ctx-data` volume. Removing the container does not
  remove it. `docker compose down -v` **does** remove it: back up first.
- To upgrade, back up, then rebuild with the new version and recreate:
  `ACD_CTX_VERSION=<new> docker compose up -d --build`. The store applies its
  schema on start. Take a backup before any upgrade that changes the schema.
- Roll back by starting the previous image tag against the same volume only if
  the schema did not change. Otherwise restore the backup taken before the upgrade.

## Security notes

- **The Host allowlist is not authentication.** It is meant to stop DNS
  rebinding: a request whose `Host` header is `evil.com` is refused (403), which
  is what the tests and the CI smoke test check. Any local process that can reach the port and sends an
  allowed `Host` can use the proxy. That is why the port is published on
  `127.0.0.1` only.
- Do not publish the port on `0.0.0.0` or another interface. The proxy holds
  API credentials in transit. For remote access, put an authenticated TLS
  reverse proxy in front, which this project does not provide.
- The plugin bearer token is written to `/data/.local/share/acd-ctx/plugin.token`
  with mode `0600`. The MITM CA private key is in `/data/ca`. Anyone who can
  read the volume can use both, so protect the volume like the host's home directory.
- The container runs as uid 65532 with a read-only root filesystem, no
  capabilities and `no-new-privileges`. Only `/data` and `/tmp` are writable.
- The image has no shell and no package manager. Debug with
  `docker exec acd-ctx /acd-ctx healthcheck` or `docker logs acd-ctx`.
- The server logs one `SECURITY` warning at startup when it listens on a
  non-loopback address. Treat that line as a deployment check.
