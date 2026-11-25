"""Render the successful adapter test receipts into the static Pages artifact."""
import html
import json
import sys
from pathlib import Path


def render(report):
    names = {
        "capabilities": "Protocol capabilities",
        "selected-items-match": "Selected items matched",
        "ready": "Native session created",
        "manual": "Manual artifact created",
        "same-reference-resolved": "Same reference reopened",
        "reopened-items-match": "Reopened items matched",
    }
    rows = []
    for row in report["adapters"]:
        if row["testVersion"] != 1:
            raise ValueError("unsupported adapter test receipt")
        advertised = row["advertised"]
        cells = [
            row["adapter"], advertised["version"], ", ".join(advertised["destination"]),
            " · ".join(names[check] for check in row["observed"]),
        ]
        target, version, operations, evidence = map(html.escape, cells)
        rows.append(f'<tr><th scope="row">{target}</th><td>{version}</td><td>{operations}</td><td>{evidence}</td></tr>')
    platform = html.escape(report["platform"])
    requests = int(report["modelRequests"])
    return f'''<h3 id="fixture-results-title">Fixture results for this build</h3>
      <p>The {platform} run used a synthetic conversation and recorded {requests} requests at its loopback model. <a href="matrix.json">Download the receipts</a>.</p>
      <div class="fixture-matrix" role="region" aria-labelledby="fixture-results-title" tabindex="0"><table><thead><tr><th scope="col">Adapter</th><th scope="col">Version</th><th scope="col">Advertised operations</th><th scope="col">Observed in this run</th></tr></thead>
      <tbody>{"".join(rows)}</tbody></table></div>'''


if __name__ == "__main__":
    site = Path(sys.argv[1])
    index = site / "index.html"
    source = index.read_text(encoding="utf-8")
    marker = "<!-- adapter fixture results -->"
    if source.count(marker) != 1:
        raise SystemExit("expected one adapter fixture results marker")
    result = render(json.loads((site / "matrix.json").read_text(encoding="utf-8")))
    acpx = json.loads((site / "acpx.json").read_text(encoding="utf-8"))
    count = len(acpx["profiles"])
    version = html.escape(acpx["version"])
    profile_result = f'<h3>acpx profile checks</h3><p>acpx {version} recorded a synthetic conversation for each of its {count} profiles using an offline ACP peer. Beam selected and cleaned each conversation, staged a reference, and reopened it. A separate run sent a prompt through acpx to OpenCode 1.19.6 with a loopback model. OpenClaw 2026.11.10 stored both reviewed turns from that session. <a href="acpx.json">Download the profile results</a>.</p>'
    profile_marker = "<!-- acpx profile results -->"
    if source.count(profile_marker) != 1:
        raise SystemExit("expected one acpx results marker")
    index.write_text(source.replace(marker, result).replace(profile_marker, profile_result), encoding="utf-8")
