package runner

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/openeverest/plugin-performance/backend/internal/driver"
	"github.com/openeverest/plugin-performance/backend/internal/store"
)

func testRun() *store.Run {
	return &store.Run{
		ID:           "abcdef12-3456-7890-abcd-ef1234567890",
		CreatedAt:    time.Now().UTC(),
		Namespace:    "default",
		InstanceName: "pg-demo",
		Engine:       "postgresql",
		Driver:       "sysbench",
		Profile:      "smoke",
		Status:       store.StatusPending,
		Spec:         driver.RunSpec{Threads: 4, DurationSeconds: 60, ReadPercent: 70, WritePercent: 30, Tables: 2, TableSize: 1000, Records: 1000},
		Fingerprint:  &store.Fingerprint{Engine: "postgresql"},
	}
}

func TestBuildJobIsolationPreferred(t *testing.T) {
	r := New(fake.NewSimpleClientset(), nil, DefaultConfig())
	job := r.buildJob(testRun(), "perf-run-abcdef12", "img:1", "echo hi")

	aff := job.Spec.Template.Spec.Affinity
	if aff == nil || aff.PodAntiAffinity == nil {
		t.Fatal("expected pod anti-affinity")
	}
	terms := aff.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution
	if len(terms) != 1 {
		t.Fatalf("expected 1 preferred term, got %d", len(terms))
	}
	sel := terms[0].PodAffinityTerm.LabelSelector.MatchLabels
	if sel[dbInstanceLabel] != "pg-demo" {
		t.Errorf("anti-affinity targets %v, want instance label pg-demo", sel)
	}
	if *job.Spec.BackoffLimit != 0 {
		t.Error("benchmarks must not retry: backoffLimit should be 0")
	}
	if *job.Spec.ActiveDeadlineSeconds != int64(60+DefaultConfig().PrepareGraceSeconds) {
		t.Errorf("deadline = %d", *job.Spec.ActiveDeadlineSeconds)
	}
}

func TestBuildJobIsolationRequired(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Isolation = IsolationRequired
	r := New(fake.NewSimpleClientset(), nil, cfg)
	job := r.buildJob(testRun(), "j", "img:1", "echo hi")
	if len(job.Spec.Template.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution) != 1 {
		t.Error("expected required anti-affinity term")
	}
}

func TestBuildJobIsolationOff(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Isolation = IsolationOff
	r := New(fake.NewSimpleClientset(), nil, cfg)
	job := r.buildJob(testRun(), "j", "img:1", "echo hi")
	if job.Spec.Template.Spec.Affinity != nil {
		t.Error("expected no affinity when isolation is off")
	}
}

func TestStartCreatesJobAndOwnedSecret(t *testing.T) {
	st, err := store.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	run := testRun()
	if err := st.CreateRun(run); err != nil {
		t.Fatal(err)
	}

	client := fake.NewSimpleClientset()
	r := New(client, st, DefaultConfig())
	d := driver.NewSysbench("")
	conn := driver.Connection{Host: "h", Port: 5432, User: "postgres", Password: "pw", Database: "postgres"}

	if err := r.Start(context.Background(), run, d, conn, d.DefaultImage()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	jobs, _ := client.BatchV1().Jobs("default").List(context.Background(), metav1.ListOptions{})
	if len(jobs.Items) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs.Items))
	}
	if !strings.HasPrefix(jobs.Items[0].Name, "perf-run-") {
		t.Errorf("job name %q", jobs.Items[0].Name)
	}

	secrets, _ := client.CoreV1().Secrets("default").List(context.Background(), metav1.ListOptions{})
	if len(secrets.Items) != 1 {
		t.Fatalf("expected 1 credentials secret, got %d", len(secrets.Items))
	}
	sec := secrets.Items[0]
	if sec.StringData["DB_PASSWORD"] != "pw" {
		t.Error("secret missing password")
	}
	if len(sec.OwnerReferences) != 1 || sec.OwnerReferences[0].Kind != "Job" {
		t.Error("secret must be owned by the Job so GC removes credentials with it")
	}

	got, _ := st.GetRun(run.ID)
	if got.Status != store.StatusRunning || got.JobName == "" {
		t.Errorf("run not marked running: %+v", got.Status)
	}
}

func TestCancel(t *testing.T) {
	st, _ := store.NewSQLite(":memory:")
	defer st.Close()
	run := testRun()
	_ = st.CreateRun(run)

	client := fake.NewSimpleClientset()
	r := New(client, st, DefaultConfig())
	d := driver.NewSysbench("")
	_ = r.Start(context.Background(), run, d, driver.Connection{Host: "h", Port: 1, User: "u", Password: "p", Database: "d"}, "img")

	if err := r.Cancel(context.Background(), run); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	got, _ := st.GetRun(run.ID)
	if got.Status != store.StatusCanceled {
		t.Errorf("status = %s, want canceled", got.Status)
	}
	jobs, _ := client.BatchV1().Jobs("default").List(context.Background(), metav1.ListOptions{})
	if len(jobs.Items) != 0 {
		t.Error("job should be deleted on cancel")
	}
}
