// everest-perf-backend is the OpenEverest performance-testing plugin backend.
//
// Contract with the host (any HTTP server works; this one is Go):
//   GET /main.js  -> embedded frontend bundle (dynamic import()'d by the UI)
//   GET /healthz  -> liveness
//   /api/...      -> plugin API, reached via /v1/plugins/performance/api/...
package main

import (
	"embed"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/openeverest/plugin-performance/backend/internal/driver"
	"github.com/openeverest/plugin-performance/backend/internal/everest"
	"github.com/openeverest/plugin-performance/backend/internal/runner"
	"github.com/openeverest/plugin-performance/backend/internal/server"
	"github.com/openeverest/plugin-performance/backend/internal/store"
)

//go:embed dist
var assets embed.FS

func main() {
	port := envOr("PORT", "8080")

	// --- Kubernetes clients: in-cluster first, kubeconfig for local dev.
	cfg, err := rest.InClusterConfig()
	if err != nil {
		kubeconfig := os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			home, _ := os.UserHomeDir()
			kubeconfig = filepath.Join(home, ".kube", "config")
		}
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			log.Fatalf("no in-cluster config and no kubeconfig: %v", err)
		}
	}
	core, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("building core client: %v", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("building dynamic client: %v", err)
	}

	// --- Store: SQLite on DATA_DIR. The chart mounts an emptyDir there by
	// default (ephemeral, per design) and a PVC when persistence is enabled.
	dataDir := envOr("DATA_DIR", os.TempDir())
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		log.Fatalf("creating data dir: %v", err)
	}
	st, err := store.NewSQLite(filepath.Join(dataDir, "everest-perf.db"))
	if err != nil {
		log.Fatalf("opening store: %v", err)
	}
	defer st.Close()

	// --- Drivers. Images overridable for air-gapped installs.
	registry := driver.NewRegistry(
		driver.NewSysbench(os.Getenv("SYSBENCH_IMAGE")),
		driver.NewYCSB(os.Getenv("YCSB_IMAGE")),
	)

	// --- Runner.
	runnerCfg := runner.DefaultConfig()
	if m := os.Getenv("ISOLATION_MODE"); m != "" {
		runnerCfg.Isolation = runner.IsolationMode(m)
	}
	run := runner.New(core, st, runnerCfg)
	run.Resume(registry)

	// STATIC_INSTANCES: JSON array of pre-registered databases (demo
	// environments, non-Everest databases). Example:
	// [{"namespace":"default","name":"pg-demo","engine":"postgresql",
	//   "host":"pg-demo.default.svc.cluster.local","port":5432,
	//   "user":"postgres","password":"...","database":"postgres"}]
	var static []everest.StaticInstance
	if raw := os.Getenv("STATIC_INSTANCES"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &static); err != nil {
			log.Fatalf("parsing STATIC_INSTANCES: %v", err)
		}
		log.Printf("registered %d static instance(s)", len(static))
	}
	resolver := everest.NewResolver(dyn, core, static)
	srv := server.New(st, resolver, run, registry, assets, os.Getenv("REQUIRE_USER_HEADER") == "true")

	httpSrv := &http.Server{
		Addr:              ":" + port,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("everest-perf plugin backend listening on :%s (isolation=%s, data=%s)",
		port, runnerCfg.Isolation, dataDir)
	log.Fatal(httpSrv.ListenAndServe())
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
