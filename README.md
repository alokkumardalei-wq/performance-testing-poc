# OpenEverest Performance-Testing Plugin (POC)

> Built as the proof of concept accompanying my LFX Mentorship 2026 Term 3
> application for [openeverest/openeverest#2464](https://github.com/openeverest/openeverest/issues/2464).

A working proof-of-concept for [openeverest/openeverest#2464](https://github.com/openeverest/openeverest/issues/2464):
benchmark databases managed by OpenEverest from the dashboard or the CLI — no
manual tool installs, no connection-string archaeology, no Kubernetes wrestling.

Pick a database → choose a **workload profile** → run → see normalized,
**fingerprinted** results you can honestly compare over time.

```
UI (clusterDetailTab) ──┐
                        ├── plugin backend ── Kubernetes Job (sysbench / go-ycsb)
CLI (everest-perf) ─────┘        │                   │
                              SQLite store        target database
```

## What it does

- **Workload profiles instead of tool flags** — Smoke, Read Heavy, Write Heavy,
  Mixed OLTP, Stress, Custom. A profile resolves to an engine-independent
  `RunSpec`; the driver translates it into tool-specific configuration.
- **Two drivers behind one narrow interface** —
  [`sysbench`](backend/internal/driver/sysbench.go) for relational OLTP
  (MySQL, PostgreSQL) and [`go-ycsb`](backend/internal/driver/ycsb.go) for
  cross-engine coverage (MongoDB + MySQL + PostgreSQL). One relational + one
  non-relational engine covered, and two drivers keep the
  [`Driver` interface](backend/internal/driver/driver.go) honest.
- **Execution as short-lived Kubernetes Jobs** with pod **anti-affinity** so
  the load generator stays off the database's node. Whether isolation actually
  held is *recorded per run*, not assumed.
- **Credentials are never persisted** — resolved at run start (the same data
  the Everest credentials endpoint serves), injected via a Job-owned Secret,
  garbage-collected with the Job.
- **Environment fingerprints** — every run stores engine version, topology,
  resources, storage class, driver image, workload parameters and generator
  placement. Comparisons between mismatched fingerprints are shown with an
  explicit "these are not like-for-like" warning listing exactly what differed.
- **Results retained** in SQLite behind a
  [`Store` interface](backend/internal/store/model.go). Ephemeral by default
  (emptyDir — no PVC unless you opt in via chart values), matching the
  maintainers' guidance; the interface is the migration path to external
  PostgreSQL or a plugin CRD later.
- **CI/CD-ready CLI** — `everest-perf run --wait --min-throughput …
  --baseline <id> --max-regression 10` with meaningful exit codes (0 ok,
  2 run failed, 3 threshold/regression violated).

## Repository layout

```
backend/                  Go backend (module github.com/openeverest/plugin-performance/backend)
  main.go                 HTTP server: /main.js, /healthz, /api/...
  internal/driver/        Driver interface + sysbench + go-ycsb
  internal/profile/       Workload profiles → RunSpec
  internal/runner/        Kubernetes Job lifecycle, anti-affinity, placement capture
  internal/store/         Store interface + SQLite implementation
  internal/everest/       Instance discovery + connection/credential resolution
  internal/server/        HTTP handlers, compare + fingerprint diff
  cmd/everest-perf/       The CI/CD CLI
src/                      React frontend (bundled per plugin contract: one ES module)
charts/performance-plugin Helm chart: Deployment, Service, RBAC, Plugin CR, optional PVC
Dockerfile                Backend image (UI embedded via go:embed)
Dockerfile.ycsb           go-ycsb driver image
test-plugin.yaml          Local-dev Plugin CR (externalUrl + vite dev server)
docs/architecture.md      Design decisions and trade-offs
scripts/demo-e2e.sh       End-to-end demo on a kind cluster
```

## Quick start (local, no full Everest install needed)

Prereqs: Go ≥1.25, Node ≥20, Docker, kind, kubectl.

```bash
# 1. Frontend bundle + backend
npm install && npm run build
cp dist/main.js backend/dist/main.js
cd backend && go build -o plugin-backend . && cd ..

# 2. A cluster with demo databases (PostgreSQL + MongoDB) and driver images
./scripts/demo-e2e.sh up

# 3. Run the backend against the cluster (demo-env.sh registers the demo
#    databases as static instances so the UI can target them directly)
source scripts/demo-env.sh && ./backend/plugin-backend

# 4a. UI: dev sandbox at http://localhost:3001/?namespace=default&instance=pg-demo
npm run dev

# 4b. CLI:
go build -o everest-perf ./backend/cmd/everest-perf
./everest-perf run --namespace default --instance pg-demo --profile smoke --wait
```

The demo databases are plain StatefulSets labeled the way the resolver expects
(`standalone mode` also lets you POST explicit connection details — see
`docs/architecture.md#standalone-mode`). Against a real Everest install the
plugin discovers `DatabaseCluster` CRs and resolves credentials from the
engine's user secret, exactly like the Everest credentials endpoint does.

## Installing into OpenEverest

```bash
helm install performance charts/performance-plugin -n everest-system
```

That deploys the backend, its RBAC (the plugin ships its own ServiceAccount +
least-privilege ClusterRole — jobs, pods, pods/log, secrets, databaseclusters)
and the `Plugin` CR (`extensions.openeverest.io/v1alpha1`) pointing at the
backend's Service. The host proxies `/v1/plugins/performance/...` to the
backend and dynamic-import()s `/main.js`, which registers a **Performance**
tab on the database detail page.

For iterating against a local k3d-hosted Everest, `kubectl apply -f
test-plugin.yaml` points the CR at your laptop instead.

## API sketch

| Method & path | Purpose |
|---|---|
| `GET /api/instances` | Everest-managed databases (engine, version, readiness) |
| `GET /api/profiles` | Workload profiles with defaults |
| `GET /api/drivers` | Available drivers and engine support |
| `POST /api/runs` | Start a run (profile + overrides, optional explicit connection) |
| `GET /api/runs?instance=` | Run history |
| `GET /api/runs/{id}` | Run detail: status, normalized result, fingerprint |
| `GET /api/runs/{id}/output` | Raw tool output (the artifact behind the numbers) |
| `POST /api/runs/{id}/cancel` | Cancel a running benchmark |
| `GET /api/compare?a=&b=` | Deltas + fingerprint diff |

## Tests

```bash
cd backend && go test ./...
```

Covers the output parsers (real sysbench/go-ycsb transcripts), script
generation (including "password never appears in the script"), profile
resolution/validation, the SQLite store round-trip, and Job construction
(anti-affinity modes, no-retry policy, Job-owned credential Secret) against a
fake clientset.

## Design notes

The why behind the shape of this POC — single plugin with per-engine drivers,
Jobs as the execution model, fingerprint-gated comparisons, ephemeral-default
storage, and the security caveats of the current plugin proxy — is written up
in [docs/architecture.md](docs/architecture.md).

[docs/demo-script.md](docs/demo-script.md) is a rehearsed ~6-minute demo
walkthrough (UI, CLI, CI regression gate, code tour).
