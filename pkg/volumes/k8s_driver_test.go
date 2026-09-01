//go:build k8scompose

package volumes

import (
	"context"
	"testing"

	appconfig "agent-compose/pkg/config"
	domain "agent-compose/pkg/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestK8sDriverCreatesAndRemovesPVC(t *testing.T) {
	client := fake.NewSimpleClientset()
	driver := NewK8sDriver(&appconfig.Config{K8sNamespace: "agent-compose"})
	driver.clients[""] = client

	record, err := driver.Create(context.Background(), domain.VolumeRecord{ID: "volume-1", Name: "cache", Options: map[string]string{
		"size": "2Gi", "access_mode": "ReadWriteMany", "storage_class": "fast",
	}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if record.Driver != domain.VolumeDriverK8s || record.Path == "" || record.Options["namespace"] != "agent-compose" {
		t.Fatalf("created record = %#v", record)
	}
	list, err := client.CoreV1().PersistentVolumeClaims("agent-compose").List(context.Background(), metav1.ListOptions{})
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("PVC list = %#v, err = %v", list.Items, err)
	}
	pvc := &list.Items[0]
	storageRequest := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
	if storageRequest.String() != "2Gi" || pvc.Spec.AccessModes[0] != corev1.ReadWriteMany || pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != "fast" {
		t.Fatalf("PVC spec = %#v", pvc.Spec)
	}

	if _, err := driver.Inspect(context.Background(), record); err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if err := driver.Remove(context.Background(), record); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if _, err := client.CoreV1().PersistentVolumeClaims("agent-compose").Get(context.Background(), pvc.Name, metav1.GetOptions{}); err == nil {
		t.Fatal("PVC still exists after Remove")
	}
}

func TestK8sDriverCreateWithoutOptionsUsesDefaults(t *testing.T) {
	// Options nil/empty is the common case (no options: block declared at
	// all), and volumeOptions writes resolved namespace/size/access_mode
	// back into its result regardless - regression test for a nil map
	// write panic ("assignment to entry in nil map") when input is empty.
	client := fake.NewSimpleClientset()
	driver := NewK8sDriver(&appconfig.Config{K8sNamespace: "agent-compose"})
	driver.clients[""] = client

	record, err := driver.Create(context.Background(), domain.VolumeRecord{ID: "volume-defaults", Name: "cache"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if record.Options["namespace"] != "agent-compose" || record.Options["size"] != "1Gi" || record.Options["access_mode"] != "ReadWriteOnce" {
		t.Fatalf("default options = %#v", record.Options)
	}
}

func TestK8sDriverRejectsInvalidPVCOptions(t *testing.T) {
	driver := NewK8sDriver(&appconfig.Config{K8sNamespace: "agent-compose"})
	driver.clients[""] = fake.NewSimpleClientset()
	for name, options := range map[string]string{
		"bad size":        "not-a-size",
		"bad access mode": "ReadWriteAll",
		"bad namespace":   "Bad_Namespace",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := driver.Create(context.Background(), domain.VolumeRecord{ID: name, Name: name, Options: map[string]string{map[string]string{"bad size": "size", "bad access mode": "access_mode", "bad namespace": "namespace"}[name]: options}}); err == nil {
				t.Fatal("Create() error = nil")
			}
		})
	}
}
