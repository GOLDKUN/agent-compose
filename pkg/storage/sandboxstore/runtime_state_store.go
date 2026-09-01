package sandboxstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	driverpkg "github.com/chaitin/agent-compose/pkg/driver"
)

func (s *Store) vmStatePath(id string) string {
	return filepath.Join(s.sandboxDir(id), "vm", "runtime.json")
}

func (s *Store) VMStatePath(id string) string {
	return s.vmStatePath(id)
}

func (s *Store) legacyVMStatePath(id string) string {
	return filepath.Join(s.sandboxDir(id), "vm", "boxlite.json")
}

func (s *Store) LegacyVMStatePath(id string) string {
	return s.legacyVMStatePath(id)
}

func (s *Store) proxyStatePath(id string) string {
	return filepath.Join(s.sandboxDir(id), "proxy", "jupyter.json")
}

func (s *Store) ProxyStatePath(id string) string {
	return s.proxyStatePath(id)
}

func (s *Store) GetVMState(id string) (VMState, error) {
	var state VMState
	if err := s.readJSONFile(s.vmStatePath(id), &state); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return VMState{}, err
		}
		if legacyErr := s.readJSONFile(s.legacyVMStatePath(id), &state); legacyErr != nil {
			return VMState{}, legacyErr
		}
	}
	state.Driver = driverpkg.ResolveRuntimeDriver(firstNonEmpty(state.Driver, state.Mode))
	if err := driverpkg.ValidateRuntimeDriver(state.Driver); err != nil {
		return VMState{}, err
	}
	if strings.TrimSpace(state.RuntimeHome) == "" {
		state.RuntimeHome = driverpkg.RuntimeHomeForDriver(s.config, state.Driver)
	}
	return state, nil
}

func (s *Store) SaveVMState(id string, state VMState) error {
	return s.saveVMState(id, state)
}

func (s *Store) saveVMState(id string, state VMState) error {
	driver, err := driverpkg.ResolveSandboxRuntimeDriver(state.Driver, s.config.RuntimeDriver)
	if err != nil {
		return err
	}
	state.Driver = driver
	state.Mode = driver
	if strings.TrimSpace(state.RuntimeHome) == "" {
		state.RuntimeHome = driverpkg.RuntimeHomeForDriver(s.config, driver)
	}
	return s.writeJSONFile(s.vmStatePath(id), state)
}

func (s *Store) GetProxyState(id string) (ProxyState, error) {
	var state ProxyState
	if err := s.readJSONFile(s.proxyStatePath(id), &state); err != nil {
		return ProxyState{}, err
	}
	return state, nil
}

func (s *Store) SaveProxyState(id string, state ProxyState) error {
	return s.writeJSONFile(s.proxyStatePath(id), state)
}

func (s *Store) AllocateHostPortForJupyter() (int, error) {
	return s.allocateHostPort()
}

func (s *Store) allocateHostPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("allocate host port: %w", err)
	}
	defer func() { _ = listener.Close() }()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("allocate host port: unexpected addr %T", listener.Addr())
	}
	return addr.Port, nil
}

func (s *Store) readJSONFile(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func (s *Store) writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
