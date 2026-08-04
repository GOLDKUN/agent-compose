package driver

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/google/uuid"
)

const (
	executionIDEnv        = "AGENT_COMPOSE_INTERNAL_EXECUTION_ID"
	executionReadyFileEnv = "AGENT_COMPOSE_INTERNAL_EXECUTION_READY_FILE"
	guestRuntimeReadyRoot = "/tmp/agent-compose-runtime-ready"
)

const guestRuntimeSignalScript = `
ready_file=$1
execution_id=$2
signal=$3
[ -r "$ready_file" ] || exit 75
pid=$(cat "$ready_file" 2>/dev/null) || exit 75
case $pid in
  ''|*[!0-9]*) exit 75 ;;
esac
environ=/proc/$pid/environ
[ -r "$environ" ] || exit 76
tr '\000' '\n' <"$environ" 2>/dev/null | grep -Fqx 'AGENT_COMPOSE_INTERNAL_EXECUTION_ID='"$execution_id" || exit 76
exe=$(readlink "/proc/$pid/exe" 2>/dev/null) || exit 76
case ${exe##*/} in
  node|nodejs) ;;
  *) exit 76 ;;
esac
tr '\000' '\n' <"/proc/$pid/cmdline" 2>/dev/null | grep -Fq agent-compose-runtime || exit 76
kill "-$signal" "$pid" 2>/dev/null || exit 76
`

var (
	// ErrGuestRuntimeNotReady means the guest runtime has not installed its
	// signal handler and published its PID yet.
	ErrGuestRuntimeNotReady = errors.New("guest runtime is not ready for signals")
	// ErrGuestRuntimeGone means the ready file no longer identifies the
	// tracked agent-compose-runtime process.
	ErrGuestRuntimeGone = errors.New("guest runtime is no longer running")
)

func guestRuntimeSignalCommand(executionID string, signal RuntimeSignal) ([]string, error) {
	executionID = strings.TrimSpace(executionID)
	if _, err := uuid.Parse(executionID); err != nil {
		return nil, fmt.Errorf("guest runtime execution ID is invalid")
	}
	signalName, err := guestRuntimeSignalName(signal)
	if err != nil {
		return nil, err
	}
	return []string{
		"sh",
		"-c",
		guestRuntimeSignalScript,
		"agent-compose-execution-signal",
		path.Join(guestRuntimeReadyRoot, executionID),
		executionID,
		signalName,
	}, nil
}

// GuestRuntimeControlEnv copies env and installs the private host/guest runtime
// identity used by graceful-stop control operations.
func GuestRuntimeControlEnv(env map[string]string, executionID string) map[string]string {
	marked := make(map[string]string, len(env)+2)
	for name, value := range env {
		if name != executionIDEnv && name != executionReadyFileEnv {
			marked[name] = value
		}
	}
	marked[executionIDEnv] = executionID
	marked[executionReadyFileEnv] = path.Join(guestRuntimeReadyRoot, executionID)
	return marked
}

func guestRuntimeSignalName(signal RuntimeSignal) (string, error) {
	switch signal {
	case RuntimeSignalInterrupt:
		return "INT", nil
	case RuntimeSignalTerminate:
		return "TERM", nil
	case RuntimeSignalKill:
		return "KILL", nil
	default:
		return "", fmt.Errorf("unsupported guest runtime signal %q", signal)
	}
}

func guestRuntimeSignalExitError(exitCode int) error {
	switch exitCode {
	case 0:
		return nil
	case 75:
		return ErrGuestRuntimeNotReady
	case 76:
		return ErrGuestRuntimeGone
	default:
		return fmt.Errorf("guest runtime signal control exited with code %d", exitCode)
	}
}
