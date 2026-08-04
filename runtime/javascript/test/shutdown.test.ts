import { EventEmitter } from "node:events";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { describe, expect, it, vi } from "vitest";
import { RuntimeShutdownController } from "../src/shutdown.js";

describe("runtime shutdown", () => {
  it("turns repeated SIGTERM requests into one abort and removes its listener", () => {
    const target = new EventEmitter();
    const root = fs.mkdtempSync(path.join(os.tmpdir(), "agent-compose-runtime-shutdown-"));
    const readyFile = path.join(root, "ready", "execution-id");
    try {
      const shutdown = new RuntimeShutdownController(
        target as Pick<NodeJS.Process, "on" | "off">,
        { filePath: readyFile, pid: 1234 },
      );
      const aborted = vi.fn();
      shutdown.abortController.signal.addEventListener("abort", aborted);

      expect(fs.readFileSync(readyFile, "utf8")).toBe("1234");
      expect(target.listenerCount("SIGTERM")).toBe(1);
      target.emit("SIGTERM");
      target.emit("SIGTERM");

      expect(shutdown.abortController.signal.aborted).toBe(true);
      expect(aborted).toHaveBeenCalledTimes(1);
      shutdown.dispose();
      shutdown.dispose();
      expect(target.listenerCount("SIGTERM")).toBe(0);
      expect(fs.existsSync(readyFile)).toBe(false);
    } finally {
      fs.rmSync(root, { recursive: true, force: true });
    }
  });
});
