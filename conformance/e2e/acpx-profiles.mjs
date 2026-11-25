// Exercise Beam's installed acpx bridge. Only the ACP peer is a fixture.
import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { access, mkdtemp, mkdir, readFile, realpath, rm, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";

const exec = promisify(execFile);
const [reportPath] = process.argv.slice(2);
const beam = process.env.BEAM_E2E_BINARY;
const acpx = process.env.BEAM_TEST_ACPX;
if (!reportPath || !path.isAbsolute(beam ?? "") || !path.isAbsolute(acpx ?? "")) {
  throw new Error("Set absolute BEAM_E2E_BINARY and BEAM_TEST_ACPX paths and provide REPORT.json");
}
const registry = createRequire(await realpath(acpx)).resolve("acpx/agent-registry");
const { listBuiltInAgents } = await import(pathToFileURL(registry));
const profiles = listBuiltInAgents().sort();
const site = await readFile(new URL("../../site/index.html", import.meta.url), "utf8");
const readme = await readFile(new URL("../../README.md", import.meta.url), "utf8");
const table = readme.split("</table>", 1)[0];
const cards = [...site.matchAll(/<li data-profile="([a-z0-9-]+)">([\s\S]*?)<\/li>/g)];
assert.deepEqual(cards.map(card => card[1]).sort(), profiles);
assert.deepEqual([...table.matchAll(/src="site\/marks\/([a-z0-9-]+)\.png"/g)].map(img => img[1]).sort(), profiles);
for (const [, profile, contents] of cards) {
  const mark = `marks/${profile}.png`;
  assert.ok(contents.includes(`src="${mark}"`), `Missing homepage mark for ${profile}`);
  await access(new URL(`../../site/${mark}`, import.meta.url));
}
const peer = fileURLToPath(new URL("../../internal/adapters/acp/testdata/agent.mjs", import.meta.url));
const root = await realpath(await mkdtemp(path.join(tmpdir(), "beam-acpx-")));
const version = (await exec(acpx, ["--version"], { cwd: root, env: { HOME: root, PATH: process.env.PATH } })).stdout.trim();
assert.equal(version, "0.22.0");

async function exercise(profile) {
  const home = path.join(root, profile);
  const cwd = path.join(home, "project");
  await mkdir(cwd, { recursive: true, mode: 0o700 });
  const env = { HOME: home, USERPROFILE: home, PATH: path.dirname(acpx) + path.delimiter + process.env.PATH };
  const run = async (bin, ...args) => (await exec(bin, args, { cwd, env, timeout: 45_000 })).stdout;
  const agent = "acpx:" + profile;
  const agents = { [profile]: { argv: [process.execPath, peer, profile] } };
  if (profile === "claude") agents.cursor = { argv: [process.execPath, peer, "cursor"] };
  const config = JSON.stringify({ agents, ttl: 1, defaultPermissions: "deny-all" });
  await writeFile(path.join(cwd, ".acpxrc.json"), config);
  const { acpxRecordId: id } = JSON.parse(await run(acpx, "--format", "json", profile, "sessions", "ensure", "--name", "csv"));
  assert.ok(id);
  try {
    const prompt = "Check CSV newlines for " + profile + " with person@example.test.";
    await run(acpx, "--format", "quiet", profile, "prompt", "-s", "csv", prompt);
    const trace = path.join(cwd, "fixture-acp.ndjson");
    const before = await readFile(trace, "utf8");
    assert.equal(before.split('\n').filter(line => line && JSON.parse(line).method === "session/prompt").length, 1);
    const { entries } = JSON.parse(await run(beam, "list", "--from", agent));
    assert.deepEqual(entries.map(entry => entry.selection), [id]);
    if (profile === "claude") {
      // Keep the same acpx home. Only the profile or working directory changes.
      const sibling = path.join(home, "second-project");
      await mkdir(sibling, { mode: 0o700 });
      await writeFile(path.join(sibling, ".acpxrc.json"), config);
      for (const [dir, source] of [[cwd, "acpx:cursor"], [sibling, agent]]) {
        const options = { cwd: dir, env, timeout: 45_000 };
        const listed = await exec(beam, ["list", "--from", source], options);
        assert.deepEqual(JSON.parse(listed.stdout).entries, []);
        await assert.rejects(exec(beam, ["pack", "--from", source, "--session", id, "--json"], options),
          error => JSON.parse(error.stdout).error?.code === "unavailable");
      }
    }
    const packet = path.join(cwd, "csv.beam.json");
    await run(beam, "pack", "--from", agent, "--session", id, "--out", packet);
    const raw = await readFile(packet, "utf8");
    const data = JSON.parse(raw);
    assert.equal(data.source, "acpx");
    assert.equal(data.truncated, true);
    assert.deepEqual(data.items.map(({ type, text }) => [type, text]), [
      ["userMessage", prompt.replace("person@example.test", "[REDACTED_EMAIL]")],
      ["agentMessage", "visible synthetic response"],
    ]);
    assert.doesNotMatch(raw, /private-fixture|person@example\.test/);
    const { result, ok } = JSON.parse(await run(beam, "adapter", "test", "--in", agent, "--fixture", packet, "--session", id, "--mode", "stage", "--json"));
    assert.equal(ok, true);
    assert.deepEqual(result.observed, ["capabilities", "selected-items-match", "staged", "same-reference-resolved"]);
    assert.deepEqual(result.advertised.destination, ["stageReference", "resolve"]);
    const after = await readFile(trace, "utf8");
    assert.deepEqual(after.split('\n').filter(line => line && JSON.parse(line).method === "session/prompt"),
      before.split('\n').filter(line => line && JSON.parse(line).method === "session/prompt"));
    console.log(profile + " passed");
    return { profile, observed: result.observed };
  } finally {
    await run(acpx, "--format", "quiet", profile, "sessions", "close", "csv");
  }
}
try {
  // Bound native processes and queue owners on shared CI runners.
  const queue = [...profiles];
  const rows = [];
  const workers = Array.from({ length: 4 }, async () => {
    while (queue.length) rows.push(await exercise(queue.shift()));
  });
  const settled = await Promise.allSettled(workers);
  for (const result of settled) if (result.status === "rejected") throw result.reason;
  rows.sort((a, b) => a.profile.localeCompare(b.profile, "en"));
  await writeFile(reportPath, JSON.stringify({ version, peer: "offline-fixture", profiles: rows }, null, 2) + "\n");
} finally {
  await rm(root, { recursive: true, force: true });
}
