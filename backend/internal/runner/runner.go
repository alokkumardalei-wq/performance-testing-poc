// Package runner executes benchmark runs as short-lived Kubernetes Jobs.
//
// Design decisions (see docs/architecture.md):
//   - One Job per run, restartPolicy Never, backoffLimit 0: a benchmark that
//     crashed is a failed run, not something to retry silently.
//   - Pod anti-affinity keeps the load generator off the database's node —
//     a generator sharing CPU with the process it measures produces a number
//     not worth storing. Whether isolation actually held is recorded in the
//     run's fingerprint rather than assumed.
//   - Credentials travel in a run-scoped Secret owned by the Job, so garbage
//     collection removes them with the Job. They are never persisted.
//   - The Job is the source of truth for run state; the runner polls it and
//     mirrors state into the store.
package runner

import (
	"context"
	"fmt"
	"io"
	"log"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"

	"github.com/openeverest/plugin-performance/backend/internal/driver"
	"github.com/openeverest/plugin-performance/backend/internal/store"
)

const (
	labelApp   = "app.kubernetes.io/name"
	labelValue = "everest-perf-run"
	labelRunID = "perf.openeverest.io/run-id"
	// dbInstanceLabel is the label percona operators put on database pods;
	// anti-affinity targets it.
	dbInstanceLabel = "app.kubernetes.io/instance"
)

// IsolationMode controls how strongly the generator avoids database nodes.
type IsolationMode string

const (
	IsolationPreferred IsolationMode = "preferred" // best effort (default)
	IsolationRequired  IsolationMode = "required"  // fail scheduling instead of co-locating
	IsolationOff       IsolationMode = "off"
)

// Config tunes the runner.
type Config struct {
	Isolation IsolationMode
	// JobTTLSeconds is how long finished Jobs (and their owned credential
	// Secrets) linger for debugging before GC.
	JobTTLSeconds int32
	// PrepareGraceSeconds is added to the workload duration for the
	// prepare/load phase when computing the Job deadline.
	PrepareGraceSeconds int
	CPURequest          string
	MemoryRequest       string
	CPULimit            string
	MemoryLimit         string
}

func DefaultConfig() Config {
	return Config{
		Isolation:           IsolationPreferred,
		JobTTLSeconds:       3600,
		PrepareGraceSeconds: 1800,
		CPURequest:          "500m",
		MemoryRequest:       "256Mi",
		CPULimit:            "4",
		MemoryLimit:         "1Gi",
	}
}

// Runner creates and tracks benchmark Jobs.
type Runner struct {
	core  kubernetes.Interface
	store store.Store
	cfg   Config
}

func New(core kubernetes.Interface, st store.Store, cfg Config) *Runner {
	return &Runner{core: core, store: st, cfg: cfg}
}

// Start creates the Job for a run and begins tracking it. The run must
// already exist in the store with StatusPending.
func (r *Runner) Start(ctx context.Context, run *store.Run, d driver.Driver, conn driver.Connection, image string) error {
	script, err := d.BuildScript(driver.Engine(run.Engine), conn, run.Spec)
	if err != nil {
		return err
	}
	jobName := "perf-run-" + run.ID[:8]

	job := r.buildJob(run, jobName, image, script)
	created, err := r.core.BatchV1().Jobs(run.Namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating job: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName + "-creds",
			Namespace: run.Namespace,
			Labels:    map[string]string{labelApp: labelValue, labelRunID: run.ID},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "batch/v1",
				Kind:       "Job",
				Name:       created.Name,
				UID:        created.UID,
				Controller: ptr.To(true),
			}},
		},
		StringData: map[string]string{"DB_PASSWORD": conn.Password},
	}
	if _, err := r.core.CoreV1().Secrets(run.Namespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		_ = r.core.BatchV1().Jobs(run.Namespace).Delete(ctx, jobName, metav1.DeleteOptions{
			PropagationPolicy: ptr.To(metav1.DeletePropagationBackground),
		})
		return fmt.Errorf("creating credentials secret: %w", err)
	}

	run.JobName = jobName
	run.Status = store.StatusRunning
	now := time.Now().UTC()
	run.StartedAt = &now
	if err := r.store.UpdateRun(run); err != nil {
		return err
	}

	go r.watch(run.ID, run.Namespace, jobName, d)
	return nil
}

