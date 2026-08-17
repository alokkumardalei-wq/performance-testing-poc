// Package server exposes the plugin's HTTP surface.
//
// Contract with the OpenEverest host (see the generic plugin design doc):
//   - GET /main.js, /icon.png  - the embedded frontend bundle
//   - GET /healthz             - liveness/readiness
//   - /api/...                 - the plugin API, reached through the host
//     proxy at /v1/plugins/performance/api/... with auth attached.
//
// Note: the host proxies GET /v1/plugins/{name}/* without authentication so
// that dynamic import() of the bundle works. Run results are not secrets in
// most deployments, but see docs/architecture.md#security for the trade-off
// and the REQUIRE_USER_HEADER hardening flag.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/openeverest/plugin-performance/backend/internal/driver"
	"github.com/openeverest/plugin-performance/backend/internal/everest"
	"github.com/openeverest/plugin-performance/backend/internal/profile"
	"github.com/openeverest/plugin-performance/backend/internal/runner"
	"github.com/openeverest/plugin-performance/backend/internal/store"
)

// Server wires the plugin API together.
type Server struct {
	store    store.Store
	resolver *everest.Resolver
	runner   *runner.Runner
	registry *driver.Registry
	assets   fs.FS

	// requireUserHeader rejects /api requests without X-Everest-User.
	requireUserHeader bool
}

func New(st store.Store, res *everest.Resolver, run *runner.Runner, reg *driver.Registry, assets embed.FS, requireUserHeader bool) *Server {
	sub, err := fs.Sub(assets, "dist")
	if err != nil {
		sub = assets
	}
	return &Server{store: st, resolver: res, runner: run, registry: reg, assets: sub, requireUserHeader: requireUserHeader}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Frontend bundle + icon.
	fileServer := http.FileServer(http.FS(s.assets))
	mux.Handle("GET /main.js", fileServer)
	mux.Handle("GET /icon.png", fileServer)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/main.js", http.StatusFound)
	})

	// Plugin API.
	mux.HandleFunc("GET /api/instances", s.handleListInstances)
	mux.HandleFunc("GET /api/profiles", s.handleListProfiles)
	mux.HandleFunc("GET /api/drivers", s.handleListDrivers)
	mux.HandleFunc("POST /api/runs", s.handleCreateRun)
	mux.HandleFunc("GET /api/runs", s.handleListRuns)
	mux.HandleFunc("GET /api/runs/{id}", s.handleGetRun)
	mux.HandleFunc("GET /api/runs/{id}/output", s.handleRunOutput)
	mux.HandleFunc("POST /api/runs/{id}/cancel", s.handleCancelRun)
	mux.HandleFunc("DELETE /api/runs/{id}", s.handleDeleteRun)
	mux.HandleFunc("GET /api/compare", s.handleCompare)

	return s.middleware(mux)
}

