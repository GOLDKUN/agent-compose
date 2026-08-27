//go:build linux && cgo && boxlitecgo && microsandboxcgo && k8scompose

package driver

import (
	"reflect"
	"testing"
)

func TestFullK8sRuntimeDriverCompiledConstraintFixture(t *testing.T) {
	want := []string{
		RuntimeDriverDocker,
		RuntimeDriverBoxlite,
		RuntimeDriverMicrosandbox,
		RuntimeDriverK8s,
	}
	if got := CompiledRuntimeDrivers(); !reflect.DeepEqual(got, want) {
		t.Fatalf("CompiledRuntimeDrivers() = %v, want full Linux+k8s capability %v", got, want)
	}
}
