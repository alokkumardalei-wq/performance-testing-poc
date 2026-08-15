# Demo video script (~5–7 minutes)

A recordable, repeatable flow. Everything below was verified working on
2026-08-14. Practice once before recording; the benchmark segments use short
20-second runs so the video keeps moving.

## Prep (before recording)

```bash
cd performance-plugin-poc

# 1. Demo cluster + databases + driver images (one-time, ~5 min)
./scripts/demo-e2e.sh up

# 2. Build everything
npm install && npm run build && cp dist/main.js backend/dist/main.js
cd backend && go build -o plugin-backend . && cd ..
go build -o everest-perf ./backend/cmd/everest-perf

# 3. Start the backend (leave running in its own terminal). demo-env.sh
#    registers pg-demo/mongo-demo as static instances so UI runs work
#    without Everest CRDs on the demo cluster.
source scripts/demo-env.sh && ./backend/plugin-backend

# 4. Start the UI sandbox (second terminal)
npm run dev
```

Optional: pre-seed one finished PostgreSQL run so the UI isn't empty on
camera (run scene 3's CLI command once before recording).

> Tip: close other kind/k3d clusters first — benchmark pods schedule in
> seconds on a quiet Docker VM and in minutes on a loaded one.

## Scene 1 — the problem (30 s, slides or talking head)

"You deployed a database through OpenEverest. Does it actually perform?
Today that means installing benchmarking tools, digging out connection
strings, and fighting Kubernetes networking. This plugin makes it: pick a
database, pick a workload, run, compare."

## Scene 2 — architecture at a glance (45 s)

Show the diagram in `docs/architecture.md`. Talking points, in order:
- One plugin, per-engine **drivers** behind a two-method interface
  (sysbench for relational OLTP, go-ycsb across MongoDB/MySQL/PostgreSQL).
- Runs are **Kubernetes Jobs** with anti-affinity off the database's node.
- Credentials live in a **Job-owned Secret**, never persisted.
- Every result carries an **environment fingerprint** — comparisons warn
  when conditions differed.

## Scene 3 — UI flow (2 min)

Open `http://localhost:3001/?namespace=default&instance=pg-demo`.

1. **Runs list** — point out status chips, throughput/p95 columns, and the
   two trend charts (throughput, latency p95) once ≥2 runs exist.
2. Click **New run** — the profile cards ARE the story: "users pick a
   workload shape, not sysbench flags." Pick *Smoke Test*, set duration 20 s,
   threads 2. Start.
3. While it runs (~1–2 min incl. prepare): click into the run — show the
   live status, then the finished view: stat tiles (ops/s, p95), the
   **environment fingerprint** table (call out `isolated: no` on the
   single-node demo cluster — "the plugin refuses to pretend a co-located
   result is clean"), and the raw tool output accordion ("normalization
   never destroys the artifact").
4. Select two runs → **Compare** — show the delta table; if you compare
   across different conditions, show the yellow "not like-for-like" warning
   listing exactly what differed.

## Scene 4 — CLI / CI-CD flow (1.5 min)

```bash
# MongoDB this time — the non-relational engine, via go-ycsb
./everest-perf run \
  --namespace default --instance mongo-demo --profile read_heavy \
  --threads 4 --duration 20 \
  --engine mongodb --host mongo-demo.default.svc.cluster.local --port 27017 \
  --user admin --password demo-mongo-pass --database ycsb \
  --wait
```

Narrate while it waits: "same API the UI uses; in a real install this goes
through the Everest proxy with a bearer token." Show the result block, then
the CI gate:

```bash
# Regression gate against a stored baseline — exit code 3 on regression
./everest-perf run --namespace default --instance pg-demo --profile smoke \
  --threads 2 --duration 20 \
  --engine postgresql --host pg-demo.default.svc.cluster.local --port 5432 \
  --user postgres --password demo-pg-pass --database postgres \
  --wait --baseline <RUN_ID_FROM_SCENE_3> --max-regression 10
echo "exit: $?"
```

## Scene 5 — what's under the hood (1 min, code tour)

Three files, ~15 s each:
- `backend/internal/driver/driver.go` — the whole driver contract fits on
  one screen.
- `backend/internal/runner/runner.go` — buildJob: anti-affinity term,
  backoffLimit 0, Job-owned credentials Secret.
- `charts/performance-plugin/templates/plugin-cr.yaml` — the Plugin CR:
  "installing this is one helm install."

Close with the verified findings (see README): the go-ycsb
`maxexecutiontime` bug and the SIGINT watchdog fix — "found because the POC
was actually run, not just written."

## Scene 6 — wrap (20 s)

Expected-outcomes checklist from the issue, each ticked with what the video
just showed. End on the mentorship roadmap slide.

## Reset between takes

```bash
curl -s http://127.0.0.1:8081/api/runs | python3 -c \
  'import json,sys;[print(r["id"]) for r in json.load(sys.stdin)["runs"]]' |
  xargs -I{} curl -s -X DELETE http://127.0.0.1:8081/api/runs/{}
kubectl --context kind-everest-perf-demo delete job -l app.kubernetes.io/name=everest-perf-run --ignore-not-found
```
