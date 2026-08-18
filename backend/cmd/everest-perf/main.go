// everest-perf is the CI/CD-facing CLI for the performance-testing plugin.
//
// It talks to the same plugin API the UI uses - either directly
// (--url http://perf-backend:8080) or through the OpenEverest host proxy
// (--url https://everest.example.com/v1/plugins/performance --token $TOKEN).
//
// Exit codes (for pipelines):
//
//	0  success (thresholds met, if any)
//	1  usage / transport / server error
//	2  benchmark run failed
//	3  threshold violated or regression against baseline
//
// Examples:
//
//	everest-perf run --namespace prod --instance pg-main --profile read_heavy \
//	    --wait --json
//	everest-perf run --namespace prod --instance pg-main --profile mixed_oltp \
//	    --wait --baseline 4f6f… --max-regression 10
//	everest-perf list --namespace prod --instance pg-main
//	everest-perf compare --a <id> --b <id>
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type client struct {
	base  string
	token string
	http  *http.Client
}

func (c *client) do(method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, strings.TrimRight(c.base, "/")+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 4*1024*1024))
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return fmt.Errorf("%s", e.Error)
		}
		return fmt.Errorf("%s %s: %s", method, path, res.Status)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Wire-format mirrors (subset) of the backend types.
type result struct {
	ThroughputOPS float64 `json:"throughputOps"`
	QPS           float64 `json:"qps,omitempty"`
	LatencyAvgMs  float64 `json:"latencyAvgMs"`
	LatencyP95Ms  float64 `json:"latencyP95Ms"`
	TotalOps      int64   `json:"totalOps"`
	Errors        int64   `json:"errors"`
}

type run struct {
	ID           string  `json:"id"`
	CreatedAt    string  `json:"createdAt"`
	Namespace    string  `json:"namespace"`
	InstanceName string  `json:"instanceName"`
	Engine       string  `json:"engine"`
	Driver       string  `json:"driver"`
	Profile      string  `json:"profile"`
	Status       string  `json:"status"`
	Message      string  `json:"message,omitempty"`
	Result       *result `json:"result,omitempty"`
}

type comparison struct {
	Deltas      map[string]float64 `json:"deltas"`
	Comparable  bool               `json:"comparable"`
	Differences []string           `json:"differences"`
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "run":
		err = cmdRun(args)
	case "list":
		err = cmdList(args)
	case "get":
		err = cmdGet(args)
	case "compare":
		err = cmdCompare(args)
	default:
		usage()
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		// exitCodeError carries a specific pipeline exit code.
		var ec exitCodeError
		if ok := asExitCode(err, &ec); ok {
			os.Exit(ec.code)
		}
		os.Exit(1)
	}
}

type exitCodeError struct {
	code int
	msg  string
}

func (e exitCodeError) Error() string { return e.msg }

