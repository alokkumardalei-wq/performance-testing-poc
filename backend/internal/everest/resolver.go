// Package everest resolves OpenEverest-managed databases into connection
// details and environment fingerprints.
//
// Credentials are fetched at run start and injected into the benchmark Job
// via a short-lived Secret owned by the Job; they are never cached and never
// written to the run store. On OpenEverest v2 the equivalent source of truth
// is GET .../instances/{name}/connection; this resolver reads the same data
// directly from the cluster (DatabaseCluster CR + user secret) so the POC
// works against a v1.x install and standalone databases alike.
package everest

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/openeverest/plugin-performance/backend/internal/driver"
)

var dbClusterGVR = schema.GroupVersionResource{
	Group:    "everest.percona.com",
	Version:  "v1alpha1",
	Resource: "databaseclusters",
}

// Instance describes a database instance visible to the plugin.
type Instance struct {
	Namespace string        `json:"namespace"`
	Name      string        `json:"name"`
	Engine    driver.Engine `json:"engine"`
	Version   string        `json:"version,omitempty"`
	Status    string        `json:"status,omitempty"`
	Hostname  string        `json:"hostname,omitempty"`
	Port      int           `json:"port,omitempty"`

	// Fingerprint inputs.
	Replicas     int32  `json:"replicas,omitempty"`
	CPULimit     string `json:"cpuLimit,omitempty"`
	MemoryLimit  string `json:"memoryLimit,omitempty"`
	StorageClass string `json:"storageClass,omitempty"`
	StorageSize  string `json:"storageSize,omitempty"`
}

