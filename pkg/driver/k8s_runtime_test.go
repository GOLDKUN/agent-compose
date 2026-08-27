//go:build k8scompose

package driver

import (
	appconfig "agent-compose/pkg/config"
	"bytes"
	"context"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	validation "k8s.io/apimachinery/pkg/api/validate/content"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/remotecommand"
	k8sexec "k8s.io/client-go/util/exec"

	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

// newTestK8sRuntime builds a k8sRuntime backed by a fake clientset, bypassing
// the lazy clientcmd-based client() construction entirely. The fake clientset
// does not simulate a kubelet transitioning a Pod to Running, so a reactor
// marks every created Pod Running immediately.
func newTestK8sRuntime(objects ...runtime.Object) (*k8sRuntime, *fake.Clientset) {
	clientset := fake.NewClientset(objects...)
	clientset.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		createAction, ok := action.(k8stesting.CreateAction)
		if !ok {
			return false, nil, nil
		}
		pod, ok := createAction.GetObject().(*corev1.Pod)
		if !ok {
			return false, nil, nil
		}
		pod.Status.Phase = corev1.PodRunning
		return false, nil, nil
	})
	r := &k8sRuntime{
		config: &appconfig.Config{
			GuestWorkspacePath:  "/workspace",
			GuestHomePath:       "/root",
			GuestStateRoot:      "/data/state",
			GuestRuntimeRoot:    "/data/runtime",
			GuestLogRoot:        "/data/logs",
			SandboxStartTimeout: 5 * time.Second,
		},
		defaultNamespace: "default",
		clients: map[string]*k8sClientEntry{
			"": {clientset: clientset, restConfig: &rest.Config{Host: "https://k8s-runtime-test.invalid"}},
		},
	}
	return r, clientset
}

// newTestK8sRuntimeNoAutoRunning is newTestK8sRuntime without the reactor
// that fakes a kubelet transitioning a created Pod to Running, so tests can
// exercise waitForPodRunning's own polling/timeout/diagnostic behavior
// against a Pod that never becomes ready.
func newTestK8sRuntimeNoAutoRunning(objects ...runtime.Object) (*k8sRuntime, *fake.Clientset) {
	clientset := fake.NewClientset(objects...)
	r := &k8sRuntime{
		config: &appconfig.Config{
			GuestWorkspacePath:  "/workspace",
			GuestHomePath:       "/root",
			GuestStateRoot:      "/data/state",
			GuestRuntimeRoot:    "/data/runtime",
			GuestLogRoot:        "/data/logs",
			SandboxStartTimeout: 500 * time.Millisecond,
		},
		defaultNamespace: "default",
		clients: map[string]*k8sClientEntry{
			"": {clientset: clientset, restConfig: &rest.Config{Host: "https://k8s-runtime-test.invalid"}},
		},
	}
	return r, clientset
}

// fakeK8sExecutor stands in for remotecommand.NewSPDYExecutor's real SPDY
// transport in tests, letting k8sRuntime.newExecutor be swapped out to
// exercise execWithInput/execRaw without a live apiserver.
type fakeK8sExecutor struct {
	stdout []byte
	stderr []byte
	err    error
}

func (f fakeK8sExecutor) Stream(options remotecommand.StreamOptions) error {
	return f.StreamWithContext(context.Background(), options)
}

func (f fakeK8sExecutor) StreamWithContext(_ context.Context, options remotecommand.StreamOptions) error {
	if options.Stdout != nil && len(f.stdout) > 0 {
		if _, err := options.Stdout.Write(f.stdout); err != nil {
			return err
		}
	}
	if options.Stderr != nil && len(f.stderr) > 0 {
		if _, err := options.Stderr.Write(f.stderr); err != nil {
			return err
		}
	}
	return f.err
}

func testSandbox(t *testing.T, id string) *Sandbox {
	t.Helper()
	return &Sandbox{Summary: SandboxSummary{
		ID:            id,
		GuestImage:    "example.test/image:latest",
		WorkspacePath: filepath.Join(t.TempDir(), "workspace"),
	}}
}