// middleware adds CORS (for the dev sandbox; in production the host proxy
// makes everything same-origin) and the optional user-header gate.
func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Everest-User")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if s.requireUserHeader && len(r.URL.Path) >= 4 && r.URL.Path[:4] == "/api" {
			if r.Header.Get("X-Everest-User") == "" {
				writeErr(w, http.StatusUnauthorized, errors.New("missing X-Everest-User header"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleListInstances(w http.ResponseWriter, r *http.Request) {
	instances, err := s.resolver.ListInstances(r.Context(), r.URL.Query().Get("namespace"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"instances": instances})
}

func (s *Server) handleListProfiles(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"profiles": profile.List()})
}

func (s *Server) handleListDrivers(w http.ResponseWriter, _ *http.Request) {
	type driverInfo struct {
		Name    string   `json:"name"`
		Image   string   `json:"image"`
		Engines []string `json:"engines"`
	}
	var out []driverInfo
	for _, name := range s.registry.Names() {
		d, _ := s.registry.Get(name)
		var engines []string
		for _, e := range []driver.Engine{driver.EnginePostgreSQL, driver.EngineMySQL, driver.EngineMongoDB} {
			if d.Supports(e) {
				engines = append(engines, string(e))
			}
		}
		out = append(out, driverInfo{Name: name, Image: d.DefaultImage(), Engines: engines})
	}
	writeJSON(w, http.StatusOK, map[string]any{"drivers": out})
}

// createRunRequest is the payload for POST /api/runs.
type createRunRequest struct {
	Namespace    string            `json:"namespace"`
	InstanceName string            `json:"instanceName"`
	Profile      string            `json:"profile"`
	Driver       string            `json:"driver,omitempty"` // default: best driver for engine
	Image        string            `json:"image,omitempty"`  // driver image override
	Overrides    profile.Overrides `json:"overrides"`

	// Connection allows benchmarking a database not managed by Everest
	// (standalone mode / CI against external DBs). When set, Namespace is
	// where the benchmark Job runs and InstanceName is a display name.
	Connection *explicitConnection `json:"connection,omitempty"`
}

type explicitConnection struct {
	Engine   string `json:"engine"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	Database string `json:"database"`
}

func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	var req createRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}
	if req.Namespace == "" || req.InstanceName == "" {
		writeErr(w, http.StatusBadRequest, errors.New("namespace and instanceName are required"))
		return
	}
	if req.Profile == "" {
		req.Profile = "smoke"
	}

	spec, err := profile.Resolve(req.Profile, req.Overrides)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	// Resolve target: Everest-managed instance or explicit connection.
	var conn driver.Connection
	var inst *everest.Instance
	if req.Connection != nil {
		conn = driver.Connection{
			Host: req.Connection.Host, Port: req.Connection.Port,
			User: req.Connection.User, Password: req.Connection.Password,
			Database: req.Connection.Database,
		}
		inst = &everest.Instance{
			Namespace: req.Namespace, Name: req.InstanceName,
			Engine: driver.Engine(req.Connection.Engine),
		}
	} else {
		inst, err = s.resolver.GetInstance(r.Context(), req.Namespace, req.InstanceName)
		if err != nil {
			writeErr(w, http.StatusNotFound, fmt.Errorf("instance not found: %w", err))
			return
		}
		conn, err = s.resolver.Connection(r.Context(), inst)
		if err != nil {
			writeErr(w, http.StatusBadGateway, fmt.Errorf("resolving connection: %w", err))
			return
		}
	}
	if inst.Engine == "" {
		writeErr(w, http.StatusBadRequest, errors.New("could not determine database engine"))
		return
	}

	var d driver.Driver
	if req.Driver != "" {
		d, err = s.registry.Get(req.Driver)
	} else {
		d, err = s.registry.ForEngine(inst.Engine)
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if !d.Supports(inst.Engine) {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("driver %s does not support engine %s", d.Name(), inst.Engine))
		return
	}
	image := req.Image
	if image == "" {
		image = d.DefaultImage()
	}

	run := &store.Run{
		ID:           uuid.NewString(),
		CreatedAt:    time.Now().UTC(),
		Namespace:    req.Namespace,
		InstanceName: req.InstanceName,
		Engine:       string(inst.Engine),
		Driver:       d.Name(),
		Profile:      req.Profile,
		Status:       store.StatusPending,
		Spec:         spec,
		Fingerprint: &store.Fingerprint{
			Engine:        string(inst.Engine),
			EngineVersion: inst.Version,
			Replicas:      inst.Replicas,
			CPULimit:      inst.CPULimit,
			MemoryLimit:   inst.MemoryLimit,
			StorageClass:  inst.StorageClass,
			StorageSize:   inst.StorageSize,
			Driver:        d.Name(),
			DriverImage:   image,
			Spec:          spec,
		},
	}
	if err := s.store.CreateRun(run); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.runner.Start(r.Context(), run, d, conn, image); err != nil {
		run.Status = store.StatusFailed
		run.Message = err.Error()
		_ = s.store.UpdateRun(run)
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	log.Printf("run %s started: %s/%s engine=%s driver=%s profile=%s user=%s",
		run.ID, run.Namespace, run.InstanceName, run.Engine, run.Driver, run.Profile,
		r.Header.Get("X-Everest-User"))
	writeJSON(w, http.StatusCreated, run)
}

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 100
	runs, err := s.store.ListRuns(store.ListFilter{
		Namespace:    q.Get("namespace"),
		InstanceName: q.Get("instance"),
		Limit:        limit,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if runs == nil {
		runs = []*store.Run{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.store.GetRun(r.PathValue("id"))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleRunOutput(w http.ResponseWriter, r *http.Request) {
	raw, err := s.store.GetRawOutput(r.PathValue("id"))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(raw))
}

func (s *Server) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.store.GetRun(r.PathValue("id"))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if run.Status != store.StatusRunning && run.Status != store.StatusPending {
		writeErr(w, http.StatusConflict, fmt.Errorf("run is %s, cannot cancel", run.Status))
		return
	}
	if err := s.runner.Cancel(r.Context(), run); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleDeleteRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.store.GetRun(r.PathValue("id"))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if run.Status == store.StatusRunning {
		_ = s.runner.Cancel(context.Background(), run)
	}
	if err := s.store.DeleteRun(run.ID); err != nil {
		writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// comparison is the payload of GET /api/compare?a=&b=.
type comparison struct {
	A *store.Run `json:"a"`
	B *store.Run `json:"b"`
	// Deltas are percentage changes from A to B (positive = B higher).
	Deltas map[string]float64 `json:"deltas"`
	// Comparable is false when the fingerprints differ; Differences lists
	// what changed so the UI can say *why* the numbers are not comparable.
	Comparable  bool     `json:"comparable"`
	Differences []string `json:"differences"`
}

func (s *Server) handleCompare(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.GetRun(r.URL.Query().Get("a"))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	b, err := s.store.GetRun(r.URL.Query().Get("b"))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if a.Result == nil || b.Result == nil {
		writeErr(w, http.StatusConflict, errors.New("both runs must have results to compare"))
		return
	}

	cmp := comparison{A: a, B: b, Deltas: map[string]float64{}}
	cmp.Deltas["throughputOps"] = pctDelta(a.Result.ThroughputOPS, b.Result.ThroughputOPS)
	cmp.Deltas["latencyAvgMs"] = pctDelta(a.Result.LatencyAvgMs, b.Result.LatencyAvgMs)
	cmp.Deltas["latencyP95Ms"] = pctDelta(a.Result.LatencyP95Ms, b.Result.LatencyP95Ms)
	if a.Result.QPS > 0 && b.Result.QPS > 0 {
		cmp.Deltas["qps"] = pctDelta(a.Result.QPS, b.Result.QPS)
	}
	cmp.Differences = fingerprintDiff(a.Fingerprint, b.Fingerprint)
	cmp.Comparable = len(cmp.Differences) == 0
	writeJSON(w, http.StatusOK, cmp)
}

func pctDelta(a, b float64) float64 {
	if a == 0 {
		return 0
	}
	return (b - a) / a * 100
}

// fingerprintDiff lists human-readable differences between the conditions two
// runs were measured under.
func fingerprintDiff(a, b *store.Fingerprint) []string {
	var diffs []string
	if a == nil || b == nil {
		return []string{"one of the runs has no environment fingerprint"}
	}
	add := func(field, av, bv string) {
		if av != bv {
			diffs = append(diffs, fmt.Sprintf("%s: %q vs %q", field, av, bv))
		}
	}
	add("engine", a.Engine, b.Engine)
	add("engineVersion", a.EngineVersion, b.EngineVersion)
	add("replicas", fmt.Sprint(a.Replicas), fmt.Sprint(b.Replicas))
	add("cpuLimit", a.CPULimit, b.CPULimit)
	add("memoryLimit", a.MemoryLimit, b.MemoryLimit)
	add("storageClass", a.StorageClass, b.StorageClass)
	add("storageSize", a.StorageSize, b.StorageSize)
	add("driver", a.Driver, b.Driver)
	add("driverImage", a.DriverImage, b.DriverImage)
	add("threads", fmt.Sprint(a.Spec.Threads), fmt.Sprint(b.Spec.Threads))
	add("duration", fmt.Sprint(a.Spec.DurationSeconds), fmt.Sprint(b.Spec.DurationSeconds))
	add("readPercent", fmt.Sprint(a.Spec.ReadPercent), fmt.Sprint(b.Spec.ReadPercent))
	add("tables", fmt.Sprint(a.Spec.Tables), fmt.Sprint(b.Spec.Tables))
	add("tableSize", fmt.Sprint(a.Spec.TableSize), fmt.Sprint(b.Spec.TableSize))
	add("records", fmt.Sprint(a.Spec.Records), fmt.Sprint(b.Spec.Records))
	if a.Isolated != nil && b.Isolated != nil && *a.Isolated != *b.Isolated {
		diffs = append(diffs, fmt.Sprintf("isolation: %v vs %v", *a.Isolated, *b.Isolated))
	}
	return diffs
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func writeStoreErr(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeErr(w, http.StatusInternalServerError, err)
}
