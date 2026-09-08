# Sub2API Helm chart

Deploys [Sub2API](https://github.com/Wei-Shaw/sub2api) on Kubernetes 1.23+.

The chart deploys the gateway only. **PostgreSQL is always external** — point
`postgresql.host` at an instance you already run. Redis can either be the
bundled single-node StatefulSet (default) or an external one.

```bash
helm install sub2api ./deploy/helm/sub2api \
  --namespace sub2api --create-namespace \
  --set postgresql.host=postgres.databases.svc.cluster.local \
  --set auth.databasePassword="$PGPASSWORD" \
  --set auth.redisPassword="$(openssl rand -hex 16)" \
  --set auth.jwtSecret="$(openssl rand -hex 32)" \
  --set auth.totpEncryptionKey="$(openssl rand -hex 32)"
```

## Requirements

| Requirement | Notes |
|---|---|
| Kubernetes | 1.23 or newer |
| PostgreSQL | 14 or newer, reachable at `postgresql.host`, with a role that may create objects in `postgresql.database` — the application applies its own migrations at startup |
| Redis | 6 or newer — bundled by default, or external via `redis.enabled=false` + `redis.host` |
| Storage | one ReadWriteOnce PVC for `/app/data` (plus one for the bundled Redis) |

## Required values

The chart refuses to render with a clear message when any of these is missing.

| Value | Required when | How to produce it |
|---|---|---|
| `postgresql.host` | always | hostname or Service name of your PostgreSQL |
| `auth.existingSecret` **or** the `auth.*` values below | always | pick one of the two credential paths |
| `auth.databasePassword` | no `existingSecret` | the password of `postgresql.username` |
| `auth.jwtSecret` | no `existingSecret` | `openssl rand -hex 32` |
| `auth.totpEncryptionKey` | no `existingSecret` | `openssl rand -hex 32` — an AES-256 key, so exactly 64 hex characters |
| `auth.redisPassword` | no `existingSecret` and `redis.enabled=true` | `openssl rand -hex 16` |
| `redis.host` | `redis.enabled=false` | hostname or Service name of your Redis |

`auth.adminPassword` is optional. Left empty, the application generates the
bootstrap admin password on the first run and prints it once in the Pod log.

`jwtSecret` and `totpEncryptionKey` must be **stable across restarts**. A new
`jwtSecret` logs every user out; a new `totpEncryptionKey` invalidates every
stored TOTP enrolment.

## Credentials

Either let the chart create the Secret from `auth.*` values, or manage the
Secret yourself and name it in `auth.existingSecret`. With `existingSecret` set,
the chart writes no credential of its own and never rolls the Pod when the
Secret changes, which is what lets External Secrets Operator, sealed-secrets or
SOPS own it.

The keys the chart reads are configurable under `auth.keys`; by default they are
named after the environment variables the application expects:

```yaml
auth:
  existingSecret: sub2api-credentials
  keys:
    databasePassword: DATABASE_PASSWORD
    redisPassword: REDIS_PASSWORD          # optional for an external Redis
    jwtSecret: JWT_SECRET
    totpEncryptionKey: TOTP_ENCRYPTION_KEY
    adminPassword: ADMIN_PASSWORD          # optional
```

## External PostgreSQL and Redis

`ci/external-datastores-values.yaml` is a complete worked example:

```yaml
postgresql:
  host: pg-primary.databases.example.com
  port: 5432
  database: sub2api
  username: sub2api
  sslMode: verify-full     # passed straight into the pgx DSN

redis:
  enabled: false
  host: redis-master.cache.example.com
  port: 6379
  username: sub2api        # Redis ACL user; empty means the default user
  enableTls: true

auth:
  existingSecret: sub2api-credentials
```

```bash
helm template sub2api ./deploy/helm/sub2api \
  --namespace sub2api \
  --values ./deploy/helm/sub2api/ci/external-datastores-values.yaml
```

## What it deploys

| Object | Condition |
|---|---|
| `Deployment/<fullname>` | always — `replicas: 1`, `strategy: Recreate` |
| `Service/<fullname>` | always — port `8080` |
| `ConfigMap/<fullname>-env` | always — non-secret environment, hashed into a Pod annotation |
| `ServiceAccount/<fullname>` | `serviceAccount.create` |
| `Secret/<fullname>-auth` | only when `auth.existingSecret` is empty |
| `PersistentVolumeClaim/<fullname>-data` | `persistence.enabled` and no `persistence.existingClaim` |
| `StatefulSet/<fullname>-redis` + `Service` | `redis.enabled` |
| `Ingress/<fullname>` | `ingress.enabled` |

No namespace, NetworkPolicy or database object is created.

## Behaviour worth knowing

- `AUTO_SETUP=true` is always set: a container deployment has no interactive
  setup wizard. On the first run the application applies its SQL migrations,
  writes `config.yaml` onto the data volume and creates the admin account.
- Migrations run in-process with bounded exponential backoff while PostgreSQL
  is still coming up. The startup probe budget is therefore wide (10 minutes by
  default) and the liveness probe is deliberately slack, so that a database
  recovery window does not read as a permanent process failure.
- Configuration precedence upstream is **environment > `config.yaml` >
  defaults**, so everything rendered into the ConfigMap keeps winning over the
  `config.yaml` written on the first run.
- The Pod runs as uid/gid 1000, the `sub2api` account the image creates. The
  image entrypoint only chowns `/app/data` when it starts as root, so entering
  as 1000 execs the binary directly — which is what the `restricted` Pod
  Security Standard requires.
- Single replica. `/app/data` is ReadWriteOnce and upstream documents no
  multi-instance mode; `strategy: Recreate` avoids a rolling update deadlocking
  on that volume.
- The bundled Redis is a convenience for small installations, not a highly
  available Redis. Use `redis.enabled=false` with a managed service or a
  dedicated Redis chart for anything else.

## Images

`image.repository` defaults to the image this fork publishes from
`.github/workflows/ghcr-publish.yml`. Upstream's own images are
`docker.io/weishaw/sub2api` and `ghcr.io/wei-shaw/sub2api`.

`image.tag` empty means `.Chart.AppVersion`. Set `image.digest` to pin by
digest instead, which is recommended for production; the digest wins over the
tag.

## Values

Every key is documented inline in [`values.yaml`](values.yaml).

## Testing a release

```bash
helm test sub2api --namespace sub2api
```

runs a Pod that fetches `/health` through the Service.