func TestK8sEnsureSandboxCreatesPodWhenMissing(t *testing.T) {
	r, clientset := newTestK8sRuntime()
	sandbox := testSandbox(t, "sandbox-1")

	info, err := r.EnsureSandbox(context.Background(), sandbox, VMState{}, ProxyState{})
	if err != nil {
		t.Fatalf("EnsureSandbox() error = %v", err)
	}
	if info.BoxID == "" {
		t.Fatal("EnsureSandbox() returned empty BoxID")
	}
	// "agent-compose-sandbox-", not bare "agent-compose-": the daemon's own
	// Deployment-managed Pod is typically also named "agent-compose-<hash>-
	// <random>", so a distinct prefix keeps the two apart in `kubectl get
	// pods` without requiring a label selector.
	if !strings.HasPrefix(info.BoxID, "agent-compose-sandbox-") {
		t.Fatalf("EnsureSandbox() BoxID = %q, want \"agent-compose-sandbox-\" prefix", info.BoxID)
	}

	pod, err := clientset.CoreV1().Pods("default").Get(context.Background(), info.BoxID, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected created pod to exist: %v", err)
	}
	if pod.Labels[k8sSandboxLabelID] != sandbox.Summary.ID {
		t.Fatalf("pod label %s = %q, want %q", k8sSandboxLabelID, pod.Labels[k8sSandboxLabelID], sandbox.Summary.ID)
	}
	if len(pod.Spec.Containers) != 1 || pod.Spec.Containers[0].Image != sandbox.Summary.GuestImage {
		t.Fatalf("pod container image = %+v, want %q", pod.Spec.Containers, sandbox.Summary.GuestImage)
	}
	// The k8s driver has no shared filesystem with the daemon (see
	// docs/design/k8s_pod_runtime_driver_design.md §2.1): sandbox data
	// flows over Exec, not a mount, so the Pod declares none.
	if len(pod.Spec.Volumes) != 0 {
		t.Fatalf("pod declares volumes %+v, want none", pod.Spec.Volumes)
	}
	if len(pod.Spec.Containers[0].VolumeMounts) != 0 {
		t.Fatalf("pod container declares volume mounts %+v, want none", pod.Spec.Containers[0].VolumeMounts)
	}
}

func TestK8sPodVolumeSpecsMapsPVCMounts(t *testing.T) {
	r := &k8sRuntime{config: &appconfig.Config{}, defaultNamespace: "agent-compose"}
	sandbox := testSandbox(t, "sandbox-volume")
	sandbox.VolumeMounts = []SandboxVolumeMount{
		{ID: "mount-cache", Type: "volume", Source: "cache", Target: "/cache", Driver: RuntimeDriverK8s, HostPath: "agent-compose/claim-cache", ReadOnly: true},
		{ID: "mount-cache-2", Type: "volume", Source: "cache", Target: "/cache-copy", Driver: RuntimeDriverK8s, HostPath: "agent-compose/claim-cache"},
	}
	volumes, mounts, err := r.podVolumeSpecs(sandbox, VMState{})
	if err != nil {
		t.Fatalf("podVolumeSpecs() error = %v", err)
	}
	if len(volumes) != 1 || len(mounts) != 2 || volumes[0].PersistentVolumeClaim == nil || volumes[0].PersistentVolumeClaim.ClaimName != "claim-cache" {
		t.Fatalf("PVC volumes/mounts = %#v / %#v", volumes, mounts)
	}
	if mounts[0].MountPath != "/cache" || !mounts[0].ReadOnly || mounts[1].MountPath != "/cache-copy" || mounts[0].Name != mounts[1].Name {
		t.Fatalf("volume mounts = %#v", mounts)
	}
}

func TestK8sPodVolumeSpecsRejectsBindAndCrossNamespaceMounts(t *testing.T) {
	r := &k8sRuntime{config: &appconfig.Config{}, defaultNamespace: "agent-compose"}
	for name, mount := range map[string]SandboxVolumeMount{
		"bind":      {Type: "bind", Source: "/host", Target: "/data", HostPath: "/host"},
		"namespace": {Type: "volume", Source: "cache", Target: "/data", Driver: RuntimeDriverK8s, HostPath: "other/claim"},
		"partial workspace overlap": {
			Type: "volume", Source: "cache", Target: "/workspace/cache", Driver: RuntimeDriverK8s, HostPath: "agent-compose/claim-cache",
		},
	} {
		t.Run(name, func(t *testing.T) {
			sandbox := testSandbox(t, "sandbox-"+name)
			sandbox.VolumeMounts = []SandboxVolumeMount{mount}
			if _, _, err := r.podVolumeSpecs(sandbox, VMState{}); err == nil {
				t.Fatal("podVolumeSpecs() error = nil")
			}
		})
	}
}

