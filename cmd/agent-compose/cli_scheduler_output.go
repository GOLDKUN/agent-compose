package main

import (
	"context"
	"fmt"
	"github.com/chaitin/agent-compose/pkg/agentcompose/api"
	agentcomposev2 "github.com/chaitin/agent-compose/proto/agentcompose/v2"
	"github.com/chaitin/agent-compose/proto/agentcompose/v2/agentcomposev2connect"
	"io"
	"strings"
	"text/tabwriter"

	"connectrpc.com/connect"
	"gopkg.in/yaml.v3"
)

func setSchedulerTriggerInspectOutput(output *composeSchedulerInspectOutput, trigger composeSchedulerTriggerItem) {
	output.Resource = "trigger"
	output.Source = trigger.Source
	output.AgentName = trigger.AgentName
	output.Trigger = &trigger
	if trigger.Source == "declarative" && trigger.declarative != nil {
		output.Definition = api.TriggerYAMLShape(trigger.declarative)
	} else if trigger.registered != nil {
		output.Registered = trigger.registered
	}
}

func schedulerDisplayEventType(value string) string {
	return strings.Replace(strings.TrimSpace(value), "loader.", "scheduler.", 1)
}

func writeSchedulerListText(out io.Writer, output composeSchedulerListOutput, verbose bool) error {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	header := "SCHEDULER\tAGENT\tTRIGGER\tKIND\tSOURCE\tENABLED"
	if verbose {
		header = "SCHEDULER\tAGENT\tTRIGGER\tTRIGGER ID\tKIND\tSOURCE\tENABLED"
	}
	if _, err := fmt.Fprintln(tw, header); err != nil {
		return err
	}
	for _, trigger := range output.Triggers {
		name := firstNonEmptyString(trigger.Name, trigger.TriggerID)
		schedulerID := firstNonEmptyString(trigger.SchedulerShortID, shortOpaqueID(trigger.SchedulerID), "-")
		if verbose {
			schedulerID = firstNonEmptyString(trigger.SchedulerID, "-")
			if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%t\n",
				schedulerID, trigger.AgentName, name, firstNonEmptyString(trigger.TriggerID, "-"),
				trigger.Kind, trigger.Source, trigger.TriggerEnabled,
			); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%t\n",
			schedulerID,
			trigger.AgentName,
			name,
			trigger.Kind,
			trigger.Source,
			trigger.TriggerEnabled,
		); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func writeSchedulerInspectText(out io.Writer, output composeSchedulerInspectOutput) error {
	if output.Resource == "scheduler" {
		data, err := yaml.Marshal(output.Scheduler)
		if err != nil {
			return err
		}
		return writeCommandOutput(out, data)
	}
	if output.Resource == "run" {
		data, err := yaml.Marshal(output.Run)
		if err != nil {
			return err
		}
		return writeCommandOutput(out, data)
	}
	var target map[string]any
	if output.Source == "declarative" {
		target = output.Definition
	} else {
		target = output.Registered
	}
	data, err := yaml.Marshal(target)
	if err != nil {
		return err
	}
	return writeCommandOutput(out, data)
}

func writeSchedulerLogsText(out io.Writer, output composeSchedulerLogsOutput) error {
	for _, event := range output.Events {
		line := fmt.Sprintf("%s %s %s", event.CreatedAt, strings.ToUpper(firstNonEmptyString(event.Level, "info")), event.Type)
		line += " scheduler=" + firstNonEmptyString(event.AgentName, shortOpaqueID(event.SchedulerID), "-")
		line += " trigger=" + firstNonEmptyString(shortOpaqueID(event.TriggerID), "-")
		line += " run=" + firstNonEmptyString(shortOpaqueID(event.RunID), "-")
		if event.Message != "" {
			line += " " + event.Message
		}
		if event.SandboxID != "" {
			line += " sandbox=" + shortOpaqueID(event.SandboxID)
		}
		if _, err := fmt.Fprintln(out, strings.TrimSpace(line)); err != nil {
			return err
		}
	}
	return nil
}

type composeSchedulerProjectOutput struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	ShortID         string  `json:"short_id"`
	SourcePath      string  `json:"source_path,omitempty"`
	CurrentRevision *uint64 `json:"current_revision,omitempty"`
	SpecHash        string  `json:"spec_hash,omitempty"`
	AgentCount      *uint32 `json:"agent_count,omitempty"`
	SchedulerCount  *uint32 `json:"scheduler_count,omitempty"`
}

