#!/usr/bin/env bash
# End-to-end demo environment: kind cluster + demo databases + driver images.
#   ./scripts/demo-e2e.sh up     create everything
#   ./scripts/demo-e2e.sh down   delete the cluster
set -euo pipefail

CLUSTER=everest-perf-demo
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SYSBENCH_IMG=perf-driver-sysbench:0.1.0
YCSB_IMG=perf-driver-ycsb:0.1.0

case "${1:-up}" in
  up)
    if ! kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
      kind create cluster --name "$CLUSTER" --wait 120s
    fi

    echo ">>> building driver images"
    docker build -t "$SYSBENCH_IMG" -f "$ROOT/Dockerfile.sysbench" "$ROOT"
    docker build -t "$YCSB_IMG" -f "$ROOT/Dockerfile.ycsb" "$ROOT"
    kind load docker-image --name "$CLUSTER" "$SYSBENCH_IMG" "$YCSB_IMG"

    echo ">>> deploying demo databases"
    kubectl --context "kind-$CLUSTER" apply -f "$ROOT/scripts/demo-dbs.yaml"
    kubectl --context "kind-$CLUSTER" rollout status statefulset/pg-demo -n default --timeout=180s
    kubectl --context "kind-$CLUSTER" rollout status statefulset/mongo-demo -n default --timeout=180s

    cat <<EOF

Demo environment ready. Run the plugin backend against it:

  SYSBENCH_IMAGE=$SYSBENCH_IMG YCSB_IMAGE=$YCSB_IMG \\
  PORT=8081 ./backend/plugin-backend

Databases (standalone-mode connections):
  postgresql  host=pg-demo.default.svc.cluster.local    port=5432  user=postgres password=demo-pg-pass    db=postgres
  mongodb     host=mongo-demo.default.svc.cluster.local port=27017 user=admin    password=demo-mongo-pass
EOF
    ;;
  down)
    kind delete cluster --name "$CLUSTER"
    ;;
  *)
    echo "usage: $0 up|down" >&2; exit 1;;
esac