// StaticInstance is a database registered via configuration rather than
// discovered from Everest CRs — used for demo environments and for
// benchmarking databases OpenEverest does not manage. Configured with the
// STATIC_INSTANCES env var (JSON array).
type StaticInstance struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Engine    string `json:"engine"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	User      string `json:"user"`
	Password  string `json:"password"`
	Database  string `json:"database"`
}

// Resolver discovers instances and their connection details.
type Resolver struct {
	dyn    dynamic.Interface
	core   kubernetes.Interface
	static []StaticInstance
}

func NewResolver(dyn dynamic.Interface, core kubernetes.Interface, static []StaticInstance) *Resolver {
	return &Resolver{dyn: dyn, core: core, static: static}
}

func (r *Resolver) staticFor(namespace, name string) *StaticInstance {
	for i := range r.static {
		if r.static[i].Namespace == namespace && r.static[i].Name == name {
			return &r.static[i]
		}
	}
	return nil
}

func (s *StaticInstance) instance() Instance {
	return Instance{
		Namespace: s.Namespace,
		Name:      s.Name,
		Engine:    driver.Engine(s.Engine),
		Status:    "ready",
		Hostname:  s.Host,
		Port:      s.Port,
	}
}

// engineToDriverEngine maps Everest engine types onto protocol-level engines.
func engineToDriverEngine(t string) (driver.Engine, error) {
	switch t {
	case "pxc", "mysql":
		return driver.EngineMySQL, nil
	case "psmdb", "mongodb":
		return driver.EngineMongoDB, nil
	case "postgresql":
		return driver.EnginePostgreSQL, nil
	}
	return "", fmt.Errorf("unsupported engine type %q", t)
}

// ListInstances returns every DatabaseCluster in the namespace ("" = all),
// plus any statically configured instances.
func (r *Resolver) ListInstances(ctx context.Context, namespace string) ([]Instance, error) {
	var instances []Instance
	for i := range r.static {
		if namespace == "" || r.static[i].Namespace == namespace {
			instances = append(instances, r.static[i].instance())
		}
	}

	var list *unstructured.UnstructuredList
	var err error
	if namespace == "" {
		list, err = r.dyn.Resource(dbClusterGVR).List(ctx, metav1.ListOptions{})
	} else {
		list, err = r.dyn.Resource(dbClusterGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	}
	if err != nil {
		if apierrors.IsNotFound(err) || meta_noKind(err) {
			return instances, nil // CRD not installed — static/standalone only
		}
		return nil, err
	}
	for i := range list.Items {
		instances = append(instances, instanceFromCR(&list.Items[i]))
	}
	return instances, nil
}

// GetInstance fetches one instance: static registrations first, then the
// DatabaseCluster CR.
func (r *Resolver) GetInstance(ctx context.Context, namespace, name string) (*Instance, error) {
	if s := r.staticFor(namespace, name); s != nil {
		inst := s.instance()
		return &inst, nil
	}
	obj, err := r.dyn.Resource(dbClusterGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	inst := instanceFromCR(obj)
	return &inst, nil
}

func instanceFromCR(obj *unstructured.Unstructured) Instance {
	inst := Instance{
		Namespace: obj.GetNamespace(),
		Name:      obj.GetName(),
	}
	engineType, _, _ := unstructured.NestedString(obj.Object, "spec", "engine", "type")
	if e, err := engineToDriverEngine(engineType); err == nil {
		inst.Engine = e
	}
	inst.Version, _, _ = unstructured.NestedString(obj.Object, "spec", "engine", "version")
	inst.Status, _, _ = unstructured.NestedString(obj.Object, "status", "status")
	inst.Hostname, _, _ = unstructured.NestedString(obj.Object, "status", "hostname")
	if port, ok, _ := unstructured.NestedInt64(obj.Object, "status", "port"); ok {
		inst.Port = int(port)
	}
	if replicas, ok, _ := unstructured.NestedInt64(obj.Object, "spec", "engine", "replicas"); ok {
		inst.Replicas = int32(replicas)
	}
	inst.CPULimit = nestedString(obj, "spec", "engine", "resources", "cpu")
	inst.MemoryLimit = nestedString(obj, "spec", "engine", "resources", "memory")
	inst.StorageClass = nestedString(obj, "spec", "engine", "storage", "class")
	inst.StorageSize = nestedString(obj, "spec", "engine", "storage", "size")
	return inst
}

// nestedString tolerates fields that are numbers in some CR versions.
func nestedString(obj *unstructured.Unstructured, fields ...string) string {
	if s, ok, _ := unstructured.NestedString(obj.Object, fields...); ok {
		return s
	}
	if v, ok, _ := unstructured.NestedFieldNoCopy(obj.Object, fields...); ok && v != nil {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

// Connection resolves credentials for an instance the same way the Everest
// API's credentials endpoint does: from the engine's user secret.
func (r *Resolver) Connection(ctx context.Context, inst *Instance) (driver.Connection, error) {
	if s := r.staticFor(inst.Namespace, inst.Name); s != nil {
		return driver.Connection{
			Host: s.Host, Port: s.Port, User: s.User,
			Password: s.Password, Database: s.Database,
		}, nil
	}
	conn := driver.Connection{Host: inst.Hostname, Port: inst.Port}
	if conn.Host == "" {
		return conn, fmt.Errorf("instance %s/%s has no hostname yet (not ready?)", inst.Namespace, inst.Name)
	}

	obj, err := r.dyn.Resource(dbClusterGVR).Namespace(inst.Namespace).Get(ctx, inst.Name, metav1.GetOptions{})
	if err != nil {
		return conn, err
	}
	secretName, _, _ := unstructured.NestedString(obj.Object, "spec", "engine", "userSecretsName")
	if secretName == "" {
		secretName = "everest-secrets-" + inst.Name
	}
	secret, err := r.core.CoreV1().Secrets(inst.Namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return conn, fmt.Errorf("reading user secret %q: %w", secretName, err)
	}

	switch inst.Engine {
	case driver.EngineMySQL:
		conn.User = "root"
		conn.Password = secretString(secret, "root")
		conn.Database = "sbtest"
		if conn.Port == 0 {
			conn.Port = 3306
		}
	case driver.EngineMongoDB:
		conn.User = secretString(secret, "MONGODB_DATABASE_ADMIN_USER")
		conn.Password = secretString(secret, "MONGODB_DATABASE_ADMIN_PASSWORD")
		conn.Database = "ycsb"
		if conn.Port == 0 {
			conn.Port = 27017
		}
	case driver.EnginePostgreSQL:
		conn.User = "postgres"
		conn.Password = secretString(secret, "password")
		conn.Database = "postgres"
		if conn.Port == 0 {
			conn.Port = 5432
		}
	default:
		return conn, fmt.Errorf("unsupported engine %q", inst.Engine)
	}
	if conn.Password == "" {
		return conn, fmt.Errorf("secret %q has no password for engine %s", secretName, inst.Engine)
	}
	return conn, nil
}

func secretString(s *corev1.Secret, key string) string {
	return string(s.Data[key])
}

// meta_noKind detects "the server could not find the requested resource"
// class errors that surface when the DatabaseCluster CRD is absent.
func meta_noKind(err error) bool {
	return strings.Contains(err.Error(), "could not find the requested resource") ||
		strings.Contains(err.Error(), "no matches for kind")
}
