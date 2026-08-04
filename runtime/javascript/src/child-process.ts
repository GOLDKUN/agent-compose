import type { ChildProcess } from "node:child_process";

export interface ChildExit {
  exitCode: number;
  spawnError?: Error;
}

export function waitForChildExit(child: ChildProcess, signal?: AbortSignal, event: "close" | "exit" = "close"): Promise<ChildExit> {
  return new Promise((resolve) => {
    let spawnError: Error | undefined;
    child.once("error", (error) => {
      spawnError = error;
      if (!signal?.aborted) {
        resolve({ exitCode: 1, spawnError });
      }
    });
    child.once(event, (code) => resolve({
      exitCode: code ?? (signal?.aborted ? 143 : 1),
      spawnError,
    }));
  });
}
