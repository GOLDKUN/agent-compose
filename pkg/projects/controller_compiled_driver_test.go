package projects

import (
	"context"
	"strings"
	"testing"

	"agent-compose/pkg/compose"
	appconfig "agent-compose/pkg/config"
	driverpkg "agent-compose/pkg/driver"
	domain "agent-compose/pkg/model"
)

func TestControllerRejectsUncompiledDriversBeforePersistence(t *testing.T) {
	ctx := context.Background()
	normalizedByDriver := make(map[string]*compose.NormalizedProjectSpec, 3)
	for _, driver := range []string{
		driverpkg.RuntimeDriverDocker,
		driverpkg.RuntimeDriverBoxlite,
		driverpkg.RuntimeDriverMicrosandbox,
	} {
		normalizedByDriver[driver] = normalizeProjectWithDriver(t, driver)
		if got := normalizedByDriver[driver].Agents[0].Driver.Name; got != driver {
			t.Fatalf("compose Normalize driver = %q, want %q", got, driver)
		}
	}

	store := &compiledDriverBoundaryStore{}
	controller := NewController(ControllerDependencies{
		Config: &appconfig.Config{RuntimeDriver: driverpkg.RuntimeDriverDocker},
		Store:  store,
	})

	docker := NormalizedProject{Spec: normalizedByDriver[driverpkg.RuntimeDriverDocker], SourcePath: "/projects/docker"}
	if validation, err := controller.ValidateProject(ctx, docker, nil); err != nil || !validation.Valid {
		t.Fatalf("ValidateProject(docker) = %#v, %v; want valid", validation, err)
	}

	for _, driver := range []string{driverpkg.RuntimeDriverBoxlite, driverpkg.RuntimeDriverMicrosandbox} {
		t.Run(driver, func(t *testing.T) {
			if driverpkg.IsRuntimeDriverCompiled(driver) {
				t.Skipf("%s is compiled in this test build", driver)
			}
			normalized := NormalizedProject{
				Spec:       normalizedByDriver[driver],
				SpecHash:   "hash-" + driver,
				SourcePath: "/projects/" + driver,
			}

			validation, err := controller.ValidateProject(ctx, normalized, nil)
			if err != nil {
				t.Fatalf("ValidateProject(%s) returned error: %v", driver, err)
			}
			assertUncompiledDriverIssue(t, validation.Issues, driver)
			if validation.Valid {
				t.Fatalf("ValidateProject(%s) valid = true, want false", driver)
			}

			result, err := controller.ApplyProject(ctx, ApplyRequest{Normalized: normalized})
			if err != nil {
				t.Fatalf("ApplyProject(%s) returned error: %v", driver, err)
			}
			assertUncompiledDriverIssue(t, result.Issues, driver)
			if result.Applied || result.RevisionSpec != normalized.Spec {
				t.Fatalf("ApplyProject(%s) = %#v, want validation-only result", driver, result)
			}
			store.assertNoWrites(t)
		})
	}

	t.Run("invalid name keeps name validation", func(t *testing.T) {
		spec := cloneNormalizedProjectDriver(t, normalizedByDriver[driverpkg.RuntimeDriverDocker], "future-runtime")
		normalized := NormalizedProject{Spec: spec, SpecHash: "hash-invalid", SourcePath: "/projects/invalid"}
		validation, err := controller.ValidateProject(ctx, normalized, nil)
		if err != nil {
			t.Fatalf("ValidateProject(invalid) returned error: %v", err)
		}
		if validation.Valid || len(validation.Issues) != 1 {
			t.Fatalf("ValidateProject(invalid) = %#v, want one name validation issue", validation)
		}
		issue := validation.Issues[0]
		if issue.Path != "agents.worker.driver" || !strings.Contains(issue.Message, "unsupported agent-compose runtime driver") || strings.Contains(issue.Message, "not compiled") {
			t.Fatalf("invalid driver issue = %#v, want existing name validation semantics", issue)
		}
		result, err := controller.ApplyProject(ctx, ApplyRequest{Normalized: normalized})
		if err != nil || len(result.Issues) != 1 || result.Applied {
			t.Fatalf("ApplyProject(invalid) = %#v, %v; want validation issue", result, err)
		}
		store.assertNoWrites(t)
	})
}

