# Portable packet conformance

`read.json` is a synthetic, selected, chronological source projection. Run `beam clean --protocol 1 < read.json` from this directory. Its stdout must match `example.beam.json` byte for byte. The example has no terminal newline. `example.sha256` records the file's hexadecimal SHA-256 checksum. Machine receipts and `--expect-digest` use `sha256:` followed by that checksum. Digests cover the actual file including JSON whitespace, ordering and escapes. Preserve the received bytes when replaying a reviewed operation.

`validation.json` applies one replacement per case to the golden bytes. The Go loader and generated TypeScript loader use the same cases. The runtime also enforces the 57,344-byte wire cap, UTF-16 field limits, valid Unicode scalars, duplicate-key rejection, calendar and timezone checks. Only source-ordered visible items belong in a packet. Speaker labels are anonymous packet-local display aliases. They carry no identity or permission at a destination.

`identity.json` freezes the three existing identity namespaces. For v2, hash the UTF-8 bytes of the literal `beam-source-v2` followed by each tuple component's unsigned four-byte big-endian UTF-8 byte length and exact UTF-8 bytes. Use the first 16 bytes of SHA-256 as lowercase hex. Pass the empty string for an absent branch. Preserve case and Unicode normalization. Source home, native ID, branch and original speaker IDs stay local.

For historical uploads, the CLI hash input is `source:identity`. The mirror hash input concatenates catalog, host and thread separated by NUL bytes. Both use the same 16-byte truncation. Adopt an old ID only with a known origin mapping. The v1 upload schema includes hook and handoff controls; importing it as a file discards those controls. An explicit v1 projection drops v2 speaker aliases. Review the new bytes before sending. Pending requests retain their original bytes.

Run the boundary checks from the repository root:

```sh
go test ./internal/packet ./internal/clean
node schema/packet-generate.mjs --check
node --test schema/packet-v2.test.mjs
```

Node 22.18 or later can run the generated TypeScript tests directly. The compiled Go executable handles packing and cleaning without a Node installation.

## Cleaner input

`schema/packet-candidate-v1.schema.json` describes one complete, assembled read result passed to `beam clean --protocol 1`. Bound the UTF-8 JSON input to 4 MiB. Supply one exact origin and branch, oldest-visible item first, and `completeness: "partial"` when the reader hit a gap. A host may supply `sourceModel` as an optional hint. Unknown members, duplicate keys, non-user speaker IDs, and empty post-cleaning history are errors. A host must authorize selection and drop raw tool I/O, hidden reasoning, provider setup, and attachment bytes before invoking the cleaner.

The omission report is a separate stderr JSON value with `redactions`, `omissions`, `lossyConversions`, and `truncated`. The package uses one-based item positions for explicit omission. Literal masking applies to every outbound display field. Automated detectors recognize common credentials, contact data, setup wrappers, local paths and unsafe URL query values; sensitive prose and unfamiliar secret formats may survive. Inspect the final packet or specify an exact literal before release.
