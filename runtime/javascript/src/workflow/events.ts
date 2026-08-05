import fs from "node:fs/promises";
import { WORKFLOW_EVENT_PREFIX } from "../constants.js";
import type { WorkflowEvent } from "./types.js";

export class WorkflowEventWriter {
  private pending = Promise.resolve();

  constructor(
    private readonly eventsPath: string,
    private readonly stderr: Pick<NodeJS.WriteStream, "write"> = process.stderr,
  ) {}

  emit(event: WorkflowEvent): Promise<void> {
    const line = `${JSON.stringify(event)}\n`;
    this.stderr.write(`${WORKFLOW_EVENT_PREFIX}${line}`);
    this.pending = this.pending.then(async () => {
      await fs.appendFile(this.eventsPath, line, { encoding: "utf8", mode: 0o600 });
    });
    return this.pending;
  }

  async flush(): Promise<void> {
    await this.pending;
  }
}
