//go:build k8scompose

package volumes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	appconfig "agent-compose/pkg/config"
	domain "agent-compose/pkg/model"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	validation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	k8sVolumeDefaultSize       = "1Gi"
	k8sVolumeDefaultAccessMode = "ReadWriteOnce"
)

// K8sDriver maps a named agent-compose volume to a Kubernetes PersistentVolumeClaim.
// Kubernetes details are kept in VolumeRecord.Options so the existing YAML
// volume schema remains the configuration boundary.
type K8sDriver struct {
	config  *appconfig.Config
	mu      sync.Mutex
	clients map[string]kubernetes.Interface
}

func NewK8sDriver(config *appconfig.Config) *K8sDriver {
	return &K8sDriver{config: config, clients: make(map[string]kubernetes.Interface)}
}

func (d *K8sDriver) Name() string { return domain.VolumeDriverK8s }

func (d *K8sDriver) Create(ctx context.Context, record domain.VolumeRecord) (domain.VolumeRecord, error) {
	if d == nil || d.config == nil {
		return domain.VolumeRecord{}, fmt.Errorf("k8s volume driver config is required")
	}
	options, namespace, err := d.volumeOptions(record.Options)
	if err != nil {
		return domain.VolumeRecord{}, err
	}
	claimName := k8sClaimName(record.ID)
	client, err := d.client("")
	if err != nil {
		return domain.VolumeRecord{}, err
	}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: claimName, Namespace: namespace, Labels: map[string]string{
			"app.kubernetes.io/managed-by": "agent-compose",
			"agent-compose.volume-id":      record.ID,
		}},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.PersistentVolumeAccessMode(options["access_mode"])},
			Resources:        corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(options["size"])}},
			StorageClassName: optionalString(options["storage_class"]),
		},
	}
	if _, err := client.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, pvc, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return domain.VolumeRecord{}, fmt.Errorf("create k8s volume PVC %s/%s: %w", namespace, claimName, err)
	}
	record.Driver = d.Name()
	record.Options = options
	record.Path = pvcRef(namespace, claimName)
	return record, nil
}

func (d *K8sDriver) Inspect(ctx context.Context, record domain.VolumeRecord) (domain.VolumeRecord, error) {
	namespace, claimName, err := parsePVCRef(record.Path)
	if err != nil {
		return domain.VolumeRecord{}, err
	}
	client, err := d.client("")
	if err != nil {
		return domain.VolumeRecord{}, err
	}
	if _, err := client.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, claimName, metav1.GetOptions{}); err != nil {
		return domain.VolumeRecord{}, fmt.Errorf("inspect k8s volume PVC %s/%s: %w", namespace, claimName, err)
	}
	record.Driver = d.Name()
	return record, nil
}

func (d *K8sDriver) Remove(ctx context.Context, record domain.VolumeRecord) error {
	namespace, claimName, err := parsePVCRef(record.Path)
	if err != nil {
		return err
	}
	client, err := d.client("")
	if err != nil {
		return err
	}
	if err := client.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, claimName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("remove k8s volume PVC %s/%s: %w", namespace, claimName, err)
	}
	return nil
}

func (d *K8sDriver) ResolveMountSource(ctx context.Context, record domain.VolumeRecord) (string, error) {
	if _, err := d.Inspect(ctx, record); err != nil {
		return "", err
	}
	return record.Path, nil
}

func (d *K8sDriver) volumeOptions(input map[string]string) (map[string]string, string, error) {
	options := NormalizeStringMap(input)
	if strings.TrimSpace(options["context"]) != "" {
		return nil, "", fmt.Errorf("k8s volume context override is not supported; PVCs use the daemon cluster")
	}
	namespace := strings.TrimSpace(options["namespace"])
	if namespace == "" {
		namespace = strings.TrimSpace(d.config.K8sNamespace)
	}
	if namespace == "" {
		namespace = "default"
	}
	if errs := validation.IsDNS1123Label(namespace); len(errs) > 0 {
		return nil, "", fmt.Errorf("k8s volume namespace %q is invalid: %s", namespace, strings.Join(errs, "; "))
	}
	size := strings.TrimSpace(options["size"])
	if size == "" {
		size = k8sVolumeDefaultSize
	}
	quantity, err := resource.ParseQuantity(size)
	if err != nil || quantity.Sign() <= 0 {
		return nil, "", fmt.Errorf("k8s volume size %q must be a positive resource quantity", size)
	}
	accessMode := strings.TrimSpace(options["access_mode"])
	if accessMode == "" {
		accessMode = k8sVolumeDefaultAccessMode
	}
	switch corev1.PersistentVolumeAccessMode(accessMode) {
	case corev1.ReadWriteOnce, corev1.ReadOnlyMany, corev1.ReadWriteMany, corev1.ReadWriteOncePod:
	default:
		return nil, "", fmt.Errorf("k8s volume access_mode %q is not supported", accessMode)
	}
	options["namespace"], options["size"], options["access_mode"] = namespace, quantity.String(), accessMode
	return options, namespace, nil
}

func (d *K8sDriver) client(contextName string) (kubernetes.Interface, error) {
	contextName = strings.TrimSpace(contextName)
	d.mu.Lock()
	defer d.mu.Unlock()
	if client, ok := d.clients[contextName]; ok {
		return client, nil
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if path := strings.TrimSpace(d.config.K8sKubeconfigPath); path != "" {
		rules.ExplicitPath = path
	}
	overrides := &clientcmd.ConfigOverrides{CurrentContext: contextName}
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubernetes config for volume context %q: %w", contextName, err)
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("build kubernetes client for volume context %q: %w", contextName, err)
	}
	d.clients[contextName] = client
	return client, nil
}

func k8sClaimName(id string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(id)))
	return "agent-compose-" + hex.EncodeToString(digest[:])[:24]
}

func pvcRef(namespace, claimName string) string { return namespace + "/" + claimName }

func parsePVCRef(value string) (string, string, error) {
	namespace, claimName, ok := strings.Cut(strings.TrimSpace(value), "/")
	if !ok || namespace == "" || claimName == "" || strings.Contains(claimName, "/") {
		return "", "", fmt.Errorf("k8s volume source %q must be namespace/claim", value)
	}
	return namespace, claimName, nil
}

func optionalString(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	value = strings.TrimSpace(value)
	return &value
}