func TestK8sPodVolumeSpecsAllowsWholeWorkspaceOrHomeAsAVolume(t *testing.T) {
	r := &k8sRuntime{config: &appconfig.Config{}, defaultNamespace: "agent-compose"}
	for name, target := range map[string]string{"workspace": "/workspace", "home": "/root"} {
		t.Run(name, func(t *testing.T) {
			sandbox := testSandbox(t, "sandbox-"+name)
			sandbox.VolumeMounts = []SandboxVolumeMount{
				{Type: "volume", Source: "data", Target: target, Driver: RuntimeDriverK8s, HostPath: "agent-compose/claim-data"},
			}
			if _, _, err := r.podVolumeSpecs(sandbox, VMState{}); err != nil {
				t.Fatalf("podVolumeSpecs() error = %v, want the whole %s root to be a valid mount target", err, name)
			}
		})
	}
}

func TestK8sValidateVolumeMountTarget(t *testing.T) {
	const workspace, home = "/workspace", "/root"
	cases := map[string]struct {
		target  string
		wantErr bool
	}{
		"exact workspace match":        {"/workspace", false},
		"exact home match":             {"/root", false},
		"unrelated path":               {"/data/models", false},
		"strict descendant":            {"/workspace/cache", true},
		"strict ancestor":              {"/", true},
		"descendant of home":           {"/root/.cache", true},
		"lookalike prefix, not nested": {"/workspace2", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := k8sValidateVolumeMountTarget(tc.target, workspace, home)
			if tc.wantErr && err == nil {
				t.Fatalf("k8sValidateVolumeMountTarget(%q) error = nil, want an error", tc.target)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("k8sValidateVolumeMountTarget(%q) error = %v, want nil", tc.target, err)
			}
		})
	}
}

func TestK8sEnsureSandboxSupportsFullLengthSandboxID(t *testing.T) {
	r, clientset := newTestK8sRuntime()
	sandbox := testSandbox(t, strings.Repeat("a", 64))

	info, err := r.EnsureSandbox(context.Background(), sandbox, VMState{}, ProxyState{})
	if err != nil {
		t.Fatalf("EnsureSandbox() error = %v", err)
	}
	pod, err := clientset.CoreV1().Pods("default").Get(context.Background(), info.BoxID, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected created pod to exist: %v", err)
	}
	labelValue := pod.Labels[k8sSandboxLabelID]
	if validationErrors := validation.IsLabelValue(labelValue); len(validationErrors) > 0 {
		t.Fatalf("pod label %s = %q is invalid: %v", k8sSandboxLabelID, labelValue, validationErrors)
	}
	if labelValue == sandbox.Summary.ID {
		t.Fatalf("pod label %s retained overlong sandbox ID %q", k8sSandboxLabelID, labelValue)
	}
	if pod.Annotations[k8sSandboxLabelID] != sandbox.Summary.ID {
		t.Fatalf("pod annotation %s = %q, want full sandbox ID %q", k8sSandboxLabelID, pod.Annotations[k8sSandboxLabelID], sandbox.Summary.ID)
	}
}

