// Registers the single memory tool for direct model dispatch.

import { Effect } from "effect";

export const TOOL_NAME = "memory";
export const TOOL_OPTIONS = { codemode: false };

const stringField = (description: string) => ({ type: "string", description });

export const memoryToolDefinition = {
  name: TOOL_NAME,
  description: "Read, search, and change Recollect memory within the bound project or user scope.",
  input: {
    type: "object",
    properties: {
      target: { type: "string", enum: ["user", "project"], description: "Memory scope." },
      operation: {
        type: "string",
        enum: ["read", "search", "append", "correct", "delete"],
        description: "Memory operation.",
      },
      requestID: stringField("Idempotency key for mutations."),
      name: stringField("Stable fact name."),
      description: stringField("Short fact description."),
      body: stringField("Fact body."),
      reason: stringField("Reason required for user append."),
      revision: stringField("Revision from a preceding read for edits."),
      query: stringField("Search query."),
      scene: stringField("Optional search scene."),
    },
    required: ["target", "operation"],
  },
  options: TOOL_OPTIONS,
  execute: (args: unknown, client: { memoryExecute(params: unknown): Promise<any> }) =>
    client.memoryExecute(args),
};

export function createMemoryTool(client: { memoryExecute(params: unknown): Promise<any> }) {
  return {
    ...memoryToolDefinition,
    execute: (args: unknown) => memoryToolDefinition.execute(args, client),
  };
}

export function registerMemoryTool(
  ctx: any,
  client: { memoryExecute(params: unknown): Promise<any> },
): Effect.Effect<void> {
  return Effect.gen(function* () {
    yield* ctx.tool.transform((tools: any) => {
      tools.add(createMemoryTool(client));
    });
  });
}
