package volumes

import (
	"strings"
	"testing"

	domain "github.com/chaitin/agent-compose/pkg/model"
)

func TestNormalizeVolumeMountSpecsAllowsSameSourceMultipleTargetsAndNestedReadOnly(t *testing.T) {
	items, err := NormalizeMountSpecs([]domain.VolumeMountSpec{
		{Source: "cache", Target: "/mnt/cache-a"},
		{Source: "cache", Target: "/mnt/cache-b"},
		{Type: domain.VolumeMountTypeVolume, Source: "nested-cache", Target: "/mnt/nested/parent/child", ReadOnly: true},
		{Type: domain.VolumeMountTypeBind, Source: "./logs", Target: "/mnt/logs"},
	})
	if err != nil {
		t.Fatalf("NormalizeVolumeMountSpecs returned error: %v", err)
	}
	if len(items) != 4 {
		t.Fatalf("normalized item count = %d, want 4: %#v", len(items), items)
	}
	if items[0].Type != domain.VolumeMountTypeVolume || items[1].Type != domain.VolumeMountTypeVolume {
		t.Fatalf("default volume mount types = %#v", items[:2])
	}
	if items[2].Target != "/mnt/nested/parent/child" || !items[2].ReadOnly {
		t.Fatalf("nested read-only mount = %#v", items[2])
	}
	if items[3].Type != domain.VolumeMountTypeBind || items[3].Source != "./logs" {
		t.Fatalf("bind mount = %#v", items[3])
	}
}

func TestNormalizeVolumeMountSpecsRejectsInvalidTargetsAndSources(t *testing.T) {
	tests := []struct {
		name string
		spec []domain.VolumeMountSpec
		want string
	}{
		{
			name: "duplicate cleaned target",
			spec: []domain.VolumeMountSpec{
				{Source: "cache-a", Target: "/mnt/cache"},
				{Source: "cache-b", Target: "/mnt/cache/."},
			},
			want: `duplicate volume mount target "/mnt/cache"`,
		},
		{
			name: "relative target",
			spec: []domain.VolumeMountSpec{{Source: "cache", Target: "mnt/cache"}},
			want: `volume mount target "mnt/cache" must be absolute`,
		},
		{
			name: "empty named volume source",
			spec: []domain.VolumeMountSpec{{Source: "", Target: "/mnt/cache"}},
			want: "volume mount source: volume name is required",
		},
		{
			name: "empty bind source",
			spec: []domain.VolumeMountSpec{{Type: domain.VolumeMountTypeBind, Source: "", Target: "/mnt/logs"}},
			want: "bind mount source is required",
		},
		{
			name: "unsupported type",
			spec: []domain.VolumeMountSpec{{Type: "tmpfs", Source: "cache", Target: "/mnt/cache"}},
			want: `volume mount type "tmpfs" is not supported`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NormalizeMountSpecs(tt.spec)
			if err == nil {
				t.Fatal("NormalizeVolumeMountSpecs returned nil error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestValidateDriverMountSpecsRejectsK8sBindMounts(t *testing.T) {
	bind := domain.VolumeMountSpec{Type: domain.VolumeMountTypeBind, Source: "./fixtures", Target: "/fixtures"}
	if err := ValidateDriverMountSpecs(domain.VolumeDriverK8s, []domain.VolumeMountSpec{bind}); err == nil || !strings.Contains(err.Error(), "does not support local bind mounts") {
		t.Fatalf("k8s bind validation error = %v", err)
	}
	if err := ValidateDriverMountSpecs("docker", []domain.VolumeMountSpec{bind}); err != nil {
		t.Fatalf("docker bind validation error = %v", err)
	}
}

func TestValidateResolvedDriverMountsRejectsLocalVolumeForK8s(t *testing.T) {
	err := ValidateResolvedDriverMounts(domain.VolumeDriverK8s, []domain.SandboxVolumeMount{{
		Type: domain.VolumeMountTypeVolume, Source: "cache", Driver: domain.VolumeDriverLocal, Target: "/cache",
	}})
	if err == nil || !strings.Contains(err.Error(), "use a volume with driver k8s") {
		t.Fatalf("resolved k8s mount validation error = %v", err)
	}
}

func TestNormalizeSessionVolumeMountsKeepsValidReadOnlyNestedMounts(t *testing.T) {
	items := NormalizeSandboxMounts([]domain.SandboxVolumeMount{
		{ID: " mount-a ", Type: " VOLUME ", Source: " cache ", Target: "/mnt/nested/../cache", ReadOnly: true, HostPath: " /host/cache "},
		{Type: domain.VolumeMountTypeVolume, Source: "missing-target", Target: "", HostPath: "/host/missing"},
		{Type: domain.VolumeMountTypeVolume, Source: "missing-host", Target: "/mnt/missing"},
	})
	if len(items) != 1 {
		t.Fatalf("normalized session mounts = %#v, want one valid mount", items)
	}
	item := items[0]
	if item.ID != "mount-a" || item.Type != domain.VolumeMountTypeVolume || item.Source != "cache" ||
		item.Target != "/mnt/cache" || !item.ReadOnly || item.HostPath != "/host/cache" {
		t.Fatalf("normalized session mount = %#v", item)
	}
}
