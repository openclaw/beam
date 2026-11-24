# Beam

Beam moves reviewed conversation history between coding agents. You choose the source session and the destination. The destination uses its own workspace, credentials, and tool permissions.

This repository contains the standalone Beam implementation under development. The existing OpenClaw sender is available in [agent-skills](https://github.com/openclaw/agent-skills/tree/v0.7.0/skills/beam). Its receiver is documented in the [OpenClaw Beam guide](https://docs.openclaw.ai/plugins/beam).

See the [standalone design proposal](docs/standalone-beam.md) for the packet, adapter, and receiver contracts.

## License

MIT. See [LICENSE](LICENSE).
