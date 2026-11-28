import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { parseBeamPacket, BEAM_PACKET_ABI } from "./packet-v3.ts";
import { parseBeamPacket as parseV2 } from "./packet-v2.ts";

const fixture = JSON.parse(readFileSync(new URL("../conformance/packet/example.beam.json", import.meta.url)));
const packet = () => ({ ...fixture, version: 3, goal: { objective: "Finish the reviewed repair", status: "paused", tokensUsed: 4000, tokenBudget: 12000 } });
const parse = value => parseBeamPacket(Buffer.from(JSON.stringify(value)));

test("v3 goal round trip preserves bounded source state", () => {
  assert.equal(BEAM_PACKET_ABI, 3);
  for (const status of ["active", "paused", "blocked", "usageLimited", "budgetLimited", "complete"]) {
    const p = packet();
    p.goal.status = status;
    assert.deepEqual(parse(p), p);
  }
  const p = packet();
  p.goal.objective = "🦊".repeat(4000);
  p.goal.tokensUsed = Number.MAX_SAFE_INTEGER;
  delete p.goal.tokenBudget;
  assert.deepEqual(parse(p), p);
  assert.throws(() => parseV2(Buffer.from(JSON.stringify(p))));
  delete p.goal;
  assert.deepEqual(parse(p), p);
});

test("v3 rejects invalid or ambiguous goal state", () => {
  for (const patch of [
    { objective: " " }, { objective: "🦊".repeat(4001) },
    { status: "running" }, { tokensUsed: -1 }, { tokensUsed: 1.5 },
    { tokensUsed: Number.MAX_SAFE_INTEGER + 1 }, { tokensUsed: null },
    { tokenBudget: 0 }, { tokenBudget: Number.MAX_SAFE_INTEGER + 1 },
    { id: "native-goal" },
  ]) {
    const p = packet();
    Object.assign(p.goal, patch);
    assert.throws(() => parse(p));
  }
  const p = packet();
  delete p.goal.tokensUsed;
  assert.throws(() => parse(p));
  const raw = JSON.stringify(packet()).replace('"tokensUsed":4000', '"tokensUsed":4000,"tokensUsed":4000');
  assert.throws(() => parseBeamPacket(Buffer.from(raw)));
  const old = packet();
  old.version = 2;
  assert.throws(() => parse(old));
  assert.throws(() => parseV2(Buffer.from(JSON.stringify(old))));
});
