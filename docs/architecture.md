# Architecture & design decisions

This POC targets OpenEverest's v2 generic plugin framework (issue #2464). The
design follows the public discussion on that issue; where maintainers stated a
preference, the POC implements it.

## The shape: one plugin, per-engine drivers

Profiles, scheduling, the result schema, the store and the comparison logic
are all engine-independent; only a driver knows about sysbench or go-ycsb.
Per-technology plugins would duplicate all of that shared machinery. The
counter-argument (RBAC isolation, independent release cadence) gets stronger
only if benchmark Jobs ever need broad permissions — they don't (see RBAC
below).

```
        ┌────────────────────────── plugin backend ─────────────────────────┐
        │  profiles ──► RunSpec ──► Driver.BuildScript ──► Job (K8s)        │
        │                              ▲                     │ logs         │
        │  REST API ◄── Store (SQLite) └─ Driver.ParseOutput ┘              │
        └───────────────────────────────────────────────────────────────────┘
```

The `Driver` contract is deliberately narrow — `BuildScript(engine, conn,
spec)` and `ParseOutput(log)` — so adding pgbench or HammerDB touches nothing
but a new file and a registry entry. Two drivers exist from day one because a
single implementation never proves an interface is an interface.

### Driver choice

- **go-ycsb**: one static Go binary that speaks MongoDB, MySQL and PostgreSQL.
  It alone satisfies the "one relational + one non-relational engine"
  requirement, and its image is trivial to build/air-gap.
- **sysbench**: the de-facto standard for relational OLTP with much
  higher-fidelity transactional workloads (oltp_read_only / read_write /
  write_only lua scripts). Used by default for MySQL/PostgreSQL.

## Workload profiles

Users pick *what they want to simulate* (Smoke, Read Heavy, Write Heavy, Mixed
OLTP, Stress, Custom), not tool flags. A profile resolves to an
engine-independent `RunSpec` (threads, duration, read/write mix, dataset
size); every knob remains overridable per run. The driver maps the mix onto
tool concepts (sysbench lua script selection; YCSB read/update proportions).
Maintainer feedback endorsed exactly this abstraction.

## Execution model: Jobs, and why state lives outside the event stream

Each run is one Kubernetes Job: `restartPolicy: Never`, `backoffLimit: 0` (a
crashed benchmark is a failed run, not something to retry silently),
`activeDeadlineSeconds` derived from the workload duration plus a prepare
grace, `ttlSecondsAfterFinished` for debuggability-then-cleanup. The runner
polls the Job and mirrors state into the store — the Job is the source of
truth. (The v2 SSE event stream currently offers no replay, so run state
cannot safely live there; polling the Job is the robust choice.)

After a backend restart, watchers are re-attached to any run still marked
`running` (`Runner.Resume`) — the Jobs kept going; only the goroutines were
lost.

### Load-generator isolation

A generator sharing CPU with the process it measures produces a number not
worth storing. The Job carries pod anti-affinity against the target's
`app.kubernetes.io/instance` label, in three modes (chart value):

- `preferred` (default): best effort — works on single-node dev clusters.
- `required`: refuse to co-locate; scheduling fails instead.
- `off`.

Crucially, the *outcome* is recorded: the run's fingerprint stores the
generator's node, the database pods' nodes, and an `isolated` boolean. The UI
warns on non-isolated results instead of pretending they're clean.

## Credentials

Resolved at run start from the same source the Everest credentials endpoint
uses (the engine's user secret + `status.hostname`/`status.port` on the
DatabaseCluster CR; on v2 this maps to `GET .../instances/{name}/connection`).
They are:

- injected into the Job through a run-scoped Secret (never in the pod spec or
  the script — scripts reference `$DB_PASSWORD`),
- owned by the Job via ownerReference, so GC deletes them together,
- never written to the run store, never returned by any API.

## Results: normalize + retain the raw artifact

Every driver maps its output onto one schema (throughput, avg/p95/p99
latency, total ops, errors, per-operation breakdown). The tool's raw stdout is
kept (truncated at 512 KiB) as the artifact behind the numbers — normalization
should never destroy evidence. Output markers
(`===EVEREST-PERF-RESULT-BEGIN/END===`) separate the measurement section from
prepare/load noise.

### Fingerprints make comparison honest