func TestK8sEnsureSandboxFindsExistingPod(t *testing.T) {
	sandbox := testSandbox(t, "sandbox-2")
	existing := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "agent-compose-sandbox-2",
			Namespace: "default",
			Labels: map[string]string{
				k8sSandboxLabelID:     sandbox.Summary.ID,
				k8sSandboxLabelDriver: RuntimeDriverK8s,
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	r, clientset := newTestK8sRuntime(existing)

	info, err := r.EnsureSandbox(context.Background(), sandbox, VMState{}, ProxyState{})
	if err != nil {
		t.Fatalf("EnsureSandbox() error = %v", err)
	}
	if info.BoxID != existing.Name {
		t.Fatalf("EnsureSandbox() BoxID = %q, want %q", info.BoxID, existing.Name)
	}

	pods, err := clientset.CoreV1().Pods("default").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	if len(pods.Items) != 1 {
		t.Fatalf("EnsureSandbox() on an existing pod created %d pods, want 1", len(pods.Items))
	}
}

func TestK8sStopAndRemoveSandboxDeletePod(t *testing.T) {
	sandbox := testSandbox(t, "sandbox-3")
	existing := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "agent-compose-sandbox-3",
			Namespace: "default",
			Labels: map[string]string{
				k8sSandboxLabelID:     sandbox.Summary.ID,
				k8sSandboxLabelDriver: RuntimeDriverK8s,
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	r, clientset := newTestK8sRuntime(existing)

	missing, err := r.StopSandbox(context.Background(), sandbox, VMState{})
	if err != nil {
		t.Fatalf("StopSandbox() error = %v", err)
	}
	if missing {
		t.Fatal("StopSandbox() reported missing for an existing pod")
	}
	if _, err := clientset.CoreV1().Pods("default").Get(context.Background(), existing.Name, metav1.GetOptions{}); err == nil {
		t.Fatal("StopSandbox() did not delete the pod")
	}

	// RemoveSandbox (and a second StopSandbox) on an already-deleted pod must
	// tolerate the not-found case rather than erroring.
	if err := r.RemoveSandbox(context.Background(), sandbox, VMState{}); err != nil {
		t.Fatalf("RemoveSandbox() on an already-removed pod returned error: %v", err)
	}
	missing, err = r.StopSandbox(context.Background(), sandbox, VMState{})
	if err != nil {
		t.Fatalf("StopSandbox() on an already-removed pod returned error: %v", err)
	}
	if !missing {
		t.Fatal("StopSandbox() on an already-removed pod should report missing")
	}
}

func TestK8sExecScriptPreservesNestedShellQuotes(t *testing.T) {
	script := k8sExecScript(ExecSpec{
		Command: "sh",
		Args:    []string{"-lc", `printf '%s' "prompt's value"`},
	}, "")

	output, err := exec.Command("sh", "-lc", script).CombinedOutput()
	if err != nil {
		t.Fatalf("execute k8s script %q: %v: %s", script, err, output)
	}
	if got, want := string(output), "prompt's value"; got != want {
		t.Fatalf("k8s script output = %q, want %q", got, want)
	}
}

func TestK8sEnsureSandboxDeletesPodItCreatedWhenWaitFails(t *testing.T) {
	r, clientset := newTestK8sRuntimeNoAutoRunning()
	sandbox := testSandbox(t, "sandbox-wait-fail")

	_, err := r.EnsureSandbox(context.Background(), sandbox, VMState{}, ProxyState{})
	if err == nil {
		t.Fatal("EnsureSandbox() error = nil, want a wait-for-running failure")
	}

	pods, listErr := clientset.CoreV1().Pods("default").List(context.Background(), metav1.ListOptions{})
	if listErr != nil {
		t.Fatalf("list pods: %v", listErr)
	}
	if len(pods.Items) != 0 {
		t.Fatalf("EnsureSandbox() left %d pod(s) behind after a failed create, want 0 (no zombie pods)", len(pods.Items))
	}
}

func TestK8sEnsureSandboxLeavesExistingPodOnWaitFailure(t *testing.T) {
	sandbox := testSandbox(t, "sandbox-existing-wait-fail")
	existing := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "agent-compose-sandbox-existing-wait-fail",
			Namespace: "default",
			Labels: map[string]string{
				k8sSandboxLabelID:     sandbox.Summary.ID,
				k8sSandboxLabelDriver: RuntimeDriverK8s,
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodPending},
	}
	r, clientset := newTestK8sRuntimeNoAutoRunning(existing)

	_, err := r.EnsureSandbox(context.Background(), sandbox, VMState{}, ProxyState{})
	if err == nil {
		t.Fatal("EnsureSandbox() error = nil, want a wait-for-running failure")
	}

	if _, getErr := clientset.CoreV1().Pods("default").Get(context.Background(), existing.Name, metav1.GetOptions{}); getErr != nil {
		t.Fatalf("EnsureSandbox() deleted a pod it did not create: %v", getErr)
	}
}

func TestK8sWaitForPodRunningFailsFastOnImagePullBackOff(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-compose-sandbox-bad-image", Namespace: "default"},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: k8sContainerName,
				State: corev1.ContainerState{
					Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: "rpc error: image not found"},
				},
			}},
		},
	}
	r, _ := newTestK8sRuntime(pod)
	r.config.SandboxStartTimeout = time.Minute

	start := time.Now()
	_, err := r.waitForPodRunning(context.Background(), r.clients[""].clientset, "default", pod.Name)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("waitForPodRunning() error = nil, want ImagePullBackOff failure")
	}
	if !strings.Contains(err.Error(), "ImagePullBackOff") || !strings.Contains(err.Error(), "image not found") {
		t.Fatalf("waitForPodRunning() error = %q, want it to name the stuck reason", err.Error())
	}
	if elapsed >= r.config.SandboxStartTimeout {
		t.Fatalf("waitForPodRunning() took %s, want it to fail fast well under the %s timeout", elapsed, r.config.SandboxStartTimeout)
	}
}

