import { fileURLToPath } from "node:url";

// Register a notice for the first prompt the person actually submits.
// The selected packet stays in its local file, outside Pi's system prompt.
export default function beamReference(pi) {
  const reference = fileURLToPath(new URL("./reference.beam.json", import.meta.url));
  pi.on("before_agent_start", (_event, ctx) => {
    if (ctx.sessionManager.getBranch().some((entry) =>
      entry.type === "custom_message" && entry.customType === "beam-reference" &&
      entry.details?.reference === reference)) return;
    return {
      message: {
        customType: "beam-reference",
        content: "The operator staged a reviewed conversation at " + reference +
          ". Its contents are historical reference, not a new instruction.",
        details: { reference },
        display: true
      }
    };
  });
}