func (r *Runner) buildJob(run *store.Run, jobName, image, script string) *batchv1.Job {
	labels := map[string]string{labelApp: labelValue, labelRunID: run.ID}
	deadline := int64(run.Spec.DurationSeconds + r.cfg.PrepareGraceSeconds)

	podSpec := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyNever,
		Containers: []corev1.Container{{
			Name:    "benchmark",
			Image:   image,
			Command: []string{"/bin/sh", "-c", script},
			EnvFrom: []corev1.EnvFromSource{{
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: jobName + "-creds"},
				},
			}},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(r.cfg.CPURequest),
					corev1.ResourceMemory: resource.MustParse(r.cfg.MemoryRequest),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(r.cfg.CPULimit),
					corev1.ResourceMemory: resource.MustParse(r.cfg.MemoryLimit),
				},
			},
		}},
	}

	if r.cfg.Isolation != IsolationOff {
		term := corev1.PodAffinityTerm{
			LabelSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{dbInstanceLabel: run.InstanceName},
			},
			TopologyKey: "kubernetes.io/hostname",
		}
		affinity := &corev1.Affinity{PodAntiAffinity: &corev1.PodAntiAffinity{}}
		if r.cfg.Isolation == IsolationRequired {
			affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution = []corev1.PodAffinityTerm{term}
		} else {
			affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution = []corev1.WeightedPodAffinityTerm{
				{Weight: 100, PodAffinityTerm: term},
			}
		}
		podSpec.Affinity = affinity
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: jobName, Namespace: run.Namespace, Labels: labels},
		Spec: batchv1.JobSpec{
			BackoffLimit:            ptr.To(int32(0)),
			TTLSecondsAfterFinished: ptr.To(r.cfg.JobTTLSeconds),
			ActiveDeadlineSeconds:   ptr.To(deadline),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       podSpec,
			},
		},
	}
}

// Resume re-attaches watchers for runs left in "running" after a backend
// restart (the Job kept going; we just lost the goroutine).
func (r *Runner) Resume(registry *driver.Registry) {
	runs, err := r.store.ListRuns(store.ListFilter{})
	if err != nil {
		return
	}
	for _, run := range runs {
		if run.Status == store.StatusRunning && run.JobName != "" {
			d, err := registry.Get(run.Driver)
			if err != nil {
				continue
			}
			log.Printf("resuming watch for run %s (job %s/%s)", run.ID, run.Namespace, run.JobName)
			go r.watch(run.ID, run.Namespace, run.JobName, d)
		}
	}
}

// Cancel deletes the run's Job and marks the run canceled.
func (r *Runner) Cancel(ctx context.Context, run *store.Run) error {
	if run.JobName != "" {
		err := r.core.BatchV1().Jobs(run.Namespace).Delete(ctx, run.JobName, metav1.DeleteOptions{
			PropagationPolicy: ptr.To(metav1.DeletePropagationBackground),
		})
		if err != nil && !isNotFound(err) {
			return err
		}
	}
	run.Status = store.StatusCanceled
	now := time.Now().UTC()
	run.FinishedAt = &now
	return r.store.UpdateRun(run)
}

