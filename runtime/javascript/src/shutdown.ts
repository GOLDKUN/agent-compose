import fs from "node:fs";
import path from "node:path";
import process from "node:process";

type SignalTarget = Pick<NodeJS.Process, "on" | "off">;

export const RUNTIME_READY_FILE_ENV = "AGENT_COMPOSE_INTERNAL_EXECUTION_READY_FILE";

interface RuntimeReadinessOptions {
  filePath?: string;
  pid?: number;
}

export class RuntimeShutdownController {
  readonly abortController = new AbortController();
  private disposed = false;
  private readonly readyFile: string | undefined;
  private readonly pid: number;

  constructor(
    private readonly target: SignalTarget = process,
    readiness: RuntimeReadinessOptions = {},
  ) {
    this.readyFile = readiness.filePath ?? process.env[RUNTIME_READY_FILE_ENV];
    this.pid = readiness.pid ?? process.pid;
    this.target.on("SIGTERM", this.handleSIGTERM);
    try {
      this.publishReadiness();
    } catch (error) {
      this.target.off("SIGTERM", this.handleSIGTERM);
      throw error;
    }
  }

  request(): void {
    if (!this.abortController.signal.aborted) {
      this.abortController.abort();
    }
  }

  dispose(): void {
    if (this.disposed) {
      return;
    }
    this.disposed = true;
    this.removeReadiness();
    this.target.off("SIGTERM", this.handleSIGTERM);
  }

  private publishReadiness(): void {
    if (!this.readyFile) {
      return;
    }
    fs.mkdirSync(path.dirname(this.readyFile), { recursive: true });
    fs.writeFileSync(this.readyFile, String(this.pid), { encoding: "utf8", mode: 0o600 });
  }

  private removeReadiness(): void {
    if (!this.readyFile) {
      return;
    }
    try {
      if (fs.readFileSync(this.readyFile, "utf8").trim() === String(this.pid)) {
        fs.unlinkSync(this.readyFile);
      }
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "ENOENT") {
        // Readiness cleanup is best effort during process teardown. Execution
        // IDs are unique, and the PID check prevents stale files signalling a
        // different runtime process.
      }
    }
  }

  private readonly handleSIGTERM = () => {
    this.request();
  };
}

export function cancellationRequested(signal: AbortSignal | undefined): boolean {
  return signal?.aborted === true;
}
