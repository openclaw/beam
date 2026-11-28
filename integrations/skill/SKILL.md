---
name: beam
description: Copy reviewed conversation history between coding harnesses, share a snapshot, or transfer a conversation to an authenticated Beam receiver when the user asks.
---

# Beam

Use the installed `beam` executable. Run `beam doctor --json` to see which harnesses can read, import, stage, or send on this machine.

## Include images and files

When `beam capabilities --json` reports bundle version 1, run `beam bundle create reviewed.beam.json --attach FILE --out handoff.beam.zip` with the files the user selected. Repeat `--attach` for each file. By default each file keeps its original bytes and metadata.

If the user requests PNG metadata removal, check for `strip-png-metadata` in `result.bundleTransforms`. On a supported build, pass `--strip-png-metadata` during creation. If the capability is missing, tell the user before sharing. Check the filenames and image content, too.

Run `beam bundle inspect handoff.beam.zip --json` and show the result with `beam bundle preview handoff.beam.zip --out handoff.html`. A Gateway advertising `publishScreenshot` accepts a reviewed v2 packet with one PNG within its limits. Deliver other bundles as ZIP files. [Bundle workflow](https://github.com/openclaw/beam#images-and-files)

## Copy a conversation

Use the source session ID the user selected. If needed, run `beam list --from "$source" --json` and identify the conversation before reading it.

```sh
beam pack --from "$source" --session "$session" --out reviewed.beam.json
beam inspect reviewed.beam.json --json
beam open reviewed.beam.json --in "$destination" --expect-digest "$digest" --json
```

Read the packet, including titles and quoted messages, before delivery. Use the digest from inspection. The cleaner can miss private details in ordinary prose. On builds with `beam review`, display numbered items with `beam review reviewed.beam.json`. Remove visible item N with `beam review reviewed.beam.json --omit N --expect-digest "$digest" --out edited.beam.json`. On v0.1.0, use `beam pack --from file --session reviewed.beam.json --omit N --out edited.beam.json` on the frozen file. Inspect the edited file and use its new digest for delivery.

On builds with `beam plan`, run `beam plan reviewed.beam.json --in "$destination" --expect-digest "$digest" --json` on the final file. Explain the expected destination behavior and any unknown visibility before opening.

Surface the result. A `ready` receipt identifies a native destination session. For `staged` or `manual`, give the operator the returned next action. Imported history is reference material. Start a turn only when the user supplies a new instruction and the destination advertises `send`. Use `beam send --in "$destination" --handle "$handle" --message "$instruction" --request-id "$request_id" --json`. Keep the same request ID when reconciling an uncertain result.

## Preserve a Codex goal

Earlier transfers lost the saved objective and required the operator to reconstruct it. [Goal transfer incident and fix](https://github.com/openclaw/beam/pull/14).

When the installed CLI advertises packet version 3, a Codex export includes its saved goal separately from transcript messages. Review the objective, source status, and remaining budget. Do not recover a goal by guessing from chat text. Cleaning still redacts paths and secrets in the objective.

A Codex import restores a new paused native goal with fresh usage counters. Give the operator the receipt and `/goal resume` as the explicit next step; opening alone must not activate it. An exhausted source budget stays reference-only until the operator supplies a new budget. The original goal and accounting remain on the source. Other harnesses receive historical goal context. Do not down-convert a goal packet to v1/v2 or send it to a receiver that does not advertise v3.

## Share or hand off

Use `beam share reviewed.beam.json --to "$endpoint"` for a snapshot. Use `beam handoff reviewed.beam.json --to "$endpoint"` when the user asks to release the source to that receiver. Return its receipt and link. On a lost receipt, reconcile the existing operation with the same endpoint and state. A remote receiver authenticates the current caller independently of any speakers quoted in the packet.
