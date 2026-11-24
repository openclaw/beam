import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { parseBeamPacket, BEAM_PACKET_ABI } from "./packet-v2.ts";

const read = name => readFileSync(new URL("../conformance/packet/" + name, import.meta.url));
const example = read("example.beam.json").toString("utf8");
test("packet validator ABI and exact golden bytes", () => {
  assert.equal(BEAM_PACKET_ABI, 2);
  assert.equal(parseBeamPacket(Buffer.from(example)).source, "codex");
  assert.throws(() => parseBeamPacket(Buffer.concat([Buffer.from(example), Buffer.from([0xff])])));
  assert.throws(() => parseBeamPacket(Buffer.from(" ".repeat(56 * 1024) + example)));
});
for (const v of JSON.parse(read("validation.json"))) {
  test(v.name, () => {
    assert.ok(example.includes(v.from));
    const input = example.replace(v.from, () => v.to);
    if (v.valid) assert.equal(parseBeamPacket(Buffer.from(input)).version, 2);
    else assert.throws(() => parseBeamPacket(Buffer.from(input)));
  });
}