A number from last week only means something if the conditions match. Each run
stores: engine + version, replicas, DB cpu/memory limits, storage class/size,
driver + image, every workload parameter, and generator placement.
`GET /api/compare` returns deltas **plus** the fingerprint diff; the UI draws
the comparison but flags "measured under different conditions" with the exact
list. The CLI's `--baseline` gate prints the same warning. Without this, the
tool eventually shows someone a 30% regression that was really a
storage-class change.

## Storage: ephemeral by default, interface for later

Per maintainer guidance: no PVC by default, no ConfigMaps/etcd as a results
database. SQLite (pure-Go driver, CGO-free image) on an emptyDir gives full
query capability while the pod lives; `persistence.enabled=true` opts into a
PVC; the `Store` interface is the seam for external PostgreSQL ("store results
in a Postgres deployed by Everest") or a plugin CRD once
`spec.customResources[]` exists. Retention is therefore a deployment choice,
and the one-off "just benchmark it once" use case costs nothing.

## Plugin-framework integration

- **Manifest**: `Plugin` CR (`extensions.openeverest.io/v1alpha1`) rendered by
  the Helm chart; `backend.serviceRef` → the plugin Service;
  `frontend.bundlePath: /main.js` served by the backend itself via `go:embed`.
- **Frontend**: one ES-module bundle; the host dynamic-import()s it and calls
  `register(api)`. The component handed to the host is a thin wrapper built
  with the host's React that mounts the plugin's own React/MUI tree inside a
  div (micro-frontend pattern — avoids the dual-React hooks crash). `api.fetch`
  is passed down via props → React context inside the plugin's own tree, not a
  window global.
- **Extension point**: `clusterDetailTab` at path `performance`. The CR's
  `providers` field is deliberately omitted: on release-2.0 the host filter
  compares against Provider CR names while the docs say engine types, so a
  documented value silently hides the tab. Engine compatibility is handled
  inside the plugin instead (and drivers are chosen server-side anyway).
- **CLI**: `everest-perf` speaks the same plugin API, either directly against
  the Service (in-cluster CI runners) or through the host proxy
  (`--url https://everest/…/v1/plugins/performance --token $EVEREST_TOKEN`).
  The Plugin CR's `spec.cli` hook (`everestctl extension run`) can wrap the
  same binary once that surface stabilizes.

### RBAC

The chart ships a ServiceAccount + least-privilege ClusterRole (cluster-scoped
because benchmark Jobs run in the *database's* namespace): create/manage
`jobs`, read `pods` + `pods/log`, `secrets` get/create/delete, read
`databaseclusters`. Nothing writes to Everest resources. This answers the
issue thread's open question ("should plugins that create Jobs ship their own
RBAC?") the same way the existing plugin examples do: yes, from the plugin's
chart.

## Security notes (current framework, worth knowing)

- The host proxies `GET /v1/plugins/{name}/*` **without authentication** so
  that bundle `import()` works. Consequence: anything this backend serves on
  GET is reachable unauthenticated through the host. Benchmark results are
  rarely sensitive, but deployments that care can set
  `requireUserHeader=true` (the chart value) to reject API requests missing
  the `X-Everest-User` header, and a follow-up upstream discussion about
  authenticating non-bundle GETs is warranted.
- Everything mutating (run creation/cancel/delete) is POST/DELETE, which the
  host does authenticate and RBAC-gate (`plugin/<name>` + `use`).

## Standalone mode

`POST /api/runs` accepts an explicit `connection {engine, host, port, user,
password, database}`. This exists for two real cases: CI pipelines
benchmarking databases not managed by Everest, and demoing/developing the
plugin without a full Everest control plane. The connection is used to build
the Job's credential Secret and then discarded, same as the managed path.

## Known POC limitations (deliberate scope cuts)

- No scheduled/recurring runs — the framework pieces (daemon mode, service
  tokens) aren't merged on release-2.0; runs are on-demand from UI/CLI/CI.
- sysbench's MySQL path assumes the target database (default `sbtest`)
  exists; go-ycsb creates its own table/collection.
- go-ycsb credentials use dedicated properties (no URL-encoding pitfalls); sysbench passwords pass via env only.
- Latency percentiles are per-tool aggregates (sysbench reports p95;
  go-ycsb per-op p95/p99 are count-weighted for the rollup).
- One replica backend with SQLite; HA would move the store external first.