func TestK8sIsSandboxAlive(t *testing.T) {
	sandbox := testSandbox(t, "sandbox-alive")
	running := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "agent-compose-sandbox-alive",
			Namespace: "default",
			Labels: map[string]string{
				k8sSandboxLabelID:     sandbox.Summary.ID,
				k8sSandboxLabelDriver: RuntimeDriverK8s,
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	r, _ := newTestK8sRuntime(running)

	alive, err := r.IsSandboxAlive(context.Background(), sandbox, VMState{})
	if err != nil {
		t.Fatalf("IsSandboxAlive() error = %v", err)
	}
	if !alive {
		t.Fatal("IsSandboxAlive() = false, want true for a Running pod")
	}

	missingSandbox := testSandbox(t, "sandbox-missing")
	alive, err = r.IsSandboxAlive(context.Background(), missingSandbox, VMState{})
	if err != nil {
		t.Fatalf("IsSandboxAlive() for a missing pod returned error = %v", err)
	}
	if alive {
		t.Fatal("IsSandboxAlive() = true, want false when the pod does not exist")
	}
}

func TestK8sReadGuestFilePreservesBinaryContent(t *testing.T) {
	sandbox := testSandbox(t, "sandbox-binary")
	existing := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "agent-compose-sandbox-binary",
			Namespace: "default",
			Labels: map[string]string{
				k8sSandboxLabelID:     sandbox.Summary.ID,
				k8sSandboxLabelDriver: RuntimeDriverK8s,
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	r, _ := newTestK8sRuntime(existing)

	// A byte sequence that is not valid UTF-8 (lone continuation/invalid
	// lead bytes) - the streaming decoder used for stdout/stderr display
	// would replace these with U+FFFD, corrupting them. A real-world
	// equivalent is any binary artifact: a PNG, a zip, a compiled binary.
	binary := []byte{0x89, 0x50, 0x4E, 0x47, 0xFF, 0xD8, 0x00, 0x01, 0xC3, 0x28, 0xA0, 0xA1}
	r.newExecutor = func(*rest.Config, string, *url.URL) (remotecommand.Executor, error) {
		return fakeK8sExecutor{stdout: binary}, nil
	}

	got, err := r.ReadGuestFile(context.Background(), sandbox, VMState{}, "/workspace/output.bin")
	if err != nil {
		t.Fatalf("ReadGuestFile() error = %v", err)
	}
	if !bytes.Equal(got, binary) {
		t.Fatalf("ReadGuestFile() = %#v, want exact binary round-trip %#v", got, binary)
	}
}

func TestK8sReadGuestFileReportsNonZeroExitCode(t *testing.T) {
	sandbox := testSandbox(t, "sandbox-exit-code")
	existing := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "agent-compose-sandbox-exit-code",
			Namespace: "default",
			Labels: map[string]string{
				k8sSandboxLabelID:     sandbox.Summary.ID,
				k8sSandboxLabelDriver: RuntimeDriverK8s,
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	r, _ := newTestK8sRuntime(existing)
	r.newExecutor = func(*rest.Config, string, *url.URL) (remotecommand.Executor, error) {
		return fakeK8sExecutor{stderr: []byte("cat: /workspace/missing: No such file or directory"), err: k8sexec.CodeExitError{Err: nil, Code: 1}}, nil
	}

	_, err := r.ReadGuestFile(context.Background(), sandbox, VMState{}, "/workspace/missing")
	if err == nil {
		t.Fatal("ReadGuestFile() error = nil, want a non-zero exit code error")
	}
	if !strings.Contains(err.Error(), "exit code 1") || !strings.Contains(err.Error(), "No such file or directory") {
		t.Fatalf("ReadGuestFile() error = %q, want it to surface the exit code and stderr", err.Error())
	}
}

func TestK8sPodExecOptionsAttachStdinOnlyForInternalTransfers(t *testing.T) {
	command := "cat > /workspace/input.txt"
	withoutStdin := k8sPodExecOptions(command, false)
	if withoutStdin.Stdin {
		t.Fatal("ordinary k8s exec unexpectedly attaches stdin")
	}
	withStdin := k8sPodExecOptions(command, true)
	if !withStdin.Stdin || !withStdin.Stdout || !withStdin.Stderr {
		t.Fatalf("transfer exec options = %#v, want stdin/stdout/stderr attached", withStdin)
	}
	if got := strings.Join(withStdin.Command, " "); got != "sh -lc "+command {
		t.Fatalf("transfer exec command = %q", got)
	}
}