func asExitCode(err error, out *exitCodeError) bool {
	if e, ok := err.(exitCodeError); ok {
		*out = e
		return true
	}
	return false
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	base := fs.String("url", envOr("EVEREST_PERF_URL", "http://127.0.0.1:8081"), "plugin API base URL")
	token := fs.String("token", os.Getenv("EVEREST_TOKEN"), "bearer token (Everest proxy)")
	jsonOut := fs.Bool("json", false, "print JSON")
	ns := fs.String("namespace", "", "instance namespace (required)")
	instance := fs.String("instance", "", "instance name (required)")
	prof := fs.String("profile", "smoke", "workload profile")
	driverName := fs.String("driver", "", "driver override (sysbench|go-ycsb)")
	threads := fs.Int("threads", 0, "threads override")
	duration := fs.Int("duration", 0, "duration seconds override")
	skipPrepare := fs.Bool("skip-prepare", false, "reuse existing dataset")
	wait := fs.Bool("wait", false, "block until the run finishes")
	timeout := fs.Duration("timeout", 2*time.Hour, "max time to wait with --wait")
	minThroughput := fs.Float64("min-throughput", 0, "fail (exit 3) if ops/s below this")
	maxP95 := fs.Float64("max-p95", 0, "fail (exit 3) if p95 latency (ms) above this")
	baseline := fs.String("baseline", "", "run ID to compare against after finishing")
	maxRegression := fs.Float64("max-regression", 10, "allowed throughput drop vs baseline, percent")
	// Standalone mode: benchmark a database not managed by Everest.
	connEngine := fs.String("engine", "", "standalone: engine (postgresql|mysql|mongodb)")
	connHost := fs.String("host", "", "standalone: database host")
	connPort := fs.Int("port", 0, "standalone: database port")
	connUser := fs.String("user", "", "standalone: database user")
	connPassword := fs.String("password", os.Getenv("EVEREST_PERF_DB_PASSWORD"), "standalone: database password (or $EVEREST_PERF_DB_PASSWORD)")
	connDatabase := fs.String("database", "", "standalone: database name")
	_ = fs.Parse(args)

	if *ns == "" || *instance == "" {
		return fmt.Errorf("--namespace and --instance are required")
	}
	c := &client{base: *base, token: *token, http: &http.Client{Timeout: 60 * time.Second}}

	overrides := map[string]any{}
	if *threads > 0 {
		overrides["threads"] = *threads
	}
	if *duration > 0 {
		overrides["durationSeconds"] = *duration
	}
	if *skipPrepare {
		overrides["skipPrepare"] = true
	}
	payload := map[string]any{
		"namespace": *ns, "instanceName": *instance, "profile": *prof, "overrides": overrides,
	}
	if *driverName != "" {
		payload["driver"] = *driverName
	}
	if *connHost != "" {
		payload["connection"] = map[string]any{
			"engine": *connEngine, "host": *connHost, "port": *connPort,
			"user": *connUser, "password": *connPassword, "database": *connDatabase,
		}
	}

	var r run
	if err := c.do("POST", "/api/runs", payload, &r); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "run %s started (profile=%s driver=%s)\n", r.ID, r.Profile, r.Driver)

	if !*wait {
		return output(&r, *jsonOut)
	}

	deadline := time.Now().Add(*timeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for run %s", r.ID)
		}
		time.Sleep(5 * time.Second)
		if err := c.do("GET", "/api/runs/"+r.ID, nil, &r); err != nil {
			return err
		}
		if r.Status == "succeeded" || r.Status == "failed" || r.Status == "canceled" {
			break
		}
		fmt.Fprintf(os.Stderr, "  status: %s\n", r.Status)
	}

	if err := output(&r, *jsonOut); err != nil {
		return err
	}
	if r.Status != "succeeded" {
		return exitCodeError{2, fmt.Sprintf("run %s: %s", r.Status, r.Message)}
	}

	// Threshold gates.
	if *minThroughput > 0 && r.Result.ThroughputOPS < *minThroughput {
		return exitCodeError{3, fmt.Sprintf("throughput %.1f ops/s below threshold %.1f", r.Result.ThroughputOPS, *minThroughput)}
	}
	if *maxP95 > 0 && r.Result.LatencyP95Ms > *maxP95 {
		return exitCodeError{3, fmt.Sprintf("p95 latency %.2f ms above threshold %.2f", r.Result.LatencyP95Ms, *maxP95)}
	}

	// Baseline regression gate.
	if *baseline != "" {
		var cmp comparison
		if err := c.do("GET", "/api/compare?a="+*baseline+"&b="+r.ID, nil, &cmp); err != nil {
			return err
		}
		if !cmp.Comparable {
			fmt.Fprintln(os.Stderr, "warning: baseline was measured under different conditions:")
			for _, d := range cmp.Differences {
				fmt.Fprintln(os.Stderr, "  -", d)
			}
		}
		if delta := cmp.Deltas["throughputOps"]; delta < -*maxRegression {
			return exitCodeError{3, fmt.Sprintf("throughput regression %.1f%% vs baseline exceeds allowed %.1f%%", -delta, *maxRegression)}
		}
		fmt.Fprintf(os.Stderr, "baseline check ok (throughput Δ %.1f%%)\n", cmp.Deltas["throughputOps"])
	}
	return nil
}

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	base := fs.String("url", envOr("EVEREST_PERF_URL", "http://127.0.0.1:8081"), "plugin API base URL")
	token := fs.String("token", os.Getenv("EVEREST_TOKEN"), "bearer token")
	jsonOut := fs.Bool("json", false, "print JSON")
	ns := fs.String("namespace", "", "filter by namespace")
	instance := fs.String("instance", "", "filter by instance")
	_ = fs.Parse(args)

	c := &client{base: *base, token: *token, http: &http.Client{Timeout: 60 * time.Second}}
	var resp struct {
		Runs []run `json:"runs"`
	}
	q := "/api/runs?namespace=" + *ns + "&instance=" + *instance
	if err := c.do("GET", q, nil, &resp); err != nil {
		return err
	}
	if *jsonOut {
		return json.NewEncoder(os.Stdout).Encode(resp.Runs)
	}
	fmt.Printf("%-38s %-12s %-10s %-10s %12s %10s\n", "ID", "PROFILE", "DRIVER", "STATUS", "OPS/S", "P95(ms)")
	for _, r := range resp.Runs {
		tput, p95 := "-", "-"
		if r.Result != nil {
			tput = fmt.Sprintf("%.1f", r.Result.ThroughputOPS)
			p95 = fmt.Sprintf("%.2f", r.Result.LatencyP95Ms)
		}
		fmt.Printf("%-38s %-12s %-10s %-10s %12s %10s\n", r.ID, r.Profile, r.Driver, r.Status, tput, p95)
	}
	return nil
}