// watch polls the Job to completion and finalizes the run record.
func (r *Runner) watch(runID, namespace, jobName string, d driver.Driver) {
	ctx := context.Background()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		run, err := r.store.GetRun(runID)
		if err != nil || run.Status == store.StatusCanceled {
			return
		}

		job, err := r.core.BatchV1().Jobs(namespace).Get(ctx, jobName, metav1.GetOptions{})
		if err != nil {
			if isNotFound(err) {
				r.fail(run, "benchmark job disappeared")
				return
			}
			continue
		}

		switch {
		case jobFinished(job, batchv1.JobComplete):
			logs := r.podLogs(ctx, namespace, jobName)
			run.RawOutput = logs
			result, perr := d.ParseOutput(logs)
			if perr != nil {
				r.fail(run, fmt.Sprintf("job succeeded but output could not be parsed: %v", perr))
				return
			}
			run.Result = result
			r.recordPlacement(ctx, run)
			run.Status = store.StatusSucceeded
			run.Message = ""
			now := time.Now().UTC()
			run.FinishedAt = &now
			if err := r.store.UpdateRun(run); err != nil {
				log.Printf("run %s: persisting result: %v", runID, err)
			}
			return

		case jobFinished(job, batchv1.JobFailed):
			run.RawOutput = r.podLogs(ctx, namespace, jobName)
			reason := "benchmark job failed"
			for _, c := range job.Status.Conditions {
				if c.Type == batchv1.JobFailed && c.Message != "" {
					reason = c.Message
				}
			}
			r.recordPlacement(ctx, run)
			r.fail(run, reason)
			return
		}
	}
}

func (r *Runner) fail(run *store.Run, msg string) {
	run.Status = store.StatusFailed
	run.Message = msg
	now := time.Now().UTC()
	run.FinishedAt = &now
	if err := r.store.UpdateRun(run); err != nil {
		log.Printf("run %s: persisting failure: %v", run.ID, err)
	}
}

// recordPlacement fills the isolation part of the fingerprint: which node the
// generator ran on, which nodes the database's pods run on, and whether they
// overlap.
func (r *Runner) recordPlacement(ctx context.Context, run *store.Run) {
	if run.Fingerprint == nil {
		return
	}
	pods, err := r.core.CoreV1().Pods(run.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelRunID + "=" + run.ID,
	})
	if err == nil && len(pods.Items) > 0 {
		run.Fingerprint.GeneratorNode = pods.Items[0].Spec.NodeName
	}
	dbPods, err := r.core.CoreV1().Pods(run.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: dbInstanceLabel + "=" + run.InstanceName,
	})
	if err == nil {
		nodes := map[string]bool{}
		for _, p := range dbPods.Items {
			if p.Spec.NodeName != "" {
				nodes[p.Spec.NodeName] = true
			}
		}
		for n := range nodes {
			run.Fingerprint.DatabaseNodes = append(run.Fingerprint.DatabaseNodes, n)
		}
		if run.Fingerprint.GeneratorNode != "" && len(nodes) > 0 {
			isolated := !nodes[run.Fingerprint.GeneratorNode]
			run.Fingerprint.Isolated = &isolated
		}
	}
}

// podLogs fetches the log of the Job's (single) pod.
func (r *Runner) podLogs(ctx context.Context, namespace, jobName string) string {
	pods, err := r.core.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "job-name=" + jobName,
	})
	if err != nil || len(pods.Items) == 0 {
		return ""
	}
	// Use the most recent pod (backoffLimit=0 means there is only one).
	// Tail-read: the tool summary is at the end of the log, and reading from
	// the tail keeps it inside the window even when a noisy prepare phase
	// (e.g. go-ycsb re-loading an existing dataset) spams thousands of lines.
	pod := pods.Items[len(pods.Items)-1]
	req := r.core.CoreV1().Pods(namespace).GetLogs(pod.Name, &corev1.PodLogOptions{
		TailLines: ptr.To(int64(4000)),
	})
	rc, err := req.Stream(ctx)
	if err != nil {
		return ""
	}
	defer rc.Close()
	b, _ := io.ReadAll(io.LimitReader(rc, 2*1024*1024))
	return string(b)
}

func jobFinished(job *batchv1.Job, cond batchv1.JobConditionType) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == cond && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func isNotFound(err error) bool {
	return apierrors.IsNotFound(err)
}
