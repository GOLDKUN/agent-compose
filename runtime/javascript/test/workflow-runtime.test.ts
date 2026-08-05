import fs from "node:fs/promises";
import path from "node:path";
import { describe, expect, it, vi } from "vitest";
import type { AgentResult } from "../src/types.js";
import { WorkflowEventWriter } from "../src/workflow/events.js";
import { parseWorkflowScript } from "../src/workflow/parser.js";
import { WorkflowRuntime } from "../src/workflow/runtime.js";
import { WorkflowStateStore } from "../src/workflow/state.js";
import { withTempSession } from "./helpers.js";

describe("workflow runtime", () => {
  it("runs parallel agents with stable invocation paths and scoped phases", async () => {
    await withTempSession(async (root) => {
      const runPrompt = vi.fn(async ({ promptText }: { promptText?: string }): Promise<AgentResult> => agentResult(promptText ?? ""));
      const runtime = await createRuntime(root, "run_parallel", `export const meta = { name: "parallel", description: "test" }
        return await parallel([
          () => phase("Backend", () => agent("backend", { key: "scan" })),
          () => phase("Frontend", () => agent("frontend", { key: "scan" })),
        ])`, runPrompt as never);

      await expect(runtime.execute()).resolves.toEqual(["backend", "frontend"]);
      expect(runtime.agents.map((agent) => [agent.invocationKey, agent.phase])).toEqual([
        ["root/parallel:0/key:scan", "Backend"],
        ["root/parallel:1/key:scan", "Frontend"],
      ]);
      expect(runPrompt).toHaveBeenCalledTimes(2);
    });
  });

  it("settles parallel branch failures but direct agent failures reject", async () => {
    await withTempSession(async (root) => {
      const fail = vi.fn(async ({ promptText }: { promptText?: string }): Promise<AgentResult> => {
        if (promptText === "bad") {
          throw new Error("provider failed");
        }
        return agentResult(promptText ?? "");
      });
      const settled = await createRuntime(root, "run_settle", `export const meta = { name: "settle", description: "test" }
        return await parallel([() => agent("good"), () => agent("bad")])`, fail as never);
      await expect(settled.execute()).resolves.toEqual(["good", null]);
      expect(settled.logs).toContain("parallel branch 1 failed: provider failed");

      const direct = await createRuntime(root, "run_direct", `export const meta = { name: "direct", description: "test" }
        return await agent("bad")`, fail as never);
      await expect(direct.execute()).rejects.toThrow("provider failed");
    });
  });

  it("reuses completed agents by invocationKey and inputHash in a new run", async () => {
    await withTempSession(async (root) => {
      const source = `export const meta = { name: "resume", description: "test" }
        return await agent("cached answer", { key: "stable" })`;
      const firstPrompt = vi.fn(async () => agentResult("cached answer"));
      const first = await createRuntime(root, "run_first", source, firstPrompt as never);
      await first.execute();
      expect(firstPrompt).toHaveBeenCalledTimes(1);

      const secondPrompt = vi.fn(async () => agentResult("should not run"));
      const second = await createRuntime(root, "run_second", source, secondPrompt as never, first.agents);
      await expect(second.execute()).resolves.toBe("cached answer");
      expect(secondPrompt).not.toHaveBeenCalled();
      expect(second.agents[0].status).toBe("cached");
    });
  });

  it("shares budget across agents and rejects calls after exhaustion", async () => {
    await withTempSession(async (root) => {
      const runPrompt = vi.fn(async ({ promptText }: { promptText?: string }) => agentResult(promptText ?? ""));
      const runtime = await createRuntime(root, "run_budget", `export const meta = { name: "budget", description: "test" }
        await agent("x")
        return await agent("second")`, runPrompt as never, [], { tokenBudget: 1 });

      await expect(runtime.execute()).rejects.toThrow("workflow token budget exhausted");
      expect(runPrompt).toHaveBeenCalledTimes(1);
    });
  });

  it("times out an individual agent and propagates workflow abort", async () => {
    await withTempSession(async (root) => {
      const waitForAbort = vi.fn(async ({ abortController }: { abortController?: AbortController }) => {
        await new Promise<void>((resolve) => abortController?.signal.addEventListener("abort", () => resolve(), { once: true }));
        return { ...agentResult(""), stopReason: "cancelled" };
      });
      const timed = await createRuntime(root, "run_timeout", `export const meta = { name: "timeout", description: "test" }
        return await agent("slow", { timeoutMs: 10 })`, waitForAbort as never);
      await expect(timed.execute()).rejects.toThrow("workflow agent timed out after 10ms");

      const parent = new AbortController();
      const aborted = await createRuntime(root, "run_abort", `export const meta = { name: "abort", description: "test" }
        return await agent("slow")`, waitForAbort as never, [], { abortController: parent });
      const result = aborted.execute();
      parent.abort();
      await expect(result).rejects.toMatchObject({ code: "WORKFLOW_ABORTED" });
    });
  });

  it("loads one nested workflow and rejects a second nesting level", async () => {
    await withTempSession(async (root) => {
      const library = path.join(root, "workspace", ".agent-compose", "workflows");
      await fs.mkdir(library, { recursive: true });
      await fs.writeFile(path.join(library, "child.js"), `export const meta = { name: "child", description: "test" }
        return { nested: args.value }`);
      const runtime = await createRuntime(root, "run_nested", `export const meta = { name: "parent", description: "test" }
        return await workflow("child", { value: 9 })`, vi.fn() as never);
      await expect(runtime.execute()).resolves.toEqual({ nested: 9 });

      await fs.writeFile(path.join(library, "child.js"), `export const meta = { name: "child", description: "test" }
        return await workflow("grandchild")`);
      const depth = await createRuntime(root, "run_depth", `export const meta = { name: "parent", description: "test" }
        return await workflow("child")`, vi.fn() as never);
      await expect(depth.execute()).rejects.toThrow("nested workflow depth exceeded");
    });
  });
});

async function createRuntime(
  root: string,
  runId: string,
  source: string,
  runPrompt: never,
  resumeAgents = [],
  overrides: { tokenBudget?: number; abortController?: AbortController } = {},
) {
  const stateRoot = path.join(root, "state");
  const store = await WorkflowStateStore.create(stateRoot, runId);
  const events = new WorkflowEventWriter(store.eventsPath, { write: () => true } as never);
  const scriptFile = path.join(root, `${runId}.js`);
  await fs.writeFile(scriptFile, source, "utf8");
  return new WorkflowRuntime({
    runId,
    parsed: parseWorkflowScript(source),
    args: null,
    scriptFile,
    stateRoot,
    workspace: path.join(root, "workspace"),
    home: path.join(root, "home"),
    provider: "codex",
    concurrency: 2,
    abortController: overrides.abortController ?? new AbortController(),
    tokenBudget: overrides.tokenBudget,
    store,
    events,
    resumeAgents,
    runPrompt,
  });
}

function agentResult(finalText: string): AgentResult {
  return {
    provider: "codex",
    threadId: "thread",
    stopReason: "completed",
    finalText,
    finalTextSource: "provider_message",
    transcript: finalText,
    stderr: "",
  };
}
