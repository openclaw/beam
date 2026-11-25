# Beam

Bring a coding conversation into **OpenClaw**. Beam reads the session you select, cleans its visible messages locally, and gives you a packet to inspect before you share it. Your team opens the returned link in its own Gateway.

[Website](https://openclaw.github.io/beam/) · [Download Beam](https://github.com/openclaw/beam/releases/tag/v0.1.0) · [Harness test results](https://openclaw.github.io/beam/#support)

## Where your session can come from

<table>
<thead><tr><th scope="col">Read via</th><th colspan="3" scope="colgroup">Harnesses</th></tr></thead>
<tbody>
<tr><th rowspan="2" scope="rowgroup">Direct or acpx</th><td><img src="site/marks/openclaw.png" alt="" width="20" height="20"> OpenClaw</td><td><img src="site/marks/claude.png" alt="" width="20" height="20"> Claude Code</td><td><img src="site/marks/codex.png" alt="" width="20" height="20"> Codex</td></tr>
<tr><td><img src="site/marks/opencode.png" alt="" width="20" height="20"> OpenCode</td><td><img src="site/marks/gemini.png" alt="" width="20" height="20"> Gemini CLI</td><td><img src="site/marks/pi.png" alt="" width="20" height="20"> Pi</td></tr>
</tbody>
<tbody>
<tr><th rowspan="8" scope="rowgroup">acpx</th><td><img src="site/marks/cursor.png" alt="" width="20" height="20"> Cursor</td><td><img src="site/marks/copilot.png" alt="" width="20" height="20"> GitHub Copilot</td><td><img src="site/marks/goose.png" alt="" width="20" height="20"> Goose</td></tr>
<tr><td><img src="site/marks/kiro.png" alt="" width="20" height="20"> Kiro</td><td><img src="site/marks/kimi.png" alt="" width="20" height="20"> Kimi</td><td><img src="site/marks/qwen.png" alt="" width="20" height="20"> Qwen Code</td></tr>
<tr><td><img src="site/marks/antigravity.png" alt="" width="20" height="20"> Antigravity</td><td><img src="site/marks/ante.png" alt="" width="20" height="20"> Ante</td><td><img src="site/marks/deepseek-harness.png" alt="" width="20" height="20"> DeepSeek Harness</td></tr>
<tr><td><img src="site/marks/devin.png" alt="" width="20" height="20"> Devin</td><td><img src="site/marks/droid.png" alt="" width="20" height="20"> Factory Droid</td><td><img src="site/marks/fast-agent.png" alt="" width="20" height="20"> fast-agent</td></tr>
<tr><td><img src="site/marks/fx.png" alt="" width="20" height="20"> fx</td><td><img src="site/marks/grok-build.png" alt="" width="20" height="20"> Grok Build</td><td><img src="site/marks/iflow.png" alt="" width="20" height="20"> iFlow</td></tr>
<tr><td><img src="site/marks/junie.png" alt="" width="20" height="20"> Junie</td><td><img src="site/marks/kilocode.png" alt="" width="20" height="20"> Kilo Code</td><td><img src="site/marks/mcode.png" alt="" width="20" height="20"> MCode</td></tr>
<tr><td><img src="site/marks/mux.png" alt="" width="20" height="20"> Mux</td><td><img src="site/marks/octomind.png" alt="" width="20" height="20"> Octomind</td><td><img src="site/marks/pool.png" alt="" width="20" height="20"> Poolside</td></tr>
<tr><td><img src="site/marks/qoder.png" alt="" width="20" height="20"> Qoder</td><td><img src="site/marks/trae.png" alt="" width="20" height="20"> Trae</td><td><img src="site/marks/zeroclaw.png" alt="" width="20" height="20"> ZeroClaw</td></tr>
</tbody>
</table>

For a session saved by acpx 0.22.0, run `beam list --from acpx:PROFILE` in the project where you used it. The [acpx registry](https://github.com/openclaw/acpx/blob/v0.22.0/docs/agents.md) lists the profile IDs. Beam marks these exports partial because acpx keeps bounded history. You can also pack a local text file or stdin.

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

## Share with OpenClaw

**On the receiving Gateway**, enable Beam and restart OpenClaw.

```sh
openclaw plugins enable beam
openclaw gateway restart
```

**In the source project**, choose an OpenCode session recorded through acpx. Replace `SESSION_ID` with the selection from the list. For a native OpenCode session use `--from opencode` in both commands.

```sh
beam list --from acpx:opencode
beam pack --from acpx:opencode --session SESSION_ID --out task.beam.json
beam convert task.beam.json --packet-version 1 --out task.v1.json
beam inspect task.v1.json
```

[OpenClaw 2026.11.10](https://github.com/openclaw/openclaw/blob/v2026.11.10/extensions/beam/index.ts) accepts v1 packets. Read the converted messages, set `BEAM_AUTH_TOKEN` to your Gateway bearer with publishing access, and replace the host below. Open the `url` in the receipt to read the snapshot. Sharing starts no model turn.

```sh
beam share task.v1.json --to https://gateway.example/api/v1/beam/sessions
```

<details>
<summary>Cloudflare Access and v2 Gateways</summary>

Behind Cloudflare Access, add `--cloudflare-login` for interactive login through `cloudflared`. For automation, use `BEAM_ACCESS_TOKEN` or the pair `CF_ACCESS_CLIENT_ID` and `CF_ACCESS_CLIENT_SECRET` alongside the Gateway credential.

With the [v2 receiver](https://github.com/openclaw/openclaw/commit/7071b82bac28e93782cb56ba4559aaab0bebe049), inspect the original packet and share it to the base URL. The first link opens the snapshot in OpenClaw. A reader with access can use the second link with `beam pull`. See the [receiver guide](docs/standalone-beam.md#operator-experience) for release and explicit continuation.

```sh
beam inspect task.beam.json
beam share task.beam.json --to https://gateway.example
```

</details>

## Images and files

On `main`, bundle screenshots, PDFs, text, audio, or video with a reviewed conversation. Choose each attachment explicitly. Beam preserves its original bytes, including embedded metadata. Check the files as well as the messages before sharing.

```sh
beam bundle create task.beam.json --attach screenshot.png --attach brief.pdf --out task.beam.zip
beam bundle inspect task.beam.zip
beam bundle preview task.beam.zip --out task.html
```

Open `task.html` to see the conversation and its media offline. Send the ZIP to preserve the original files and exact `packet.beam.json`. OpenClaw's v1 and v2 snapshot endpoints accept the text packet. Send the bundle separately when your recipient also needs the attachments. [View the example](https://openclaw.github.io/beam/example.html) · [Bundle format and limits](docs/standalone-beam.md#portable-asset-bundles)

## Other export destinations

| Destination | Result |
| --- | --- |
| <img src="site/marks/codex.png" alt="" width="20" height="20"> Codex or <img src="site/marks/opencode.png" alt="" width="20" height="20"> OpenCode | A new native session containing the reviewed history as a reference. |
| <img src="site/marks/claude.png" alt="" width="20" height="20"> Claude Code or <img src="site/marks/pi.png" alt="" width="20" height="20"> Pi | A staged reference for the next explicit instruction. |
| <img src="site/marks/gemini.png" alt="" width="20" height="20"> Gemini CLI | A session file to import in Gemini. Gemini handles login. |
| acpx 0.22.0 | A staged reference to continue manually. |
| Local file | A Markdown reference to attach in another tool. |

Run `beam open task.beam.json --in opencode` for a native copy, or `beam render task.beam.json` for Markdown. Beam cleans visible messages locally and can redact common secrets, email addresses, and local paths. Read the packet before sharing it. The [support matrix](https://openclaw.github.io/beam/#support) lists tested versions and what each destination can reopen.

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
