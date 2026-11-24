# Beam

Beam moves reviewed conversation history between coding agents. You choose the source session and the destination. The destination uses its own workspace, credentials, and tool permissions.

## Install and transfer

Install from source with Go 1.25 or newer.

```sh
go install github.com/openclaw/beam/cmd/beam@main
beam doctor --json
```

Run the following in the source project. Choose a session ID from the list.

```sh
beam list --from codex
beam pack --from codex --session "$SESSION_ID" --out task.beam.json
beam inspect task.beam.json
beam open task.beam.json --in opencode
```

The last command opens the reviewed file. An explicit new instruction starts the next model turn. Use `beam render task.beam.json` for a Markdown reference you can attach by hand.

## Remote authentication

`beam share task.beam.json --to https://gateway.example` publishes a reviewed packet to an enabled receiver. Supply its bearer credential through `BEAM_AUTH_TOKEN`. When the receiver is behind Cloudflare Access, use one of the following.

- For an interactive session, add `--cloudflare-login` to use the installed `cloudflared` helper.
- For an existing Access token, set `BEAM_ACCESS_TOKEN`.
- For a service token, supply both `CF_ACCESS_CLIENT_ID` and `CF_ACCESS_CLIENT_SECRET`.

The Gateway controls which sessions each caller can see and which operations they can perform. Use a local OpenClaw profile for operations that require a device-bound Gateway identity. See the [OpenClaw guide](https://docs.openclaw.ai/plugins/beam).

## Contracts and compatibility

The source and destination negotiate operations independently. Native destinations can reopen conversations. Staged destinations produce a reference and a next action. See the [harness matrix](https://openclaw.github.io/beam/#support).

The [portable Beam skill](integrations/skill/SKILL.md) gives an agent the same review, transfer, and explicit-send workflow.

Hosts that only need local cleaning can embed the matching `beam-clean` executable with its [redistribution notices](CLEANER-NOTICE.txt). Each release's `cleaners.json` lists the raw gzip, compressed and executable sizes, and both SHA-256 digests for each platform. Invoke `beam-clean clean --protocol 1` with a candidate on stdin. It uses the same implementation as the CLI, writes packet bytes to stdout, and writes the omission counts to stderr.

The [schema directory](schema) defines the portable packet, cleaner input and adapter envelopes. The [conformance vectors](conformance/packet) exercise the shared Go and TypeScript packet boundary. The [design proposal](docs/standalone-beam.md) describes the rollout and receiver contracts. The original OpenClaw sender is available in [agent-skills](https://github.com/openclaw/agent-skills/tree/v0.7.0/skills/beam).

## License

MIT. See [LICENSE](LICENSE).
