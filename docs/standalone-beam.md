---
title: Standalone Beam and OpenClaw session portability
summary: "Beam CLI, packet and adapter contracts, receiver protocol, verified harness behavior, and OpenClaw integration targets."
read_when:
  - Implementing or reviewing a Beam reader, destination, or receiver
  - Changing OpenClaw session catalog access, export, or adoption
---

# Standalone Beam and OpenClaw session portability

**Status.** Beam v0.2.0 ships packet review, destination planning, adapter tests, and portable asset bundles. OpenClaw main at `7071b82bac28` includes the bundled v2 receiver, caller-scoped catalog routes, continuation journal, and managed source mirroring. The separately tagged OpenClaw 2026.11.8 registers the v1 endpoint. The original design baseline was OpenClaw `b9ef0d7fca28` and agent-skills `1fa548f4ad81`. [Standalone release](https://github.com/openclaw/beam/releases/latest) · [Integrated receiver](https://github.com/openclaw/openclaw/blob/7071b82bac28e93782cb56ba4559aaab0bebe049/extensions/beam/index.ts) · [Tagged v1 receiver](https://github.com/openclaw/openclaw/blob/v2026.11.8/extensions/beam/index.ts) · [Tested harnesses](https://openclaw.github.io/beam/#support)

## Operator experience

Install one `beam` executable. Choose a source session, review the cleaned conversation, then choose where it goes. The recipient uses their own harness, workspace, credentials, and tool permissions. A local transfer works without an OpenClaw installation or Beam account. Give OpenClaw users the existing `openclaw plugins enable beam` entry point.

```sh
beam doctor --json
beam list --from claude
beam pack --from claude --session <id> --out repair.beam.json
beam inspect repair.beam.json
beam open repair.beam.json --in codex
```

| User action                     | Observable result                                                                                                                                                                                      |
| ------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `list` / `pack`                 | Select an exact session and branch. Produce a bounded file. Packing starts zero model turns.                                                                                                           |
| `inspect` / `render`            | Inspect all outgoing fields or render the file as quoted Markdown for a manual recipient.                                                                                                              |
| `clean --protocol 1`            | Read one authorized candidate projection on stdin. Write the validated portable packet to stdout. This is the entry point for OpenClaw mirroring and other hosts with their own history reader.        |
| `open FILE --in HARNESS`        | Make an independent copy or stage a reference. Return its precise state and how to open it.                                                                                                            |
| `share FILE --to TEAM`          | Publish a readable snapshot and return a link with the receiver's access rules.                                                                                                                        |
| `pull LINK --out FILE`          | Read the authorized retained snapshot into a portable file. If only the binding survives, report that state. Exporting the destination's newer conversation is a separate authorized source selection. |
| `handoff FILE --to TEAM`        | Release this publication to one receiver. Reopening its link resolves the same bound destination under current access rules.                                                                           |
| `continue LINK --message "..."` | Submit this new instruction to the destination. Reuse its own request ID on retries.                                                                                                                   |

`beam open` defaults to `--mode auto`. Beam tries the adapter’s advertised passive native copy or stages a reference. Use `--mode native` to require a verified native session. A staged receipt contains a handle and the next action. `beam resolve --in HARNESS --handle HANDLE` reopens that reference and `beam lookup --in HARNESS --operation-id ID` checks a previous operation. Supply a new instruction with `beam send --in HARNESS --handle HANDLE --request-id UUID --message TEXT` when the adapter supports send. An explicit `--message` on `open` combines preparation and send. A handoff retires future Beam writes to that receiver publication. Other copies and the original source harness remain independent.

The direct `push` command combines source selection and destination delivery. For example, `beam push --from claude --session <id> --to opencode` creates a local copy. Add `--dry-run` to write the cleaned packet to stdout and its digest to stderr before contacting the destination. On a subsequent push, `--expect-digest sha256:<64 lowercase hex digits>` checks that the exact outgoing bytes still match. Use `beam pack --from HARNESS --session ID --out -` to stream the packet. A remote publication requires an explicit `share` or `handoff`.

For remote commands, choose the receiver independently of the supplied link. Pass `--endpoint https://team.example/team` or set `BEAM_ENDPOINT`. Use `--profile-dir DIR` when a shortened human link needs the corresponding OpenClaw remote profile.

For a v1 receiver, run `beam convert repair.beam.json --packet-version 1 --out repair.v1.json`, then `beam inspect repair.v1.json` and review the projected bytes. The v1 packet omits speaker aliases. Choose the v1 endpoint explicitly when sharing the reviewed projection.

Both ends can be ordinary harnesses. A tool-enabled agent can run `beam pack` or `beam open` directly. A second machine can receive the same reviewed bytes through an existing authenticated transport:

```sh
ssh devbox 'beam open - --in opencode --json' < repair.beam.json
```

The command runs as the selected account on the target machine. It requires Beam and a supported target harness there. An interactive-only target returns a staged reference or a manual outcome. A shared Gateway sends only the history authorized for the requesting person to a child process.

### OpenClaw quickstart conformance

The [Pages workflow](../.github/workflows/pages.yml) pins acpx 0.22.0, OpenCode 1.19.6 and OpenClaw 2026.11.10. It runs two independent checks before publishing the [harness results](https://openclaw.github.io/beam/#support).

- The [profile suite](../conformance/e2e/acpx-profiles.mjs) compares the homepage with the installed acpx registry. Each profile records a conversation with an offline ACP peer in a fresh project. Beam lists and packs the selected session, removes a test email and private blocks, stages the result and resolves the same reference. It also checks that changing projects or profiles hides the selected record.
- The [OpenCode test](../conformance/e2e/native_test.go) starts the installed OpenCode ACP server through acpx and sends one instruction to a loopback model. The [Gateway test](../conformance/e2e/legacy_openclaw_test.go) takes that exact export, converts it to v1, inspects it and shares it with a bearer to an isolated OpenClaw Gateway. It checks the returned link, the stored messages, and zero model requests on the Gateway.

## Repository ownership and distribution

| Repository | Current owner and contract |
| --- | --- |
| `openclaw/beam` | Owns the Go CLI, cleaner, schemas, harness adapters, receiver client, conformance fixtures, skill assets and one-page website. |
| `openclaw/openclaw` | Bundles the default-off Beam plugin and verified native cleaner. The plugin owns receiver policy, catalog projection and mirroring. The Gateway owns verified caller identity, catalog access and Team sessions. [Bundled cleaner](https://github.com/openclaw/openclaw/pull/204593) |
| `openclaw/agent-skills` | Keeps the installed hook entry point, legacy arguments and receipts. The skill delegates Gateway transport to a compatible Beam executable on PATH and retains its bundled sender for older installations. [Installed skill bridge](https://github.com/openclaw/agent-skills/pull/407) |

The standalone repository contains these implementation directories.

```text
cmd/beam/                     native CLI and machine entry points
internal/packet/              wire validation, bounded serialization and rendering
internal/clean/               shared export policy and omission report
internal/adapters/            codex, claude, opencode, pi, gemini, acp, openclaw, file
internal/receiver/            negotiation, HTTP client, auth and receipts
internal/journal/             local pending handoffs and recovery
schema/                       packet, CLI, adapter and skill schemas, generated packet validator
conformance/                  synthetic input/output vectors and provider tests
integrations/                 target-native hook/SDK bridges and compatibility skill
site/                         one static page and links to release artifacts
```

Beam publishes CLI archives for macOS, Linux, and Windows on x64 and arm64. The release includes `SHA256SUMS`, platform cleaner executables, and manifests. The version tag pins the schemas and generated TypeScript packet validator consumed by the connector. Target-native helpers ship with their adapters. [Native downloads and checksums](https://github.com/openclaw/beam/releases/latest) · [v0.2.0 schemas](https://github.com/openclaw/beam/tree/v0.2.0/schema)

The bundled connector uses its installed packet validator for receipt and viewing. On supported OpenClaw platforms, the package includes the matching native cleaner for outbound cleaning and mirroring. The plugin verifies its platform asset before executing it. Source-only or unsupported platforms can still receive and should report the exact missing outbound capability. An official catalog listing alone cannot satisfy one-command enablement because the [enable lifecycle](https://github.com/openclaw/openclaw/blob/b9ef0d7fca28cfea8dec4472d4527d98c40d7ba2/src/plugins/management-mutations.ts#L342-L349) looks up an installed plugin.

Bundling the six v0.1.0 cleaners added **6,808,001 bytes** to the compressed OpenClaw npm package in a controlled packaging run. The same candidate tree produced 121,609,113 bytes with the assets and 114,801,112 bytes without them. Only the six native files and two generated package inventories differed. Operators incur that download cost even when Beam stays disabled. The Codex catalog plugin is a separate source-side install. [Packaging evidence](https://github.com/openclaw/openclaw/pull/204593)

The public page needs the install command, two-harness walkthrough, synthetic cleaning example, per-version support matrix, schema, and releases. Host it statically. A running Beam server, browser-side cleaner, and sign-in are unnecessary for this page.

## Portable asset bundles

A `.beam.zip` travels as an ordinary file. Use a single-disk ZIP32 archive without an archive comment. Store all members uncompressed. The central directory limit is 16 KiB. At the archive root, `packet.beam.json` holds the exact reviewed v1 or v2 packet and `manifest.json` follows the [v1 bundle schema](../schema/bundle-v1.schema.json). The packet retains its original SHA-256. The bundle digest covers the complete ZIP, including the manifest and all attachments.

Each attachment has a display filename, media type, byte count, SHA-256 and a generated path such as `assets/01.png`. Number paths in manifest order, starting at 01. Use only the listed regular files. A bundle contains 1–16 attachments, up to 16 MiB each and 32 MiB combined. The manifest limit is 16 KiB. The full archive limit is 32 MiB plus 128 KiB. The existing packet limit is 56 KiB.

Beam detects PNG, JPEG, GIF, WebP, PDF, UTF-8 text, ID3-tagged MP3, WAV, MP4 and WebM. By default it copies the selected files byte for byte. This includes image metadata and document contents. The packet cleaner applies to the conversation. Inspect the inventory and review the files before sending them.

For static PNGs, `beam bundle create PACKET --attach image.png --strip-png-metadata --out handoff.beam.zip` re-encodes the image before computing its byte count and digest. Check for `strip-png-metadata` in `result.bundleTransforms` from `beam capabilities --json`. Beam v0.2.0 predates this option.

The cleaned PNG drops EXIF (including embedded location), text, timestamps, color profiles, and trailing data. Removing a color profile or orientation tag can change how the image looks. Preview the final bundle and review any sensitive details in the pixels and filename. The option applies by detected file type. Other files keep their original bytes. Animated PNGs require a separate static export. The cleaning limit is 32,768 pixels per side and 16,777,216 total pixels. The input file and encoded result must each fit the per-file byte limit.

`beam bundle inspect FILE --json` checks the member names, hashes, lengths, types and packet schema before returning the manifest and bundle digest. To compare against a digest supplied by the sender, add `--expect-digest sha256:…`. `beam bundle preview FILE --out preview.html` creates an offline HTML page with inline images, playback controls and downloadable documents. Any ZIP tool can extract the packet and original attachments. A v2 Gateway advertising `publishScreenshot` and `maxScreenshotBytes` accepts `beam share task.beam.zip --to ENDPOINT` for a v2 packet and one PNG within its limits. The CLI stops before publication on an unsupported receiver. To share the conversation with a text-only Gateway, extract `packet.beam.json` and follow the regular packet-sharing workflow.

For a screenshot, the normal conditional snapshot PUT uses `application/vnd.openclaw.beam.screenshot+json` and a JSON object with three strings: `packet` (exact reviewed v2 JSON text), `name` (display filename), and `png` (standard padded base64). The receipt and head retain the packet digest. Their additional `presentationDigest` is SHA-256 of the UTF-8 filename, one zero byte, and the PNG bytes, expressed as `sha256:<hex>`. A changed image or filename changes the revision. Sharing the text packet by itself removes the previous screenshot, even when the packet bytes match. Handing off that same packet releases the existing publication with its preview. A read of `{apiUrl}/image` uses the same current reader permissions as the snapshot. OpenClaw's first implementation accepts one PNG up to 512 KiB and 16,777,216 pixels.

## Portable data and cleaning

The v2 packet uses a closed Draft 2020-12 JSON Schema and a transport-level byte limit. The v1 endpoint retains its own packet contract. [V2 schema](../schema/packet-v2.schema.json) · [V1 schema](../schema/packet-v1.schema.json) · [Cleaner input](../schema/packet-candidate-v1.schema.json)

```ts
type PacketV2 = {
  version: 2;
  beamId: string;
  source: string;
  title: string;
  updatedAt: string;
  completed: boolean;
  truncated?: boolean;
  sourceModel?: { provider: string; model: string };
  items: Array<{
    type: "userMessage" | "agentMessage" | "other";
    text: string;
    speaker?: string;
  }>;
};
```

```json
{
  "version": 2,
  "beamId": "0123456789abcdef0123456789abcdef",
  "source": "claude",
  "title": "CSV importer",
  "updatedAt": "2026-11-24T08:00:00Z",
  "completed": false,
  "items": [
    { "type": "userMessage", "speaker": "p1", "text": "Keep quoted newlines inside a cell." },
    { "type": "agentMessage", "text": "The parser is fixed. The Windows test is still missing." }
  ]
}
```

- Closed objects at every level. Validate version before reading the remainder. Reject duplicate object keys, invalid UTF-8, unknown kinds, and malformed dates on v2 ingress. A document is at most 57,344 UTF-8 bytes including JSON syntax. Measure after serialization.
- `beamId` is 32 lowercase hex characters. `source` and model provider are lowercase identifiers matching `[a-z0-9._-]+`, up to 32 and 64 characters respectively. Title is nonblank, at most 160 UTF-16 code units. Model name is 1–256 code units with no whitespace. `updatedAt` is a valid RFC 3339 instant with a zone, up to 64 characters. `completed` describes the source run. All text limits use UTF-16 code units for v1 parity. Clip on complete Unicode scalar boundaries.
- Keep 1–200 nonblank items, oldest first, up to 6,000 UTF-16 code units per item. `other` carries a plain descriptive summary. `speaker` is an optional `p1` through `p200` packet-local alias on a user item. Display an anonymous participant when source authorship is unavailable. A speaker alias confers no host identity, membership, permissions, or Git attribution.
- Receiver generation, revision, request ID, access rights, destination handle, hook event, local paths, and credentials belong outside the v2 file. v1's optional `handoffId` is a legacy transport control field. A file reader must never execute a release from a field it reads. A model hint must pass the destination's own model policy.
- A branch selector is local and opaque. The standalone source identity function takes the tuple `(adapter, sourceHome, nativeSession, branch)` with a length prefix for each UTF-8 component and a fixed `beam-source-v2` domain prefix. Take the first 16 bytes of SHA-256 as lowercase hex. Prefix lengths are unsigned 32-bit big-endian. Use an empty component for an absent branch. Adapters that cannot establish the same stable origin must publish separate origins. Preserve existing v1 identities and URLs. Adopt an old ID only when its exact origin mapping is established. Never merge on a matching title or visible text.
- Each adapter selects only visible conversation text. Omit system/developer messages, hidden reasoning, tool arguments/results, binaries and ambient workspaces. Apply one cleaning pass to every exported string including titles, summaries, model hints and attachment labels. Default attachment placeholders expose only kind and optionally size. Support explicit item omission and literal redaction locally. Automated secret detection is best effort.
- Clean before fitting to the wire limit. Drop oldest items first to retain recent work, clip an oversized remaining item at a scalar boundary, and set `truncated`. Reject a result with no shareable content. Report redactions, omissions, lossy conversions and truncation separately from the file. `inspect` reads the exact file to be sent. Publishing reviewed v2 bytes must not silently reserialize or down-convert them. Materialize and review a v1 projection separately when required.

Existing numeric limits and item types come from the [v1 parser](https://github.com/openclaw/openclaw/blob/b9ef0d7fca28cfea8dec4472d4527d98c40d7ba2/extensions/beam/src/types.ts#L18-L85). Two regression vectors come from the [filename placeholder](https://github.com/openclaw/agent-skills/blob/1fa548f4ad814f0f8401623c37c9d4b61e050173/skills/beam/scripts/beam-session.js#L720-L735) and the [automatic mirror's visible-text behavior](https://github.com/openclaw/openclaw/blob/b9ef0d7fca28cfea8dec4472d4527d98c40d7ba2/docs/plugins/beam.md#L204-L216).

## CLI and third-party adapter contract

`beam doctor --json` detects locally installed executables, reports their actual versions, adapter protocol range and supported operations. A missing harness disables that entry only. Use a fixed executable and argument array. Machine calls are noninteractive unless the user explicitly starts login or an interactive harness. A header, packet, or remote peer cannot choose a local executable, directory, model, or helper.

Ship built-ins for the supported targets. Offer the same contract to external readers and writers through a locally configured executable. It needs to implement only the operations it advertises. A decoder can ship independently of a writer. Exchange one UTF-8 JSON request on stdin and one closed, versioned JSON response on stdout per process. Put bounded, redacted human diagnostics on stderr. The host terminates work at timeout or cancellation and retains the operation ID for an uncertain write.

```ts
type AdapterCapabilities = {
  protocol: 1;
  adapter: string;
  version: string;
  source: Array<"list" | "read">;
  destination: Array<"stageReference" | "openCopy" | "send" | "resolve">;
  recovery: "lookupByOperationId" | "manual";
};

type AdapterOp =
  | { op: "capabilities" }
  | { op: "list"; cursor?: string; limit: number }
  | { op: "read"; selection: string; cursor?: string; limit: number }
  | { op: "stageReference" | "openCopy"; operationId: string; packet: PacketV2 }
  | { op: "resolve"; handle: string }
  | { op: "lookupOperation"; operationId: string }
  | { op: "send"; handle: string; requestId: string; instruction: string };

type OpenResult =
  | { state: "ready"; handle: string; nativeSession: string }
  | { state: "staged"; handle: string; nextAction: string }
  | { state: "manual"; artifact: string; nextAction: string }
  | { state: "needs_reconciliation"; operationId: string; nextAction: string };
```

Wrap each machine request with `{protocol:1, request:<AdapterOp>}` and result with either `{protocol:1, ok:true, result:...}` or `{protocol:1, ok:false, error:{code, retry, message}}`. `list` returns `{entries:[{selection,title}],nextCursor?}`. `read` returns `{source,origin,branch?,title,updatedAt,completed,items,nextCursor?,completeness}`. Here `origin` is the private tuple `{adapter,sourceHome,nativeSession}`, `branch` is the optional selected branch, and `completeness` is `complete` or `partial`. Each candidate item is `{type,text,speakerId?}` with the three portable item types. Only a user message may carry `speakerId`. The cleaner replaces that source-scoped identifier with a packet-local alias. Project an attributed assistant message as `agentMessage` with its visible text and omit its source identity. The adapter must report gaps. The host assembles one branch oldest first, bounds the read, maps aliases, cleans it and excludes private IDs from wire data. Pagination defaults to 50, max 200 items per page. Budget each adapter response at 4 MiB and each exported document at 56 KiB. Cursors are opaque and bound to the selection and read generation. An invalidated cursor restarts an explicit read or yields `source_changed`.

From OpenClaw main `6d4e300f`, the mirror applies the user-only speaker rule when a catalog also attributes an assistant reply. In a registered-catalog test, the Beam v0.1.0 cleaner retained the assistant reply between two users’ messages and assigned the first user the same alias when they spoke again. [Multiplayer projection](https://github.com/openclaw/openclaw/pull/204810)

For `beam clean --protocol 1`, the host supplies one assembled `read` result with the cursor omitted and the same origin and branch fields. On success stdout contains only v2 packet bytes. Omission counts go to stderr without source text. An incomplete read sets `truncated`. Use `beam capabilities --json` to negotiate CLI protocol, packet versions, and built-in adapter IDs before invoking it. `doctor` also probes the tools installed on the current machine.

The public schemas define the request, common success/error envelope and operation-specific result. The conformance test runs an external process that implements those JSON contracts directly, selected with `--adapter ID=/absolute/executable` on the relevant Beam subcommand. [Request](../schema/adapter-v1.schema.json) · [Envelope](../schema/cli-v1.schema.json) · [Results](../schema/adapter-result-v1.schema.json) · [Executable conformance](../conformance/e2e/external_test.go)

Before launching a mutating adapter operation, the host durably records its operation ID when retry recovery is requested. An adapter advertising `lookupByOperationId` must implement `lookupOperation`. It accepts the original ID even if the child exited before emitting a handle. It returns `{state:"pending"|"unknown"}` or the recorded `OpenResult`. An unknown outcome never authorizes an automatic second create.

`resolve` returns the current `OpenResult` and performs no creation. `send` returns `{state:"accepted"|"completed",requestId,handle}` with the strength supported by the harness. Emit `ready` only after native persistence and reopen are verified. A staged reference may live in a user-selected file or a private, bounded pending store until consumed or explicitly discarded. Its `handle` must continue to work after Beam exits. Retry creates a new copy only when the person explicitly requests another one. A timed-out target that cannot find a prior operation yields `needs_reconciliation`. It must not advertise durable handoff.

Public CLI machine exit codes are 0 for a completed command (including an explicitly requested staged or manual outcome), 2 for usage/invalid input, 3 for unsupported or unavailable capability, 4 for authentication/authorization, 5 for conflict or reconciliation, and 6 for a transient transport failure. A request for a passive native copy cannot silently degrade to a model turn or manual outcome. `pack --out -` and `render` stream artifact bytes. `--json` commands emit the envelope. Unknown major protocol versions fail before a mutating operation. Schema changes, new required permissions, and weaker receipt semantics require explicit negotiation.

For example, a third-party writer receives the following read-only probe on stdin:

```json
{ "protocol": 1, "request": { "op": "capabilities" } }
```

A minimal writer for a prompt-only harness can return:

```json
{
  "protocol": 1,
  "ok": true,
  "result": {
    "protocol": 1,
    "adapter": "example",
    "version": "1.0.0",
    "source": [],
    "destination": ["stageReference", "resolve", "send"],
    "recovery": "manual"
  }
}
```

Beam can then request `stageReference` with the reviewed packet. The writer returns `staged` and a local handle. A later `send` carries the fresh instruction. An independent source adapter only needs `list` and `read`.

## Out-of-box harness coverage

The operator installs the harness and supplies their own credentials. Run `beam doctor --json` to see which operations that installation supports. Each row distinguishes reading existing history, preparing a destination without a model turn, and sending a fresh instruction. The release floor requires a Codex ↔ OpenCode round trip, Claude Code read and staged receive, manual file transfer, and OpenClaw receive and authorized export.

| Adapter | Selected source | Destination behavior |
| --- | --- | --- |
| Codex 0.172.0 | Read one thread’s visible conversation and recognize Beam’s quoted reference frame on re-export. | `ready` after opening a durable thread. The imported frame persists in model context. Its historical items do not appear as individual turns in the native transcript. |
| OpenCode 1.19.6 | Export a selected session and project visible text. | `ready` after creating a session and storing one quoted reference message with `noReply:true`. The reference appears in native history and export. |
| Claude Code 2.1.296 | After an explicit turn, Beam read the selected native session by ID and recovered the user question and assistant reply. | `staged`. Open made zero model calls. A per-invocation SessionStart hook supplied a reference path. One explicit instruction sent that path and the new question to the loopback model. |
| Pi 0.83.0 | Read the selected session branch. After an explicit turn, Beam read the native session ID and recovered the new question and answer. | `staged`. Native startup had zero messages and model calls. The extension attached one reference notice to the first explicit turn. |
| Gemini CLI 0.45.0 | Beam read the fresh native ID created on import. | `staged`. Importing the generated session file persisted a reference notice under a new native ID before Gemini requested login. The test ended at login with zero user turns. |
| ACP / ACPX | `--from acpx:<agent>` (also `acp:<agent>`) reads selected project history. Stock acpx 0.22.0 does not establish completeness, so Beam marks the packet truncated. | `staged` on 0.22.0. Lookup and resolve recover the private reference after restart. Programmatic send requires an acpx whose prompt command supports `--expect-record` to verify the immutable session record. |
| File | Read a selected Beam packet or UTF-8 text file. | `manual`. Render a Markdown artifact for the recipient to attach. |
| OpenClaw 2026.6.11 | An independently paired read-only device exported the selected session and excluded the second fixture conversation. This Gateway supplied no pagination receipt, so its packet was marked truncated. A wrong-token device received an authentication error (exit 4) and no packet. [Source conformance](https://github.com/openclaw/beam/blob/196f25c7183533a4f53c1d7fb3d673e76b99a90b/internal/adapters/openclaw/native_test.go) | An enabled receiver can advertise remote snapshot and handoff operations. Qualifying first continuation, ordinary Team access and reopen requires the combined host and plugin. |

On Claude and Pi the first model request contained a path to the reviewed reference and the operator’s question. The packet prose stayed in the local artifact. On all three staged bridges `resolve` recovers the Beam handle. Reading a subsequently created native session requires its own source selection. [Claude](https://github.com/openclaw/beam/blob/643d748dbb28dd245901b070f70718bfc1319f8b/internal/adapters/claude/native_test.go) · [Pi](https://github.com/openclaw/beam/blob/643d748dbb28dd245901b070f70718bfc1319f8b/internal/adapters/pi/native_test.go) · [Gemini](https://github.com/openclaw/beam/blob/643d748dbb28dd245901b070f70718bfc1319f8b/internal/adapters/gemini/native_test.go)

On standalone Beam main from `15ab0f6`, an explicit Claude session ID probes its transcript filename in the matching project directories. Beam checks the stored project and session identity, including exact filename spelling for older records. Listing sessions still enumerates the archive. [Selected Claude transcript lookup](https://github.com/openclaw/beam/commit/15ab0f6cbeb99c9ed8676fcd7d75ed180e067108)

The downloaded Beam v0.1.0 macOS arm64 executable completed a native round trip with stock Codex 0.172.0, OpenCode 1.19.6, a synthetic conversation, and a loopback model. Beam reopened and re-exported each native copy in a fresh process. Those operations made zero model requests. An explicit Codex send then made one request containing both the imported reference and the new instruction. [Executable round trip](../conformance/e2e/native_test.go)

The published acpx 0.22.0 supports passive selection and staging. Within the supported 0.22.x series, Beam probes for `acpx <agent> prompt -s <name> --expect-record <record-id> --file -`. When available, the first explicit instruction creates and seeds the native target. A confirmed replay returns its receipt. An ambiguous interruption requires reconciliation. The guarded prompt landed on acpx main after 0.22.0. Beam enables send when the installed binary advertises the flag. [Beam ACP adapter](../internal/adapters/acp) · [acpx immutable record selection](https://github.com/openclaw/acpx/pull/1053)

Evidence for these adapter choices:

- Codex separates [thread read](https://github.com/openai/codex/blob/414782450952a196bbb64b1605a458c2531c47b6/codex-rs/app-server/tests/suite/v2/thread_read.rs#L138-L175), [history injection and turn start](https://github.com/openai/codex/blob/414782450952a196bbb64b1605a458c2531c47b6/codex-rs/app-server/tests/suite/v2/thread_inject_items.rs#L42-L99). Claude documents [selected-session APIs](https://code.claude.com/docs/en/agent-sdk/sessions) and [hook context](https://code.claude.com/docs/en/hooks#add-context-for-claude).
- OpenCode v1.19.6 implements the [passive message path](https://github.com/anomalyco/opencode/blob/f4e363c8295fa90a03dddc451882fffe8fbce357/packages/opencode/src/session/prompt.ts#L1045-L1070). Pi documents [branch traversal](https://github.com/earendil-works/pi/blob/f8149d1abde50b31c8cb5aed09e89319b927f247/packages/coding-agent/src/core/session-manager.ts#L1484-L1498) and the [first persistent write](https://github.com/earendil-works/pi/blob/f8149d1abde50b31c8cb5aed09e89319b927f247/packages/coding-agent/src/core/session-manager.ts#L1170-L1199).
- Gemini documents [hook semantics](https://geminicli.com/docs/hooks/reference/#sessionstart) and [native session import](https://github.com/google-gemini/gemini-cli/blob/03862c4164844c6cf8063b4cc48546ec1609cf2e/packages/cli/src/gemini.tsx#L339-L400). ACP distinguishes [session setup](https://agentclientprotocol.com/protocol/v1/session-setup) and [prompt turns](https://agentclientprotocol.com/protocol/v1/prompt-turn). OpenClaw has a bounded [chat history request](https://github.com/openclaw/openclaw/blob/b9ef0d7fca28cfea8dec4472d4527d98c40d7ba2/packages/gateway-protocol/src/schema/logs-chat.ts#L38-L65).

## Receiver HTTP contract

A remote receiver is optional. The client negotiates the v2 operations available to its authenticated caller. OpenClaw main implements snapshot publication, conditional reads, publisher release, destination resolution, continuation and recovery of the caller’s send receipt. It retains the v1 write endpoint at `/api/v1/beam/sessions`. [Receiver routes](https://github.com/openclaw/openclaw/blob/7071b82bac28e93782cb56ba4559aaab0bebe049/extensions/beam/src/http-v2.ts)

| Method and relative path                                  | Request and receipt                                                                                                                                                                                                                                                                          |
| --------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET /api/v2/beam/capabilities`                           | Authenticated, read-only. Return `{protocol:2,packetVersions,operations,maxBodyBytes,sendReceiptRetentionSeconds}`. This advertises support for the current caller. Release still requires authorization on the selected publication.                                                        |
| `GET /api/v2/beam/snapshots/{beamId}/head` | Return the authorized revision, digest, state and release permission. Return an opaque publication ID and machine link for a known generation, or a conditional snapshot link for a legacy row without one. Inaccessible and absent resources return the same public lookup result. |
| `GET /api/v2/beam/snapshots/{beamId}/snapshot` | Read a legacy unbound row under the same caller’s authorization. Require `If-Match` with the head revision. A stale or missing precondition returns 412. |
| `PUT /api/v2/beam/snapshots/{beamId}`                     | Body is the exact reviewed packet. URL and body IDs must match. Use `If-None-Match: *` for creation or `If-Match` with the head revision for replacement. Return `{beamId,publicationId,revision,digest,disposition,url,apiUrl,canRelease}`. `disposition` is `applied` or `unchanged`.      |
| `GET /api/v2/beam/publications/{id}/snapshot`             | Read the retained, authorized packet. Return revision and digest with the representation. Return a distinct expired result to an authorized caller once only the binding remains.                                                                                                            |
| `POST /api/v2/beam/publications/{id}/release`             | Send `If-Match` and `{digest}`. Recheck the original verified publisher and exact generation. Return `{publicationId,revision,state:"released",url}`.                                                                                                                                        |
| `GET /api/v2/beam/publications/{id}/destination`          | Read-only resolution. Return `released_unbound`, `creating`, `ready` with an opaque destination reference, or `needs_reconciliation`, subject to destination access.                                                                                                                         |
| `GET /api/v2/beam/publications/{id}/requests/{requestId}` | Read this caller's recorded send outcome. Return `pending`, `accepted`, `completed`, or `needs_reconciliation`. An expired or unknown receipt cannot prove a send failed.                                                                                                                    |
| `POST /api/v2/beam/publications/{id}/continue`            | `{requestId,instruction,agent?}`. The initial authorized request fixes the agent, reserves and initializes one destination, then submits the instruction. Later requests use the same binding and ordinary send authorization. Return the destination state and this request's send outcome. |

A receipt may carry three URI references. `url` opens the human view, `apiUrl` identifies the exact publication generation, and `snapshotUrl` reads an unbound legacy snapshot. Resolve each reference against the URL of the request that returned it. Verify that the resolved URL stays on the selected origin and inside its configured mount before sending credentials.

For example, a proxy serves Beam at `https://team.example/team` and strips `/team` before forwarding. The Control UI uses its own `/console` base. Each response can preserve both paths using these references.

| Request under `/team` | Receipt field | Value | Resolved path |
| --- | --- | --- | --- |
| `GET /api/v2/beam/snapshots/{beamId}/head` | `apiUrl` | `../../publications/{id}` | `/team/api/v2/beam/publications/{id}` |
| Same head request | `url` | `../../../../../console/beam/{slug}` | `/team/console/beam/{slug}` |
| `PUT /api/v2/beam/snapshots/{beamId}` | `apiUrl` | `../publications/{id}` | `/team/api/v2/beam/publications/{id}` |

A machine link includes the opaque publication ID and works on an authorized fresh device. For an existing human link with a full Beam ID, the CLI reads its head. A shortened link requires a matching OpenClaw remote profile. The client asks `sessions.catalog.route` for the caller’s permitted list operation and requires one complete, unambiguous match on the registered host and route. [Mounted-client conformance](https://github.com/openclaw/beam/blob/d6bb1e5fab31c3daf59b56bf819293398a9581ba/internal/receiver/mount_test.go)

A row created before publication generations returns `state:"legacy_unbound"`, `canRelease:false`, a revision, a digest and `snapshotUrl:"snapshot"`. The head omits `publicationId` and `apiUrl`. A pull sends `If-Match` to the snapshot URL under the same caller. The client verifies the response ETag, the `Beam-Digest` header and the SHA-256 of the returned v2 bytes against the head. Reading leaves the row unbound. Explicit republication uses `If-Match` and the receiver’s write policy. A known generation and verified publisher are prerequisites for release. Legacy uploader attribution alone supplies neither. [OpenClaw legacy-row conformance](https://github.com/openclaw/openclaw/blob/ae48478cae515816aa83e8bb52072094d3115881/extensions/beam/src/http-v2.test.ts)

An installed OpenClaw 2026.11.7 Gateway wrote a v1 snapshot before the receiver upgrade. The candidate reopened its state, served a conditional v2 projection with legacy hook and actor metadata removed, and preserved the original row bytes. [Installed upgrade fixture](https://github.com/openclaw/beam/blob/984975ac0fab83d20f9b4b55996dabf104ad86a1/conformance/e2e/legacy_openclaw_test.go)

Use `application/json` and bounded reads. A `publicationId` identifies one incarnation at one receiver. A `revision` is an opaque strong ETag bound to the publication generation, stored write revision, and packet digest. A different generation must reject the old `If-Match` with 412 even if the stored revision and packet bytes match. A `digest` is `sha256:` followed by 64 lowercase hex characters over the exact accepted entity bytes. The client verifies the digest it submitted before offering release. A v1 write to the same live publication invalidates a superseded v2 revision even at an equal timestamp. The receiver must return a stable v2 representation of a legacy row when it advertises that representation’s digest. [Generation-bound ETags](https://github.com/openclaw/openclaw/pull/204582)

Every v2 write carries a client-generated `Idempotency-Key` (UUID). For continue it must equal the body `requestId`. Persist it before the first send attempt. A retry reads its status or replays the identical original request. If a native send may have occurred and no receipt can establish the outcome, return `needs_reconciliation`. Scope its receipt to the receiver, caller, operation, and resource. Reuse with a different request body or precondition yields `409 request_identity_conflict`. For an authorized replay of an already accepted identical request, return the recorded result before evaluating its now-stale precondition. Keep release and destination identity for the binding lifetime. Declare the retention period for send receipts in capabilities. After that period, an unresolved send requires reconciliation. An adapter may advertise durable message retries only when its native send/lookup or host transaction can establish the original outcome. Never retry an uncertain model turn as a fresh request.

| Result                                                           | Meaning and caller action                                                                                            |
| ---------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| `400 invalid_packet` / `413 too_large` / `415 unsupported_media` | Correct the input and preview the resulting bytes.                                                                   |
| `401 authentication_required` / `403 forbidden`                  | Authenticate at the correct origin or request the relevant access. Never treat an auth failure as a legacy receiver. |
| `404 unavailable` / `410 expired`                                | Reveal expiry only to an authorized caller. A cold link or a known ID grants no access.                              |
| `409 released` / `409 needs_reconciliation`                      | Resolve the recorded incarnation. An uncertain or missing destination cannot trigger a replacement session.          |
| `412 revision_changed`                                           | Fetch authorized head metadata. Preview the new content before creating another release intent.                      |
| `429 rate_limited` / `503 temporary` / connection lost           | Respect retry hints. Reuse the frozen identity. Query status before reissuing a possibly completed operation.        |

Errors return `{ok:false,error:{code,retry,message},requestId?}` without transcript excerpts or credentials. `retry` is `never`, `same_request` or `after_reconcile`. To use the v1 wire contract, select the exact legacy endpoint. Authentication failures, transport errors and proxy responses do not trigger an automatic downgrade. Explicit handoff to a known snapshot-only receiver returns unsupported while the source remains writable.

### Journal and failure recovery

Beam v0.1.0 keeps the original packet pending after an HTTP 400, 413, or 415. In Beam v0.2.0, a complete response with one of those statuses on the first v2 publish permits a corrected, newly reviewed packet when no other attempt could have committed. After an incomplete reply or an overlapping retry, rerun `beam share` with the original file to reconcile its bytes, request ID, and precondition. If a build with this change stopped abruptly during its first PUT, use the same or a newer build for the first retry. Beam v0.1.0 can resume once that retry has made the frozen request available to it. [Publish recovery change](https://github.com/openclaw/beam/commit/b0881f50cec699cdee2644187a90a4294cbeea91)

1. Authenticate and discover operations. Inspect existing local intent before starting a new one. Publish the reviewed snapshot conditionally or obtain the identical accepted head. A snapshot write is reversible by updating it while unreleased. It does not retire the sender.
2. Verify the receipt, `canRelease`, and digest. Persist the exact intended target, bytes, publication ID, revision and operation ID before attempting release. Fence concurrent local hooks for this source and endpoint. Persist any pending publication request identity before dispatch too, so a lost publication response is recoverable.
3. Release conditionally. After acknowledgement, retain the receipt and clear pending content. A lost response leaves the source fenced. Query that same receiver and generation or replay the same intent. A definite refusal can end the intent only after establishing that generation is still writable. A failed preflight cannot strand a new source.
4. On first continuation, reserve one destination atomically. Perform async host setup outside SQLite write transactions. Authorize newly started work before it begins and recheck the authoritative destination row at commit. Apply any explicit grant precondition for that effect. A known rollback may retry the same reservation. An uncertain external effect, missing incarnation, or expired unbound snapshot yields reconciliation. Resolve, refresh, and browser reload must be read-only.
5. Keep snapshot retention separate from release/binding metadata. On the inspected OpenClaw baseline this means seven days, 500 shared live rows, and 20 per restricted publisher. Expiry cannot make a released source writable. Deleting a snapshot cannot delete a destination. A later request or independently started job must pass current authorization even when a receipt or retained binding exists.

Ordinary packing and explicit file copies need no Beam database. Use local SQLite for cross-restart intents/receipts and, where needed, bounded staged references. OpenClaw uses its existing shared state store. A third-party receiver can choose another durable implementation. Credential material stays with its auth provider. Existing [sender journal](https://github.com/openclaw/agent-skills/blob/1fa548f4ad814f0f8401623c37c9d4b61e050173/skills/beam/scripts/beam-handoff.js#L7-L48) and [receiver transaction](https://github.com/openclaw/openclaw/blob/b9ef0d7fca28cfea8dec4472d4527d98c40d7ba2/extensions/beam/upload-operation-api.ts#L125-L202) show why a v1 success status alone cannot prove that the selected revision was stored.

### Authentication

Local reads and copies use the operating-system account and native harness. For an HTTPS receiver, choose a named destination with a pinned origin and base path. Support the operator's configured Gateway credential and optional edge auth. For Cloudflare Access, delegate interactive login to `cloudflared`. Automation may supply an origin-scoped Access service credential using the normal secret provider. A service credential authenticates a machine. A verified original person and current grant are required to release a person-owned publication. A shared credential may be authorized to publish a snapshot without qualifying for personal release.

Keep edge and origin credentials separate. Reject credentialed redirects to another origin. Never place tokens in packets, URLs, command results, logs or the handoff journal. The explicit v1 endpoint accepts legacy snapshot and supported handoff writes. The CLI uses negotiated v2 operations for remote pull, destination resolution and continuation. The installed compatibility skill keeps its v1 `read` and `continue` commands for a previously recorded handoff, using the operator’s matching remote profile and the resolved destination. Use the operator’s existing scoped OpenClaw profile when a short human link needs catalog routing. The v2 host must dispatch only an allowlist of operations under the authenticated caller. [Cloudflare CLI](https://developers.cloudflare.com/cloudflare-one/tutorials/cli/) · [OpenClaw authenticated dispatch](https://github.com/openclaw/openclaw/blob/b9ef0d7fca28cfea8dec4472d4527d98c40d7ba2/src/plugin-sdk/gateway-method-runtime.ts#L21-L49)

An offline Linux arm64 test exercised the unmodified `cloudflared` 2026.6.0 binary. The helper made one HTTPS `HEAD` request to the mounted API and used the advertised audience to select a token from an isolated cache. One Beam write reached the loopback receiver with a locally signed RS256 Access token and a separate origin bearer. The receiver verified both credentials. A production tenant and interactive identity-provider login remain untested by this fixture. [Executable fixture](../conformance/e2e/cloudflare_openclaw_test.go) · [Cloudflare binary release](https://github.com/cloudflare/cloudflared/releases/tag/2026.6.0)

The signed-identity and restart tests ran the native Beam CLI against a Gateway installed from combined OpenClaw tree `e84f1a9e`. They used synthetic people, conversations and model responses.

- With separately signed publisher and teammate identities, a test edge rejected missing, expired, incorrectly signed and wrong-audience tokens before forwarding a request. The teammate could pull the reviewed bytes. Supplying the publisher’s name in a header did not authorize the teammate to release the publication. The verified publisher could release it. [Signed identity fixture](https://github.com/openclaw/beam/blob/bc40916d0f11a51f96faa7ea3406b19208303c7a/conformance/e2e/cloudflare_actors_test.go)
- After a committed release lost its acknowledgement, the original publisher retried and a fresh teammate journal pulled the exact packet. Release, pull and resolve made zero model calls. The publisher then repeated one explicit continuation with the same request ID. The loopback model received one request, and the Gateway returned the same destination after a restart. [Multiplayer and restart fixture](https://github.com/openclaw/beam/blob/bc40916d0f11a51f96faa7ea3406b19208303c7a/conformance/e2e/openclaw_test.go)
- A separate installed Gateway run lost the HTTP response after the Gateway recorded the continuation receipt. Retrying and replaying the same request ID recovered that receipt with two status reads. The receiver saw one continuation POST and the loopback provider received one turn. [Lost continuation response fixture](https://github.com/openclaw/beam/blob/695d2ec52937878b9afd433f78b99f13fedf53ce/conformance/e2e/openclaw_test.go)

### Qualification on OpenClaw main

The installation test packed OpenClaw main `7071b82bac28e93782cb56ba4559aaab0bebe049`. The npm archive’s SHA-256 was `c85109b5f45751da3bc0a8cb86ae73322848cf86b339a1dd3319702911dabc7b`. An installed public OpenClaw 2026.11.8 Gateway wrote a v1 snapshot, then the installed main build reopened the same state and served its conditional v2 projection. A separate two-person run used the released Beam CLI to publish and release, restart the Gateway, and recover a lost continuation response. Repeating the same request ID produced one continuation POST and one model turn. [Released v1 upgrade fixture](https://github.com/openclaw/beam/blob/3eee517ed14ca8453d9dfa0f531d30cbfeadcc53/conformance/e2e/legacy_openclaw_test.go) · [Continuation recovery fixture](https://github.com/openclaw/beam/blob/695d2ec52937878b9afd433f78b99f13fedf53ce/conformance/e2e/openclaw_test.go)

A fresh Chromium device opened two private links in the installed Gateway and used the owner-scoped catalog methods to list, read, and continue. The explicit instruction issued one native `chat.send`. A separate authorized operator then disabled Beam with `plugins.setEnabled`. The guest’s existing WebSocket received `routes.changed`, the Control UI cleared the transcript, and a route lookup on that same connection returned `null`. The invitation policy provider was synthetic. The browser, Gateway, bundled Beam plugin, and Control UI were real. The browser run took 23.37 seconds, the two-person wire run 39.30 seconds, and the released-package upgrade 49.06 seconds. [Guest browser configuration](https://github.com/openclaw/openclaw/blob/7071b82bac28e93782cb56ba4559aaab0bebe049/ui/src/e2e/guest-owner-filter.real-gateway.e2e.test.ts#L64-L85)

## OpenClaw implementation

Keep `openclaw/plugin-sdk/*` generic. Product labels, the packet format, public `/beam/` links, source-release rules, and v1/v2 codecs belong in the plugin. Four bounded host changes make the connector possible.

### 1. Authenticated caller for HTTP and RPC

OpenClaw main exposes one frozen view of the current authenticated HTTP or RPC request. A handler calls `getGatewayRequestAuthorityV1()` from `openclaw/plugin-sdk/gateway-request-authority`. It returns `undefined` outside an admitted handler. The SDK change merged in `b23f1ee39229`. [OpenClaw request authority](https://github.com/openclaw/openclaw/pull/204563)

```ts
type GatewayRequestAuthorityV1 = Readonly<{
  scopes: readonly string[];
  clientIp?: string;
  attributionProfileId?: string;
  personProfileId?: string;
  requiredGrantPluginId?: string;
  assertCurrent(requiredGrantPluginId?: string): void;
  revalidate(requiredGrantPluginId?: string): Promise<void>;
}>;
```

- Use `personProfileId` to verify an individual publisher. A Gateway can attach an `attributionProfileId` for display when a shared credential or service authenticated the request. If `personProfileId` is absent, the host has supplied no verified individual. A plugin cannot determine the credential’s kind from that absence.
- Check the admitted scopes and, when the connector requires a particular access policy, the exact `requiredGrantPluginId`. This identifies the policy explicitly required by the admitted role and confirmed by its original grant. Other grants may contribute scopes. Reopening or sending to a Team session still requires ordinary destination access.
- An ordinary v2 HTTP `GET` admits a Visitor once. Keep its original actor, row scope and transport lifetime across asynchronous reads. Use `assertCurrent()` before disclosure. Visitor revocation takes effect on the next request. A queued, retried or independently started send needs current authorization at its own work start. A privileged effect can explicitly bind its original named grant with `revalidate(pluginId)` before the effect and `assertCurrent(pluginId)` at synchronous commit. A new invitation cannot revive that original grant. Both guards expire when the handler settles. A nested registered RPC inherits the exact HTTP caller and original request lifetime. A worker must obtain authority through its host-owned operation boundary.
- Keep profile IDs, client IP and grant information inside the receiving host. The public packet contains only portable history and packet-local speaker aliases.

The Gateway authenticates the presenting credential when it admits an HTTP request. Rotating a paired-device token or changing an iframe cookie’s credential generation takes effect on the next HTTP request. Beam retains the original request lifetime and applies ordinary destination checks. [HTTP admission contract](https://github.com/openclaw/openclaw/pull/203590) · [Request authority implementation](https://github.com/openclaw/openclaw/blob/b23f1ee3922939a62ccac97f2fd4ede84a310493/src/plugins/runtime/gateway-request-authority.ts) · [SDK usage](https://github.com/openclaw/openclaw/blob/b23f1ee3922939a62ccac97f2fd4ede84a310493/docs/plugins/sdk-runtime/gateway-and-nodes.md)

### 2. Caller-scoped catalog route lookup

On OpenClaw main, a catalog provider declares its share URL in `shareRoute` and may register caller-scoped methods in `ownedRoute.methods`. The Gateway checks the registration owner, method declarations, and the admitted caller's grants. A fresh client calls `sessions.catalog.route` with either the URL segment or the known catalog ID. An unavailable route returns `null`. [Gateway and Control UI implementation](https://github.com/openclaw/openclaw/pull/204574)

```ts
type RouteRequest = { routeSegment: string } | { catalogId: string };
type RouteResult = {
  catalogId: string;
  routeSegment: string;
  methods: { list: string; read: string; resolve?: string; continue?: string };
} | null;
```

The Control UI uses the returned methods to list, read, resolve and continue as permitted. Every operation applies its own object-level access checks. The route response contains no session rows or profiles. Existing registered method names such as `beam.owned.read` remain available to older clients. [Protocol schema](https://github.com/openclaw/openclaw/blob/be729d98f68b72fe000d79492e40d850fd02b62e/packages/gateway-protocol/src/schema/sessions-catalog.ts)

For a restricted browser behind OpenClaw’s trusted proxy, set `gateway.auth.trustedProxy.deviceAutoApprove` to `{enabled:true,scopes:["operator.sessions.write"]}`. From OpenClaw main `c755b47b`, a stock Control UI can pair within that explicit cap using the verified user from its trusted ingress. The optional `x-openclaw-scopes` header can narrow access further. Chromium opened only the permitted guest sessions with the header omitted. On earlier v2 builds, have the trusted ingress overwrite that header with `operator.sessions.write` on HTTP requests and the Control UI WebSocket upgrade. The guest role still limits the session and its invitation policy determines which publication the person can access. [Explicit device caps](https://github.com/openclaw/openclaw/pull/204802) · [Earlier trusted-proxy setup](https://github.com/openclaw/openclaw/blob/7071b82bac28e93782cb56ba4559aaab0bebe049/docs/gateway/trusted-proxy-auth.md#control-ui-pairing-behavior)

### 3. Authorized export, locality, and background mirroring

For one interactive export, reuse `chat.history` or the scoped catalog reader with the original caller. Preserve `{catalogId,hostId,threadId,agentId?,sourceHomeId?}` from selection to every page. Project allowed roles and bounded text before passing bytes to the native cleaner. A connector can return “partial history” when a backend provides only part of the thread.

For scheduled export, the service registration declares the operator's catalog allowlist in its configuration. OpenClaw supplies `ctx.catalogExports`. Producers explicitly identify eligible local hosts and rows. The service preserves the full locator across reads and sends the authorized content through the same cleaner used for interactive exports. Imported, federated, and unclassified rows stay out of automatic mirroring. An explicit authorized selection remains available independently. [Service catalog export contract](https://github.com/openclaw/openclaw/pull/204577)

From OpenClaw main `91702a5c`, a service can resume a catalog page on a fresh handle. Pass the provider’s opaque cursor back to its host with `catalog.list({cursors: {[hostId]: nextCursor}})`. Each list checks the current host and rows, retires the previous page’s read grants, and grants reads only for the returned rows. Finish reading the current page before advancing. To select one source, make a separate call with its full locator. [Service catalog paging](https://github.com/openclaw/openclaw/pull/204796)

From OpenClaw main `43ef4fb0`, the Claude Code and Pi catalog providers resolve an exact native session ID independently of sidebar search. Both selected the original session from physical test archives containing 101 newer conversations whose working directories mentioned the same ID. Moving the source file to another storage root still invalidated the binding. [Exact Claude and Pi selection](https://github.com/openclaw/openclaw/pull/204809)

From OpenClaw main `adb00813`, the mirror can skip the native cleaner when a fresh, authorized read yields the same cleaner input as the last acknowledged publication. V2 also rechecks the stored publisher credential, generation, revision, digest and pending or released state after the read. A restart or changed input runs the cleaner again. In a three-poll test on 300 unchanged messages, both builds performed 12 authorized page reads. Cleaner launches fell from three to one. [Mirror change and measurements](https://github.com/openclaw/openclaw/pull/204793)

The OpenClaw mirror checks the receiver’s authenticated v2 capabilities before creating an enrollment. The caller needs both `publish` and `release`. The first write must return `canRelease: true` for that publisher before the source confirms the new publication. The source binds the enrollment to its receiver endpoint, full source locator, and a salted HMAC of the configured Gateway bearer. After rotating that bearer, an operator repeats `beam.mirror.enroll` with the same `source` and the previously confirmed `confirmGeneration`. A read-only `GET` of the receiver head must confirm publisher authority (`canRelease`), the exact generation and the recorded state or pending digest. The source then conditionally updates its enrollment and retains any pending request identity. Separately injected Cloudflare Access credentials rotate at the proxy. The standalone CLI supplies edge and origin credentials per request. [Mirror enrollment](https://github.com/openclaw/openclaw/pull/204586) · [Receiver head](https://github.com/openclaw/openclaw/pull/204575)

From OpenClaw main `2d17819c`, the v2 scheduler rotates through acknowledged enrollments across restarts, up to 32 active and 32 completed sources per poll. It reselects the exact authorized source and reads it before replaying a pending write with its original bytes, request ID and receiver precondition. Older enrollments that contain only a source hash recover their locator by scanning one local host page per poll. A new receiver endpoint requires a new enrollment. The v1 poller keeps its existing newest-32 discovery.

In a Linux test with SQLite, the released v0.1.0 cleaner and 120 synthetic catalog sessions, confirmed sources at ranks 33 and 101 resumed after restart. An unchanged source at rank 101 needed one exact lookup and one protected read. For 32 confirmed sources on one page, one list served all 32 reads. Neither unchanged poll launched a cleaner or sent an HTTP request. A separate lost-response case replayed the retained request after the source had been idle for 181 minutes. [Scheduler and qualification](https://github.com/openclaw/openclaw/pull/204831)

#### Mirror a managed Codex thread between two Gateways

Use compatible OpenClaw builds containing the v2 receiver and mirror changes. The installed proof used candidate npm packages from OpenClaw tree `39f052e3439a`. Those packages reported `2026.11.7`, the same version string as the older public release. Beam v0.1.0 and the OpenClaw distribution have separate releases.

1. On the **receiver**, run `openclaw plugins enable beam` and restart the Gateway. Give the source an HTTPS v2 endpoint and a credential for an identified publisher permitted to publish and release.
2. On the **source**, install the official OpenClaw catalog plugin with `openclaw plugins install @openclaw/codex`, enable the `codex` and `beam` plugins, and restart after configuration. The catalog plugin manages the native `@openai/codex` executable. The Linux x64 proof installed the separately packed candidate plugin with `plugins install npm-pack:<tarball> --force --accept-capabilities`. Its native dependency was Codex 0.169.0.
3. To select the source user's native history, set `plugins.entries.codex.config.appServer.homeScope` to `user` and run the Gateway with the intended `CODEX_HOME`. Configure `plugins.entries.beam.config.mirror` with the receiver's `endpoint`, its Gateway `token`, and `catalogs: ["codex"]`. The proof used an isolated home containing only synthetic turns.
4. From an authorized administrator profile, select the thread using `sessions.catalog.list`. The row must have `exportPolicy.origin: "native-local"` and `exportPolicy.automatic: true`. Supply its returned catalog, host, thread, agent and source-home identifiers to `beam.mirror.enroll`. Keep the publication ID for explicit reauthorization after a Gateway bearer change.

```sh
openclaw gateway call sessions.catalog.list --params '{"catalogId":"codex","agentId":"main"}'
openclaw gateway call beam.mirror.enroll --params '{"source":{"catalogId":"codex","hostId":"<returned-host>","threadId":"<returned-thread>","agentId":"main","sourceHomeId":"<returned-home>"}}'
```

The installed source and receiver kept one publication generation across source disablement, an explicit native turn, credential rotation, reauthorization by the same publisher, and automatic update. A different teammate could not reauthorize it. A third native turn after release and restart left the released revision intact. Imported and proxied rows remained ineligible. The three explicit native turns accounted for all three provider requests. A second installed run used the released Beam v0.1.0 Linux CLI and packaged worker on OpenClaw candidate tree `945e4356936c`. It verified the receiver capability check and first publisher-bound receipt as part of the same end-to-end flow. [Two-Gateway native mirror fixture](https://github.com/openclaw/beam/blob/602ac10dfcd9c0595196f58893f62bba1430a24f/conformance/e2e/codex_mirror_test.go)

### 4. State operation boundary and existing session adoption

The bundled plugin runs typed upload and continuation operations through the Gateway’s plugin state runtime. The continuation worker journals admission before dispatch, binds each receipt to its actor and request, and retains uncertain outcomes for reconciliation. Replays can recover a recorded outcome without scheduling another turn. [Continuation worker](https://github.com/openclaw/openclaw/blob/23e252373e9b44b5f14f476fa52f4d838fd5ec91/extensions/beam/continuation-operation-api.ts)

Reuse `SessionCatalogProvider.copyToGatewaySession` and `resolveGatewayConversation`. They already supply guarded reservation, history read, completion, and rollback. The connector determines eligibility and release state. The Gateway creates and records the destination, applies model and sandbox policy, owns membership/Activity, and authorizes each reopen and send. OpenClaw `main` now returns both the logical `sessionKey` and the verified current `sessionId` after the usual access and provenance checks. OpenClaw main also accepts the optional `expectedSessionId` on `chat.send` and checks it again when admitting a message. A client can bind its send to the conversation it resolved. If the destination changes, the Gateway rejects the send before dispatch. A conditional Stop preserves work in the replacement conversation. Existing callers can omit the field. [Catalog resolution](https://github.com/openclaw/openclaw/pull/204576) · [Conditional send](https://github.com/openclaw/openclaw/pull/204587)

OpenClaw main's plugin SDK helper `sendToResolvedSession({target, message, idempotencyKey})` accepts the guarded catalog result directly. It submits the strict send under the current authenticated Gateway scope. The helper returns `accepted` only for a matching request ID and an admitted native run, `forbidden` for an access refusal, or `unconfirmed` when admission cannot be established. The continuation worker records an uncertain outcome for reconciliation. [Scoped plugin send](https://github.com/openclaw/openclaw/pull/204638) · [Continuation journal](https://github.com/openclaw/openclaw/pull/204581)

Preserve plugin ID `beam` and its `sessions` and `handoffs` namespaces. Use additive rows or value fields where compatible with existing readers. Any necessary SQLite schema-version change requires explicit owner review before implementation.

Relevant owners are `src/plugin-sdk/plugin-state-runtime.ts`, `src/plugins/plugin-state-operation-source.ts`, `src/gateway/server-methods/session-catalog-gateway-copy.ts`, and `extensions/beam/src/{store,session-catalog}.ts`. [Worker state contract](https://github.com/openclaw/openclaw/blob/b9ef0d7fca28cfea8dec4472d4527d98c40d7ba2/docs/plugins/sdk-runtime/state-and-system.md#L481-L523) · [Existing provider hooks](https://github.com/openclaw/openclaw/blob/b9ef0d7fca28cfea8dec4472d4527d98c40d7ba2/src/plugins/session-catalog.ts#L215-L248) · [Host creation policy](https://github.com/openclaw/openclaw/blob/b9ef0d7fca28cfea8dec4472d4527d98c40d7ba2/src/gateway/server-methods/session-catalog-gateway-copy.ts#L461-L499)

## Compatibility and rollout

The original rollout sequence and its compatibility requirements follow. The operator instructions and qualification results above record what has been released and tested.

1. **Freeze existing behavior.** Import licensed synthetic fixtures from agent-skills and OpenClaw. Record existing v1 IDs, timestamps, relative links, config, journal rows, hook arguments, and sanitizer discrepancies. Give each discrepancy an intended v2 result. Run both the released sender and the older joined-test client against the receiver. On the inspected stable tag, staff could create independent copies. The investigated main also implements verified release and one binding. [Stable](https://github.com/openclaw/openclaw/blob/e31bbf056de321127988ec5c1a36ff1b59297de0/docs/plugins/beam.md#L116-L142) · [Newer receiver](https://github.com/openclaw/openclaw/blob/b9ef0d7fca28cfea8dec4472d4527d98c40d7ba2/docs/plugins/beam.md#L119-L142)
2. **Land native file interoperability.** Release schemas, Go cleaner, local pack/inspect/open/push, compatibility skill, and the release-floor adapters above. Qualify the other packaged adapters independently. Preserve the old script's `publish` default, explicit `publish --snapshot`, and passive hooks. The new product guides use `share` and `handoff`. Preserve documented legacy implicit fallback on a definite legacy receipt. Explicit handoff must never downgrade silently.
3. **Land the generic SDK changes and migrate the bundled plugin.** Use a second unrelated fixture plugin to demonstrate the seams. Update SDK export policy/baselines, schema and client bindings, scoped tests, operator docs, plugin inventory/labels when surfaces change, and release notes. Pin the native cleaner and validator to a tested version. Keep old routes live for supported clients.
4. **Add v2 publishing, release and recovery.** Extend receiver revision tracking to writes on both protocol versions. Honor the existing sender's `--state` and `BEAM_STATE` overrides, journal path, exact endpoint plus Beam ID key, frozen v1 payload and original generation. Resume a pending v1 operation through v1. Preserve a retirement indication that already installed legacy hooks can see. Keep legacy manual and mirror identifiers where they already exist. Test concurrent old hooks, new CLI, rollback, and restart. Publish the support matrix and the one-page site from the tested release.
5. **Consider moving the connector source later.** Keeping the small adapter inside OpenClaw avoids a plugin-location migration for this extraction. A later separately distributed connector needs an official package, installed worker module, tested offline enable/upgrade behavior, and a documented migration for enabled and explicitly disabled installations.

## Release acceptance

| Journey             | Required observation                                                                                                                                                                                                                                                                                                                                                 |
| ------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Cross-harness       | Run A → B and B → A with installed harness versions and synthetic content. Select the exact branch, inspect final bytes, open, exit the target, reopen when claimed, send a new instruction, and verify the originating session remains usable. Run one real adapter row per advertised capability.                                                                  |
| Passive behavior    | Count zero model turns, network uploads to a model, or tool calls caused by an open advertised as passive. A first-prompt destination returns `staged` until the user submits.                                                                                                                                                                                       |
| Packet and cleaning | Compare golden bytes across CLI, mirror and hook. Test CRLF, astral/combining text, filenames, secrets in titles, multiline values, duplicate fields, speaker aliases, clipping, empty output, 56 KiB limits and down-projection. Use synthetic fixtures only.                                                                                                       |
| Multiplayer         | Two authorized actors continuing simultaneously get one conversation. Revocation after read admission blocks the next request and separately started work. A privileged effect that explicitly requires the original grant refuses a revoked grant at commit. A different Visitor cannot read the source. Publication ownership cannot bypass destination membership. A shared-token display profile cannot release as a verified person. Historic speakers cannot become current members. |
| SDK independence    | Use a fixture with different plugin ID, catalog ID and route segment. Open a cold private link with a restricted user, check mixed scopes, reload/disable/re-enable, reject another plugin's methods, and preserve ordinary staff operation. Include Session Share and two homes with the same thread ID in mirror tests.                                            |
| Recovery            | Lose the publish, release, and send acknowledgements separately. Restart each side. Race v1/v2 writers, change the head at an equal timestamp, expire before first bind, and remove or reset a destination after binding. Preserve one eligible destination and one confirmed send per request, or return a specific uncertain result.                               |
| Packaging           | Test released archives on supported OS/architectures, normal home paths with spaces, Windows launchers, bundled offline enablement, installed worker writes, schema negotiation, and old hooks across upgrade. Exercise the Cloudflare login shim with an authorized test identity and check service-only behavior separately.                                       |
| Compatibility       | Open old links on the upgraded receiver. Resolve both the v2 machine link and an old human link on a fresh device with no sender journal. Exercise v1 client → new receiver, new client → snapshot-only receiver, mid-release upgrade and rollback. Exact approved v1 bytes remain replayable. Never transform a frozen pending packet during migration.             |

For release diagnosis, record operation, adapter and version, negotiated capabilities, bytes, latency, stable error code, and terminal state. Omit transcript text, filenames, credentials, auth headers, and human identity from default diagnostics. Publish the tested capability matrix with the release. A successful native API call, an in-memory session ID, or a mock-only test cannot establish a durable cross-harness handoff.

## Packet JSON Schema

Publish this structural schema as `schema/packet-v2.schema.json` in the standalone repository. Enforce the earlier wire rules alongside it. JSON Schema counts Unicode code points for `maxLength`, so the wire validator must additionally count UTF-16 units. Enforce the raw UTF-8 byte cap before decoding, reject duplicate keys during decoding, and enable date-time format assertion.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "Beam packet v2",
  "type": "object",
  "additionalProperties": false,
  "required": ["version", "beamId", "source", "title", "updatedAt", "completed", "items"],
  "properties": {
    "version": { "const": 2 },
    "beamId": { "type": "string", "minLength": 32, "maxLength": 32, "pattern": "^[0-9a-f]{32}$" },
    "source": { "type": "string", "minLength": 1, "maxLength": 32, "pattern": "^[a-z0-9._-]+$" },
    "title": { "type": "string", "maxLength": 160, "pattern": "\\S" },
    "updatedAt": { "type": "string", "format": "date-time", "maxLength": 64 },
    "completed": { "type": "boolean" },
    "truncated": { "type": "boolean" },
    "sourceModel": {
      "type": "object",
      "additionalProperties": false,
      "required": ["provider", "model"],
      "properties": {
        "provider": {
          "type": "string",
          "minLength": 1,
          "maxLength": 64,
          "pattern": "^[a-z0-9._-]+$"
        },
        "model": { "type": "string", "minLength": 1, "maxLength": 256, "not": { "pattern": "\\s" } }
      }
    },
    "items": {
      "type": "array",
      "minItems": 1,
      "maxItems": 200,
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["type", "text"],
        "properties": {
          "type": { "enum": ["userMessage", "agentMessage", "other"] },
          "text": { "type": "string", "maxLength": 6000, "pattern": "\\S" },
          "speaker": { "type": "string", "pattern": "^p([1-9]|[1-9][0-9]|1[0-9]{2}|200)$" }
        },
        "dependentSchemas": { "speaker": { "properties": { "type": { "const": "userMessage" } } } }
      }
    }
  }
}
```
