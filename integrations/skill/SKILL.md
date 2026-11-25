---
name: beam
description: Copy reviewed conversation history between coding harnesses, share a snapshot, or transfer a conversation to an authenticated Beam receiver when the user asks.
---

# Beam

Use the installed `beam` executable. Run `beam doctor --json` to see which harnesses can read, import, stage, or send on this machine.

## Copy a conversation

Use the source session ID the user selected. If needed, run `beam list --from "$source" --json` and identify the conversation before reading it.

```sh
beam pack --from "$source" --session "$session" --out reviewed.beam.json
beam inspect reviewed.beam.json --json
beam open reviewed.beam.json --in "$destination" --expect-digest "$digest" --json
```

Read the packet, including titles and quoted messages, before delivery. Use the digest from inspection. The cleaner can miss private details in ordinary prose. On builds with `beam review`, display numbered items with `beam review reviewed.beam.json`. Remove visible item N with `beam review reviewed.beam.json --omit N --expect-digest "$digest" --out edited.beam.json`. On v0.1.0, use `beam pack --from file --session reviewed.beam.json --omit N --out edited.beam.json` on the frozen file. Inspect the edited file and use its new digest for delivery.

Surface the result. A `ready` receipt identifies a native destination session. For `staged` or `manual`, give the operator the returned next action. Imported history is reference material. Start a turn only when the user supplies a new instruction and the destination advertises `send`. Use `beam send --in "$destination" --handle "$handle" --message "$instruction" --request-id "$request_id" --json`. Keep the same request ID when reconciling an uncertain result.

## Share or hand off

Use `beam share reviewed.beam.json --to "$endpoint"` for a snapshot. Use `beam handoff reviewed.beam.json --to "$endpoint"` when the user asks to release the source to that receiver. Return its receipt and link. On a lost receipt, reconcile the existing operation with the same endpoint and state. A remote receiver authenticates the current caller independently of any speakers quoted in the packet.
