// audit: behavior - Registers one memory tool with codemode false and forwards args to memory.execute.
// audit: regression - Code Mode hiding the tool or dropping arguments must be caught.
// audit: coverage - Covers the schema surface and the execute delegation seam.
// audit: seam - A fake ToolEditor captures registration without an OpenCode host.

import { describe, expect, test } from "bun:test";
import { Effect } from "effect";
import { TOOL_NAME, TOOL_OPTIONS, memoryToolDefinition, registerMemoryTool } from "./tools";

describe("memory tool", () => {
  test("registers with codemode false so direct dispatch reaches the engine", async () => {
    const added: Array<{ definition: any; options: any }> = [];
    const ctx = {
      tool: {
        transform(edit: (editor: any) => any): any {
          edit({ add(definition: any) { added.push({ definition, options: definition.options }); } });
          return Effect.void;
        },
      },
    };
    await Effect.runPromise(registerMemoryTool(ctx as any, {} as any));

    expect(added).toHaveLength(1);
    expect(added[0].definition.name).toBe(TOOL_NAME);
    expect(added[0].definition.input).toBeDefined();
    expect(added[0].options).toEqual(TOOL_OPTIONS);
    expect(TOOL_OPTIONS.codemode).toBe(false);
  });

  test("schema carries every documented argument", () => {
    const props = Object.keys(memoryToolDefinition.input.properties);
    for (const field of [
      "target", "operation", "requestID", "name", "description",
      "body", "reason", "revision", "query", "scene",
    ]) {
      expect(props).toContain(field);
    }
  });

  test("forwards the full argument set to memory.execute", async () => {
    const calls: any[] = [];
    const client = { memoryExecute: async (args: any) => { calls.push(args); return { content: "ok" }; } };
    const args = {
      target: "project", operation: "search", requestID: "r1", name: "n",
      description: "d", body: "b", reason: "", revision: "", query: "build", scene: "",
    };

    const execution = memoryToolDefinition.execute(args, client);
    expect(typeof (execution as any).pipe).toBe("function");
    const result = await Effect.runPromise(execution as any);

    expect(calls).toHaveLength(1);
    expect(calls[0]).toEqual(args);
    expect(result).toEqual({ content: "ok" });
  });
});