func cmdGet(args []string) error {
	fs := flag.NewFlagSet("get", flag.ExitOnError)
	base := fs.String("url", envOr("EVEREST_PERF_URL", "http://127.0.0.1:8081"), "plugin API base URL")
	token := fs.String("token", os.Getenv("EVEREST_TOKEN"), "bearer token")
	jsonOut := fs.Bool("json", true, "print JSON")
	id := fs.String("id", "", "run ID (required)")
	_ = fs.Parse(args)
	if *id == "" {
		return fmt.Errorf("--id is required")
	}
	c := &client{base: *base, token: *token, http: &http.Client{Timeout: 60 * time.Second}}
	var r run
	if err := c.do("GET", "/api/runs/"+*id, nil, &r); err != nil {
		return err
	}
	return output(&r, *jsonOut)
}

func cmdCompare(args []string) error {
	fs := flag.NewFlagSet("compare", flag.ExitOnError)
	base := fs.String("url", envOr("EVEREST_PERF_URL", "http://127.0.0.1:8081"), "plugin API base URL")
	token := fs.String("token", os.Getenv("EVEREST_TOKEN"), "bearer token")
	a := fs.String("a", "", "baseline run ID")
	b := fs.String("b", "", "candidate run ID")
	_ = fs.Parse(args)
	if *a == "" || *b == "" {
		return fmt.Errorf("--a and --b are required")
	}
	c := &client{base: *base, token: *token, http: &http.Client{Timeout: 60 * time.Second}}
	var cmp comparison
	if err := c.do("GET", "/api/compare?a="+*a+"&b="+*b, nil, &cmp); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(cmp)
}

func output(r *run, jsonOut bool) error {
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	fmt.Printf("run %s  [%s]\n", r.ID, r.Status)
	if r.Result != nil {
		fmt.Printf("  throughput: %.1f ops/s\n", r.Result.ThroughputOPS)
		if r.Result.QPS > 0 {
			fmt.Printf("  queries:    %.1f q/s\n", r.Result.QPS)
		}
		fmt.Printf("  latency:    avg %.2f ms · p95 %.2f ms\n", r.Result.LatencyAvgMs, r.Result.LatencyP95Ms)
		fmt.Printf("  total ops:  %d (errors: %d)\n", r.Result.TotalOps, r.Result.Errors)
	}
	if r.Message != "" {
		fmt.Printf("  message: %s\n", r.Message)
	}
	return nil
}

func usage() {
	fmt.Fprintln(os.Stderr, `everest-perf - database benchmarks for OpenEverest, from the CLI

Usage:
  everest-perf run     --namespace NS --instance NAME [--profile P] [--wait] [flags]
  everest-perf list    [--namespace NS] [--instance NAME]
  everest-perf get     --id RUN_ID
  everest-perf compare --a RUN_ID --b RUN_ID

Run 'everest-perf <command> -h' for command flags.`)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