func schedulerProjectOutput(ctx context.Context, client agentcomposev2connect.ProjectServiceClient, runtimeProject composeRuntimeProject) composeSchedulerProjectOutput {
	// 权威摘要来自 daemon 已应用状态，字段规范化由 daemon 负责。此处与
	// composeProjectSummaryOutput 同源直接映射，保证 scheduler JSON 的 project
	// 字段与 `inspect project --json` 一致。
	if runtimeProject.summaryLoaded {
		return schedulerProjectOutputFromSummary(runtimeProject.project.GetSummary())
	}

	// 退化路径：只能解析本地项目身份时，按需补全摘要。补全仅服务 JSON 展示，
	// scheduler 历史查询不依赖当前部署状态，失败时退化为最小项目引用。
	response, err := client.GetProject(ctx, connect.NewRequest(&agentcomposev2.GetProjectRequest{
		Project: &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: runtimeProject.id()}},
	}))
	if err == nil && response != nil && response.Msg != nil {
		if loadedSummary := response.Msg.GetProject().GetSummary(); strings.TrimSpace(loadedSummary.GetProjectId()) != "" {
			return schedulerProjectOutputFromSummary(loadedSummary)
		}
	}

	summary := runtimeProject.project.GetSummary()
	return composeSchedulerProjectOutput{
		ID:         displayOpaqueID(summary.GetProjectId()),
		Name:       summary.GetName(),
		ShortID:    shortOpaqueID(summary.GetProjectId()),
		SourcePath: firstNonEmptyString(summary.GetSourcePath(), runtimeProject.composePath),
	}
}

// schedulerProjectOutputFromSummary 映射权威项目摘要，数值字段用指针保留合法的零值。
func schedulerProjectOutputFromSummary(summary *agentcomposev2.ProjectSummary) composeSchedulerProjectOutput {
	currentRevision := summary.GetCurrentRevision()
	agentCount := summary.GetAgentCount()
	schedulerCount := summary.GetSchedulerCount()
	return composeSchedulerProjectOutput{
		ID:              displayOpaqueID(summary.GetProjectId()),
		Name:            summary.GetName(),
		ShortID:         shortOpaqueID(summary.GetProjectId()),
		SourcePath:      summary.GetSourcePath(),
		CurrentRevision: &currentRevision,
		SpecHash:        summary.GetSpecHash(),
		AgentCount:      &agentCount,
		SchedulerCount:  &schedulerCount,
	}
}

type composeProjectSchedulerOutput struct {
	AgentName    string `json:"agent_name"`
	SchedulerID  string `json:"scheduler_id"`
	Enabled      bool   `json:"enabled"`
	TriggerCount uint32 `json:"trigger_count"`
}

type composeSchedulerListOutput struct {
	Project  composeSchedulerProjectOutput `json:"project"`
	Triggers []composeSchedulerTriggerItem `json:"triggers"`
}

type composeSchedulerInspectOutput struct {
	Project    composeSchedulerProjectOutput `json:"project"`
	Resource   string                        `json:"resource"`
	Source     string                        `json:"source"`
	AgentName  string                        `json:"agent_name"`
	Scheduler  *composeSchedulerItem         `json:"scheduler,omitempty"`
	Trigger    *composeSchedulerTriggerItem  `json:"trigger,omitempty"`
	Run        *composeSchedulerRunItem      `json:"run,omitempty"`
	Definition map[string]any                `json:"definition,omitempty"`
	Registered map[string]any                `json:"registered,omitempty"`
}

type composeSchedulerRunsOutput struct {
	Project composeSchedulerProjectOutput `json:"project"`
	Runs    []composeSchedulerRunItem     `json:"runs"`
}

type composeSchedulerLogsOutput struct {
	Project composeSchedulerProjectOutput `json:"project"`
	Run     *composeSchedulerRunItem      `json:"run,omitempty"`
	Events  []composeSchedulerLogEvent    `json:"events"`
}

func composeProjectSchedulerOutputFromProto(scheduler *agentcomposev2.ProjectScheduler) composeProjectSchedulerOutput {
	return composeProjectSchedulerOutput{
		AgentName:    scheduler.GetAgentName(),
		SchedulerID:  displayOpaqueID(scheduler.GetSchedulerId()),
		Enabled:      scheduler.GetEnabled(),
		TriggerCount: scheduler.GetTriggerCount(),
	}
}
