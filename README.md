<h1><img src="site/beam-cover.webp" alt="Beam" width="100%"></h1>

Beam is a local CLI for moving a conversation between coding agents. Pick a session, inspect its cleaned history, and open it in another agent. The destination uses its own workspace, tools, and credentials. Opening a copy leaves the next model turn up to you.

[Website](https://openclaw.github.io/beam/) · [Downloads](https://github.com/openclaw/beam/releases) · [Harness support](https://openclaw.github.io/beam/#support) · [Packet format](schema/packet-v2.schema.json)

## Install

[Download Beam v0.1.0](https://github.com/openclaw/beam/releases/tag/v0.1.0) for macOS, Linux, or Windows. Extract `beam` (`beam.exe` on Windows) to a directory on your `PATH`. Run `beam doctor --json` to see which agents are available locally.

<details>
<summary>Install with Go or verify a download</summary>

Use Go 1.25 or newer.

```sh
go install -ldflags="-X main.version=0.1.0" github.com/openclaw/beam/cmd/beam@v0.1.0
beam --version
```

To verify an archive, replace `ARCHIVE` with its filename and run `gh attestation verify ARCHIVE --repo openclaw/beam`.

</details>

## Try a handoff

In the source project, run `beam list --from codex`. Pick a session and replace `SESSION_ID` with its ID.

```sh
beam pack --from codex --session SESSION_ID --out task.beam.json
beam inspect task.beam.json
beam open task.beam.json --in opencode
```

`pack` writes the selected messages to a local file. `inspect` prints the packet and its SHA-256 digest. Read the messages before you run `open` to create the OpenCode session. Send a new instruction when you want a reply.

Or try Beam with just a POSIX shell. Pack a line of text and render it as Markdown.

```sh
printf '%s\n' 'Keep quoted newlines in the CSV parser.' | beam pack --from file --session - --out sample.beam.json
beam render sample.beam.json
```

## What arrives

| Destination | Result |
| --- | --- |
| Codex or OpenCode | A new native session containing the reviewed history as a reference. |
| Claude Code or Pi | A staged reference for the next explicit instruction. |
| Gemini CLI | A session file to import in Gemini. Gemini handles login. |
| acpx 0.22.0 | A staged reference to continue manually. |
| Local file | A Markdown reference to attach in another tool. |
| OpenClaw | An authenticated Gateway can receive a shared packet and export a selected session. |

Beam cleans visible messages locally and can redact common secrets, email addresses, and local paths. Read the packet before sharing it. The [support matrix](https://openclaw.github.io/beam/#support) lists tested versions and what each destination can reopen.

## Share with OpenClaw

Enable the Beam plugin on the receiving Gateway and restart it.

```sh
openclaw plugins enable beam
openclaw gateway restart
```

For a v2 receiver, use your Gateway URL and supply its bearer through `BEAM_AUTH_TOKEN`.

```sh
beam share task.beam.json --to https://gateway.example
```

Behind Cloudflare Access, add `--cloudflare-login` for interactive login through `cloudflared`. For automation, use `BEAM_ACCESS_TOKEN` or the pair `CF_ACCESS_CLIENT_ID` and `CF_ACCESS_CLIENT_SECRET`. The Gateway enforces access for each caller. See [OpenClaw setup](https://docs.openclaw.ai/plugins/beam) for profiles and the [v1 conversion instructions](docs/standalone-beam.md#operator-experience).

## On the main branch

Build from `main` to use `beam review`, `beam plan`, and `beam adapter test`. These commands are newer than v0.1.0.

```sh
go install github.com/openclaw/beam/cmd/beam@main
beam review task.beam.json
beam plan task.beam.json --in opencode
beam adapter test --in opencode --json
```

`review` numbers the messages. To write an edited packet, pass `--keep N` or `--omit N`, the digest from review with `--expect-digest`, and a new `--out` file. `plan` shows how the destination will open that file.

## Build an integration

- Give an agent the [Beam skill](integrations/skill/SKILL.md) to select, review, and transfer a session.
- Connect an executable with `--adapter name=/path/to/executable`. Use the [stdio contract](docs/standalone-beam.md#cli-and-third-party-adapter-contract), [schemas](schema), and [conformance fixtures](conformance/packet) to test it.
- For cleaning alone, run `beam-clean clean --protocol 1`. It reads a [candidate](schema/packet-candidate-v1.schema.json) from stdin, writes the packet to stdout, and reports omissions on stderr. Release archives include [redistribution notices](CLEANER-NOTICE.txt).

[MIT license](LICENSE) · [Original OpenClaw sender](https://github.com/openclaw/agent-skills/tree/v0.7.0/skills/beam)