func normalizeProjectWithDriver(t *testing.T, driver string) *compose.NormalizedProjectSpec {
	t.Helper()
	raw := "name: compiled-boundary-" + driver + "\nagents:\n  worker:\n    provider: codex\n    driver:\n      " + driver + ": {}\n"
	parsed, err := compose.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("compose Parse(%s) returned error: %v", driver, err)
	}
	normalized, err := compose.Normalize(parsed, compose.NormalizeOptions{ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatalf("compose Normalize(%s) returned error: %v", driver, err)
	}
	return normalized
}

func cloneNormalizedProjectDriver(t *testing.T, spec *compose.NormalizedProjectSpec, driver string) *compose.NormalizedProjectSpec {
	t.Helper()
	if spec == nil || len(spec.Agents) != 1 || spec.Agents[0].Driver == nil {
		t.Fatalf("invalid normalized project fixture: %#v", spec)
	}
	cloned := *spec
	cloned.Agents = append([]compose.NormalizedAgentSpec(nil), spec.Agents...)
	clonedDriver := *spec.Agents[0].Driver
	clonedDriver.Name = driver
	cloned.Agents[0].Driver = &clonedDriver
	return &cloned
}

func assertUncompiledDriverIssue(t *testing.T, issues []ValidationIssue, driver string) {
	t.Helper()
	if len(issues) != 1 {
		t.Fatalf("%s issues = %#v, want one", driver, issues)
	}
	issue := issues[0]
	if issue.Path != "agents.worker.driver" || !strings.Contains(issue.Message, driver) || !strings.Contains(issue.Message, "not compiled") {
		t.Fatalf("%s issue = %#v, want typed capability validation message", driver, issue)
	}
}

type compiledDriverBoundaryStore struct {
	controllerCoverageStore
	writes []string
}

func (s *compiledDriverBoundaryStore) recordWrite(operation string) {
	s.writes = append(s.writes, operation)
}

func (s *compiledDriverBoundaryStore) assertNoWrites(t *testing.T) {
	t.Helper()
	if len(s.writes) != 0 {
		t.Fatalf("project validation performed writes: %v", s.writes)
	}
}

func (s *compiledDriverBoundaryStore) UpsertProject(_ context.Context, item domain.ProjectRecord) (domain.ProjectRecord, error) {
	s.recordWrite("project")
	return item, nil
}

func (s *compiledDriverBoundaryStore) SaveProjectRevision(_ context.Context, item domain.ProjectRevisionRecord) (domain.ProjectRevisionRecord, bool, error) {
	s.recordWrite("revision")
	return item, true, nil
}

func (s *compiledDriverBoundaryStore) UpsertProjectAgent(_ context.Context, item domain.ProjectAgentRecord) (domain.ProjectAgentRecord, error) {
	s.recordWrite("project_agent")
	return item, nil
}

func (s *compiledDriverBoundaryStore) UpsertManagedAgentDefinition(_ context.Context, item domain.AgentDefinition) (domain.AgentDefinition, error) {
	s.recordWrite("agent_definition")
	return item, nil
}

func (s *compiledDriverBoundaryStore) SetAgentDefinitionEnabled(_ context.Context, _ string, _ bool) (domain.AgentDefinition, error) {
	s.recordWrite("agent_definition_enabled")
	return domain.AgentDefinition{}, nil
}

func (s *compiledDriverBoundaryStore) UpsertProjectScheduler(_ context.Context, item domain.ProjectSchedulerRecord) (domain.ProjectSchedulerRecord, error) {
	s.recordWrite("scheduler")
	return item, nil
}

func (s *compiledDriverBoundaryStore) SetProjectSchedulerEnabled(_ context.Context, _, _ string, _ bool) (domain.ProjectSchedulerRecord, error) {
	s.recordWrite("scheduler_enabled")
	return domain.ProjectSchedulerRecord{}, nil
}

func (s *compiledDriverBoundaryStore) UpsertManagedScheduler(_ context.Context, item domain.Scheduler) (domain.Scheduler, error) {
	s.recordWrite("scheduler")
	return item, nil
}

func (s *compiledDriverBoundaryStore) ReplaceSchedulerTriggers(_ context.Context, _ string, _ []domain.SchedulerTrigger) ([]domain.SchedulerTrigger, error) {
	s.recordWrite("scheduler_triggers")
	return nil, nil
}

func (s *compiledDriverBoundaryStore) SetSchedulerEnabled(_ context.Context, _ string, _ bool) error {
	s.recordWrite("scheduler_enabled")
	return nil
}
