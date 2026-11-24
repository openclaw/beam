---
title: Standalone Beam and OpenClaw session portability
summary: "Beam CLI, packet and adapter contracts, receiver protocol, verified harness behavior, and OpenClaw integration targets."
read_when:
  - Implementing or reviewing a Beam reader, destination, or receiver
  - Changing OpenClaw session catalog access, export, or adoption
---

# Standalone Beam and OpenClaw session portability

**Status.** The standalone CLI, cleaner and adapters have executable contracts and conformance tests. The OpenClaw sections describe the receiver and SDK changes required to qualify the full integration. The design baseline is OpenClaw `b9ef0d7fca28` and the released agent-skills sender `1fa548f4ad81`. The [harness matrix](../site/index.html) records tested versions. The [release criteria](#release-acceptance) apply to a packaged distribution.

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

For a v1 receiver, run `beam convert repair.beam.json --packet-version 1 --out repair.v1.json`, then `beam inspect repair.v1.json` and review the projected bytes. The v1 packet omits speaker aliases. Choose the v1 endpoint explicitly when sharing the reviewed projection.

Both ends can be ordinary harnesses. A tool-enabled agent can run `beam pack` or `beam open` directly. A second machine can receive the same reviewed bytes through an existing authenticated transport:

```sh
ssh devbox 'beam open - --in opencode --json' < repair.beam.json
```

The command runs as the selected account on the target machine. It requires Beam and a supported target harness there. An interactive-only target returns a staged reference or a manual outcome. A shared Gateway sends only the history authorized for the requesting person to a child process.

## Repository ownership and distribution

| Repository              | Proposed change                                                                                                                                                                                                                                                                  |
| ----------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `openclaw/beam` (new)   | Own the Go module, native releases, language-neutral schemas, CLI, cleaner, harness adapters, receiver client, conformance fixtures, skill assets, and one-page static site. Publish the executable as `beam`.                                                                   |
| `openclaw/openclaw`     | Retain a small official `beam` plugin in the standard distribution, disabled by default. It owns OpenClaw ingress, receiver policy, catalog projection, and migration. Core owns generic request authority, catalog access and Team sessions.                                    |
| `openclaw/agent-skills` | Keep the installed `skills/beam/scripts/beam` entry point, historical arguments, hook payloads, and legacy exit/receipt behavior. Make the skill delegate to a compatible binary once available. Retire the copied JS implementation after testing upgrades and installed hooks. |

Proposed layout for the new repository:

```text
cmd/beam/                     native CLI and machine entry points
internal/packet/              wire validation, bounded serialization and rendering
internal/clean/               shared export policy and omission report
internal/adapters/            codex, claude, opencode, pi, gemini, acp, file
internal/receiver/            negotiation, HTTP client, auth and receipts
internal/journal/             local pending handoffs and recovery
schema/                       versioned packet, command, adapter and HTTP schemas
conformance/                  synthetic input/output vectors and provider tests
integrations/                 target-native hook/SDK bridges and compatibility skill
site/                         one static page and links to release artifacts
```

Ship signed or verifiably checksummed native archives for macOS, Linux, and Windows on supported architectures. Start from the [Crabbox Go release matrix](https://github.com/openclaw/crabbox/blob/dd0bd80cc3a5b1bd897e749805a9a49a91565e76/.goreleaser.yaml#L18-L35). Publish immutable schemas and a small generated TypeScript validator for the connector. Keep the cleaner in the executable. Package any target-native helper with its adapter. Document which helper uses the target's installed runtime.

For the bundled connector, receive, view, and continue use the installed plugin and its validator. Package the matching native executable as a platform asset for outbound cleaning and mirroring on supported OpenClaw distributions. Resolve and verify it during setup, with an observable capability status. Test enablement offline on the packaged distribution. Source-only or unsupported platforms can still receive and should report the exact missing outbound capability. An official catalog listing alone cannot satisfy one-command enablement because the [enable lifecycle](https://github.com/openclaw/openclaw/blob/b9ef0d7fca28cfea8dec4472d4527d98c40d7ba2/src/plugins/management-mutations.ts#L342-L349) looks up an installed plugin.

The public page needs the install command, two-harness walkthrough, synthetic cleaning example, per-version support matrix, schema, and releases. Host it statically. A running Beam server, browser-side cleaner, and sign-in are unnecessary for this page.

## Portable data and cleaning

Use a closed JSON Schema, Draft 2020-12, with a transport-level byte limit. Preserve v1 on its existing route. The proposed v2 portable packet has this complete shape:

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
- A branch selector is local and opaque. For new sources define one shared identity function and publish byte-level test vectors for both CLI and mirror. The proposed input is the tuple `(adapter, sourceHome, nativeSession, branch)` with a length prefix for each UTF-8 component and a fixed `beam-source-v2` domain prefix. Take the first 16 bytes of SHA-256 as lowercase hex. Prefix lengths are unsigned 32-bit big-endian. Use an empty component for an absent branch. Adapters that cannot establish the same stable origin must publish separate origins. Preserve existing v1 identities and URLs. Adopt an old ID only when its exact origin mapping is established. Never merge on a matching title or visible text.
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

Wrap each machine request with `{protocol:1, request:<AdapterOp>}` and result with either `{protocol:1, ok:true, result:...}` or `{protocol:1, ok:false, error:{code, retry, message}}`. `list` returns `{entries:[{selection,title}],nextCursor?}`. `read` returns `{source,origin,branch?,title,updatedAt,completed,items,nextCursor?,completeness}`. Here `origin` is the private tuple `{adapter,sourceHome,nativeSession}`, `branch` is the optional selected branch, and `completeness` is `complete` or `partial`. Each candidate item is `{type,text,speakerId?}` with the three portable item types. `speakerId` is a source-scoped identifier that the cleaner replaces with a packet-local alias. The adapter must report gaps. The host assembles one branch oldest first, bounds the read, maps aliases, cleans it and excludes private IDs from wire data. Pagination defaults to 50, max 200 items per page. Budget each adapter response at 4 MiB and each exported document at 56 KiB. Cursors are opaque and bound to the selection and read generation. An invalidated cursor restarts an explicit read or yields `source_changed`.

For `beam clean --protocol 1`, the host supplies one assembled `read` result with the cursor omitted and the same origin and branch fields. On success stdout contains only v2 packet bytes. Omission counts go to stderr without source text. An incomplete read sets `truncated`. Use `beam capabilities --json` to negotiate CLI protocol, packet versions, and built-in adapter IDs before invoking it. `doctor` also probes the tools installed on the current machine.

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
| Claude Code | Read the selected project session. | `staged`. A per-invocation SessionStart hook points to the reviewed local artifact. The operator supplies the first instruction. |
| Pi | Select the session and follow the chosen branch. | `staged`. The native extension supplies a reference notice on the first explicit turn. |
| Gemini CLI | Select the project and session. | `staged`. Open a generated session file in the native CLI. Version 0.45.0 assigned a fresh identity before login. Completing a model turn requires the target’s own login. |
| ACP / ACPX | `--from acpx:<agent>` (also `acp:<agent>`) reads selected project history. Stock acpx 0.22.0 does not establish completeness, so Beam marks the packet truncated. | `staged` on 0.22.0. Lookup and resolve recover the private reference after restart. Programmatic send requires an acpx whose prompt command supports `--expect-record` to verify the immutable session record. |
| File | Read a selected Beam packet or UTF-8 text file. | `manual`. Render a Markdown artifact for the recipient to attach. |
| OpenClaw | Read an authorized native session through the selected host or profile. Preserve the backend’s completeness evidence. | An enabled receiver can advertise remote snapshot and handoff operations. Qualifying first continuation, ordinary Team access and reopen requires the combined host and plugin. |

The public CLI round trip used stock Codex 0.172.0, OpenCode 1.19.6, a synthetic conversation and a loopback model. Beam reopened and re-exported each native copy in a fresh process. Those operations made zero model requests. An explicit Codex send then made one request containing both the imported reference and the new instruction. [Executable round trip](../conformance/e2e/native_test.go)

Stock acpx 0.22.0 supports passive selection and staging. For a guarded first send, Beam probes for `acpx <agent> prompt -s <name> --expect-record <record-id> --file -`. When available, the first explicit instruction creates and seeds the native target. A confirmed replay returns its receipt. An ambiguous interruption requires reconciliation. Stock 0.22.0 does not advertise this flag. [Beam ACP adapter](../internal/adapters/acp)

Evidence for these adapter choices:

- Codex separates [thread read](https://github.com/openai/codex/blob/414782450952a196bbb64b1605a458c2531c47b6/codex-rs/app-server/tests/suite/v2/thread_read.rs#L138-L175), [history injection and turn start](https://github.com/openai/codex/blob/414782450952a196bbb64b1605a458c2531c47b6/codex-rs/app-server/tests/suite/v2/thread_inject_items.rs#L42-L99). Claude documents [selected-session APIs](https://code.claude.com/docs/en/agent-sdk/sessions) and [hook context](https://code.claude.com/docs/en/hooks#add-context-for-claude).
- OpenCode v1.19.6 implements the [passive message path](https://github.com/anomalyco/opencode/blob/f4e363c8295fa90a03dddc451882fffe8fbce357/packages/opencode/src/session/prompt.ts#L1045-L1070). Pi documents [branch traversal](https://github.com/earendil-works/pi/blob/f8149d1abde50b31c8cb5aed09e89319b927f247/packages/coding-agent/src/core/session-manager.ts#L1484-L1498) and the [first persistent write](https://github.com/earendil-works/pi/blob/f8149d1abde50b31c8cb5aed09e89319b927f247/packages/coding-agent/src/core/session-manager.ts#L1170-L1199).
- Gemini documents [hook semantics](https://geminicli.com/docs/hooks/reference/#sessionstart) and [native session import](https://github.com/google-gemini/gemini-cli/blob/03862c4164844c6cf8063b4cc48546ec1609cf2e/packages/cli/src/gemini.tsx#L339-L400). ACP distinguishes [session setup](https://agentclientprotocol.com/protocol/v1/session-setup) and [prompt turns](https://agentclientprotocol.com/protocol/v1/prompt-turn). OpenClaw has a bounded [chat history request](https://github.com/openclaw/openclaw/blob/b9ef0d7fca28cfea8dec4472d4527d98c40d7ba2/packages/gateway-protocol/src/schema/logs-chat.ts#L38-L65).

## Receiver HTTP contract

A remote receiver is optional. The client negotiates the v2 operations supported by the selected receiver and caller. The OpenClaw read-side implementation covers capabilities, head and snapshot reads. Write, release and continuation have separate acceptance criteria. The existing v1 write endpoint remains `/api/v1/beam/sessions`. [OpenClaw read-side implementation](https://github.com/openclaw/openclaw/blob/ae48478cae515816aa83e8bb52072094d3115881/extensions/beam/src/http-v2.ts)

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

Use `application/json` and bounded reads. A `publicationId` identifies one incarnation at one receiver. A `revision` is an opaque strong ETag. A `digest` is `sha256:` followed by 64 lowercase hex characters over the exact accepted entity bytes. The client verifies the digest it submitted before offering release. A v1 write to the same live publication invalidates a superseded v2 revision even at an equal timestamp. The receiver must return a stable v2 representation of a legacy row when it advertises that representation’s digest.

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

1. Authenticate and discover operations. Inspect existing local intent before starting a new one. Publish the reviewed snapshot conditionally or obtain the identical accepted head. A snapshot write is reversible by updating it while unreleased. It does not retire the sender.
2. Verify the receipt, `canRelease`, and digest. Persist the exact intended target, bytes, publication ID, revision and operation ID before attempting release. Fence concurrent local hooks for this source and endpoint. Persist any pending publication request identity before dispatch too, so a lost publication response is recoverable.
3. Release conditionally. After acknowledgement, retain the receipt and clear pending content. A lost response leaves the source fenced. Query that same receiver and generation or replay the same intent. A definite refusal can end the intent only after establishing that generation is still writable. A failed preflight cannot strand a new source.
4. On first continuation, reserve one destination atomically. Perform async host setup outside SQLite write transactions. Revalidate the original grant and authoritative row at commit. A known rollback may retry the same reservation. An uncertain external effect, missing incarnation, or expired unbound snapshot yields reconciliation. Resolve, refresh, and browser reload must be read-only.
5. Keep snapshot retention separate from release/binding metadata. On the inspected OpenClaw baseline this means seven days, 500 shared live rows, and 20 per restricted publisher. Expiry cannot make a released source writable. Deleting a snapshot cannot delete a destination. Revocation applies even when a receipt or retained binding exists.

Ordinary packing and explicit file copies need no Beam database. Use local SQLite for cross-restart intents/receipts and, where needed, bounded staged references. OpenClaw uses its existing shared state store. A third-party receiver can choose another durable implementation. Credential material stays with its auth provider. Existing [sender journal](https://github.com/openclaw/agent-skills/blob/1fa548f4ad814f0f8401623c37c9d4b61e050173/skills/beam/scripts/beam-handoff.js#L7-L48) and [receiver transaction](https://github.com/openclaw/openclaw/blob/b9ef0d7fca28cfea8dec4472d4527d98c40d7ba2/extensions/beam/upload-operation-api.ts#L125-L202) show why a v1 success status alone cannot prove that the selected revision was stored.

### Authentication

Local reads and copies use the operating-system account and native harness. For an HTTPS receiver, choose a named destination with a pinned origin and base path. Support the operator's configured Gateway credential and optional edge auth. For Cloudflare Access, delegate interactive login to `cloudflared`. Automation may supply an origin-scoped Access service credential using the normal secret provider. A service credential authenticates a machine. A verified original person and current grant are required to release a person-owned publication. A shared credential may be authorized to publish a snapshot without qualifying for personal release.

Keep edge and origin credentials separate. Reject credentialed redirects to another origin. Never place tokens in packets, URLs, command results, logs or the handoff journal. The explicit v1 endpoint accepts legacy snapshot and supported handoff writes. The CLI uses negotiated v2 operations for remote pull, destination resolution and continuation. The installed compatibility skill keeps its v1 `read` and `continue` commands for a previously recorded handoff, using the operator’s matching remote profile and the resolved destination. Use the operator’s existing scoped OpenClaw profile when a short human link needs catalog routing. The v2 host must dispatch only an allowlist of operations under the authenticated caller. [Cloudflare CLI](https://developers.cloudflare.com/cloudflare-one/tutorials/cli/) · [OpenClaw authenticated dispatch](https://github.com/openclaw/openclaw/blob/b9ef0d7fca28cfea8dec4472d4527d98c40d7ba2/src/plugin-sdk/gateway-method-runtime.ts#L21-L49)

## OpenClaw implementation

Keep `openclaw/plugin-sdk/*` generic. Product labels, the packet format, public `/beam/` links, source-release rules, and v1/v2 codecs belong in the plugin. Four bounded host changes make the connector possible.

### 1. Authenticated caller for HTTP and RPC

Expose one read-only view of the request the host already admitted. An illustrative public shape follows. The host constructs it and preserves the same authority across awaited work.

```ts
type PluginRequestAuthority = {
  actor: { kind: "person"; profileId: string } | { kind: "service" | "shared" };
  scopes: readonly string[];
  grant?: { issuer: string; assertCurrent(): void };
  signal: AbortSignal;
  revalidate(): Promise<void>;
  assertCurrent(): void;
};
```

Define “person” from the authentication provenance. A service or shared-secret caller may have a display profile. That profile alone cannot prove an individual publisher. The policy issuer attests the purpose of its grant. A connector declares which policy admits its restricted view. A generic grant from an unrelated issuer cannot satisfy it. Await `revalidate()` after asynchronous input reads and before disclosure or write preparation. Use synchronous `assertCurrent()` at write admission and inside the commit guard. Revalidate the captured grant itself so revocation followed by reinvitation cannot revive the original request. Return the same abstraction to HTTP and RPC handlers. Session authorization at the destination remains independent.

Change `src/plugins/runtime/gateway-request-scope*`, the public SDK request-context export, and Gateway HTTP/RPC adapters. Migrate `extensions/beam/src/http.ts` and `owned-view.ts` off internal client fields. Verify the [current private-field lookup](https://github.com/openclaw/openclaw/blob/b9ef0d7fca28cfea8dec4472d4527d98c40d7ba2/extensions/beam/src/http.ts#L22-L33) and [shared-auth profile projection](https://github.com/openclaw/openclaw/blob/b9ef0d7fca28cfea8dec4472d4527d98c40d7ba2/src/gateway/http-auth-user-profile.ts#L217-L242).

### 2. Caller-scoped catalog route lookup

Extend `SessionCatalogProvider.shareRoute` with an optional mapping for restricted operations. Add one read-only Gateway query. Proposed types:

```ts
type CatalogOp = "list" | "read" | "resolve" | "continue";
type MethodMap = Partial<Record<CatalogOp, string>>;

type CatalogRouteRegistration = { ownedOperations?: MethodMap };
type RouteRequest = { routeSegment: string };
type RouteResult = { catalogId: string; routeSegment: string; methods: MethodMap };
```

`sessions.catalog.route({routeSegment})` resolves one active route for the current caller. Return only operations that caller can attempt. The caller still needs object-level authorization on each operation. Use the active registration owner, unique route segment, method declaration, and scope to validate the map. Reject reserved/conflicting routes and methods claimed from another plugin. Use the same opaque unavailable result for unregistered and inadmissible routes. The lookup returns no session list, profile IDs, counts, or row ownership information. Admit a restricted caller through their existing scoped access without adding shared `operator.read`. Return a method per operation because read and write permissions can differ. Re-resolve on plugin/connection change.

Update `src/plugins/session-catalog.ts`, `src/plugin-sdk/session-catalog.ts`, `packages/gateway-protocol/src/schema/sessions-catalog.ts`, the Gateway dispatch/method descriptors and catalog registry. Change `ui/src/lib/sessions/catalog-read-method.ts`, `ui/src/pages/chat/route-loader-catalog-share.runtime.ts` and `ui/src/pages/chat/chat-pane-render.ts` to consume that result, including the composer. Keep existing method names such as `beam.owned.read` registered for shipped clients. The [existing registry](https://github.com/openclaw/openclaw/blob/b9ef0d7fca28cfea8dec4472d4527d98c40d7ba2/src/gateway/server-methods/session-catalog-provider-access.ts#L180-L217) already knows route collisions. [Current UI selection](https://github.com/openclaw/openclaw/blob/b9ef0d7fca28cfea8dec4472d4527d98c40d7ba2/ui/src/lib/sessions/catalog-read-method.ts#L4-L20) happens before a cold link can list its catalog.

### 3. Authorized export, locality, and background mirroring

For one interactive export, reuse `chat.history` or the scoped catalog reader with the original caller. Preserve `{catalogId,hostId,threadId,agentId?,sourceHomeId?}` from selection to every page. Project allowed roles and bounded text before passing bytes to the native cleaner. A connector can return “partial history” when a backend provides only part of the thread.

For scheduled export, expose a public service-lifetime read handle bound to the configured catalog allowlist and local source policy. Producers identify `native-local`, `imported`, or `federated` origin and whether automatic export is allowed. Default unknown provenance to ineligible for automatic export. Explicit authorized user selection remains available. Do not let a service impersonate a user. Exclude the mirror's own publications, carry the full locator through deduplication, and apply the shared cleaner to every export path.

Update `src/plugins/session-catalog.ts`, `src/plugin-sdk/session-catalog.ts`, producer catalog projections, and `extensions/beam/src/mirror.ts`. A remote Session Share peer also reports `kind:"gateway"`. Host kind cannot establish locality. Source ownership and export policy need explicit evidence. [Locator](https://github.com/openclaw/openclaw/blob/b9ef0d7fca28cfea8dec4472d4527d98c40d7ba2/packages/gateway-protocol/src/schema/sessions-catalog.ts#L18-L24) · [Mirror selection](https://github.com/openclaw/openclaw/blob/b9ef0d7fca28cfea8dec4472d4527d98c40d7ba2/extensions/beam/src/mirror.ts#L438-L461) · [Federated catalog](https://github.com/openclaw/openclaw/blob/b9ef0d7fca28cfea8dec4472d4527d98c40d7ba2/extensions/session-share/src/gateway-catalog.ts#L197-L214)

### 4. State operation boundary and existing session adoption

Keep the documented keyed-store transaction API. Make the typed worker operation and keyed publication guard available through supported SDK exports where required. The guard ties an effect to one publication generation and the original request. Preserve `extensions/beam/upload-operation-api.ts` as a compiled top-level worker module, or migrate all callers to an equally supported worker entry in the same change. Exercise it from the packaged plugin. A successful route registration alone cannot demonstrate that a state write will resolve its module.

Reuse `SessionCatalogProvider.copyToGatewaySession` and `resolveGatewayConversation`. They already supply guarded reservation, history read, completion, and rollback. The connector determines eligibility and release state. The Gateway creates and records the destination, applies model and sandbox policy, owns membership/Activity, and authorizes each reopen and send. No second generic adoption engine is needed. Augment a return type only if the conformance test demonstrates a specific missing fact. Preserve plugin ID `beam` and its `sessions` and `handoffs` namespaces. Use additive rows or value fields where compatible with existing readers. Any necessary SQLite schema-version change requires explicit owner review before implementation.

Relevant owners are `src/plugin-sdk/plugin-state-runtime.ts`, `src/plugins/plugin-state-operation-source.ts`, `src/gateway/server-methods/session-catalog-gateway-copy.ts`, and `extensions/beam/src/{store,session-catalog}.ts`. [Worker state contract](https://github.com/openclaw/openclaw/blob/b9ef0d7fca28cfea8dec4472d4527d98c40d7ba2/docs/plugins/sdk-runtime/state-and-system.md#L481-L523) · [Existing provider hooks](https://github.com/openclaw/openclaw/blob/b9ef0d7fca28cfea8dec4472d4527d98c40d7ba2/src/plugins/session-catalog.ts#L215-L248) · [Host creation policy](https://github.com/openclaw/openclaw/blob/b9ef0d7fca28cfea8dec4472d4527d98c40d7ba2/src/gateway/server-methods/session-catalog-gateway-copy.ts#L461-L499)

## Compatibility and rollout

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
| Multiplayer         | Two authorized actors continuing simultaneously get one conversation. An invitation revoked while the operation awaits fails at commit. A different Visitor cannot read the source. Publication ownership cannot bypass destination membership. A shared-token display profile cannot release as a verified person. Historic speakers cannot become current members. |
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
