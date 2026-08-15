# Source this before starting the backend against the demo cluster:
#   source scripts/demo-env.sh && ./backend/plugin-backend
export PORT=8081
export SYSBENCH_IMAGE=perf-driver-sysbench:0.1.0
export YCSB_IMAGE=perf-driver-ycsb:0.1.0
export STATIC_INSTANCES='[
  {"namespace":"default","name":"pg-demo","engine":"postgresql",
   "host":"pg-demo.default.svc.cluster.local","port":5432,
   "user":"postgres","password":"demo-pg-pass","database":"postgres"},
  {"namespace":"default","name":"mongo-demo","engine":"mongodb",
   "host":"mongo-demo.default.svc.cluster.local","port":27017,
   "user":"admin","password":"demo-mongo-pass","database":"ycsb"}
]'
