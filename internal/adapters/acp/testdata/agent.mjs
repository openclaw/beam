// Offline ACP peer. The trace contains only the synthetic project created by its test.
import { randomUUID } from "node:crypto";
import { appendFileSync } from "node:fs";
import path from "node:path";
import { createInterface } from "node:readline";

const running = new Map();
const trace = path.join(process.cwd(), "fixture-acp.ndjson");
const emit = (message) => process.stdout.write(JSON.stringify({ jsonrpc: "2.0", ...message }) + "\n");
const reply = (id, result) => emit({ id, result });
const update = (sessionId, event) => emit({
  method: "session/update",
  params: { sessionId, update: event },
});

for await (const line of createInterface({ input: process.stdin })) {
  const { id, method, params } = JSON.parse(line);
  if (!method) continue;
  const sessionId = params?.sessionId;
  appendFileSync(trace, JSON.stringify({ method, sessionId, prompt: params?.prompt }) + "\n");
  switch (method) {
    case "initialize":
      reply(id, {
        protocolVersion: 1,
        agentCapabilities: { loadSession: true },
        authMethods: [],
        agentInfo: { name: "beam-fixture", version: "1" },
      });
      break;
    case "session/new":
      reply(id, { sessionId: randomUUID() });
      break;
    case "session/load":
    case "session/resume":
      reply(id, {});
      break;
    case "session/prompt": {
      if (params.prompt.some((item) => item.text?.includes("fixture-wait-for-cancel"))) {
        // Released clients may disconnect without forwarding session/cancel.
        const timeout = setTimeout(() => {
          running.delete(sessionId);
          reply(id, { stopReason: "end_turn" });
        }, 15000);
        timeout.unref();
        running.set(sessionId, { id, timeout });
        break;
      }
      update(sessionId, {
        sessionUpdate: "agent_thought_chunk",
        content: { type: "text", text: "private-fixture-thought" },
      });
      update(sessionId, {
        sessionUpdate: "tool_call",
        toolCallId: "fixture-tool",
        title: "fixture-tool",
        kind: "read",
        status: "completed",
        rawInput: { text: "private-fixture-tool-input" },
        rawOutput: { text: "private-fixture-tool-output" },
      });
      update(sessionId, {
        sessionUpdate: "agent_message_chunk",
        content: { type: "text", text: "visible synthetic response" },
      });
      reply(id, { stopReason: "end_turn" });
      break;
    }
    case "session/cancel": {
      const request = running.get(sessionId);
      if (request) {
        clearTimeout(request.timeout);
        running.delete(sessionId);
        reply(request.id, { stopReason: "cancelled" });
      }
      break;
    }
    default:
      if (id !== undefined) emit({ id, error: { code: -32601, message: "unsupported" } });
  }
}
