#!/usr/bin/env python3
"""Collect the license files of the modules compiled into the full native CLI."""

import os
from pathlib import Path
import subprocess


def notices():
    modules = {}
    for system in ("darwin", "linux", "windows"):
        for arch in ("amd64", "arm64"):
            rows = subprocess.check_output(
                ["go", "list", "-deps", "-f",
                 "{{if .Module}}{{.Module.Path}}|{{.Module.Version}}|{{.Module.Dir}}{{end}}", "./cmd/beam"],
                env={**os.environ, "GOOS": system, "GOARCH": arch, "CGO_ENABLED": "0"}, text=True,
            )
            for row in rows.splitlines():
                if row:
                    name, version, directory = row.split("|", 2)
                    if version:
                        modules[(name, version)] = Path(directory)
    sections = [Path("CLEANER-NOTICE.txt").read_text().rstrip()]
    for (name, version), directory in sorted(modules.items()):
        licenses = sorted(p for p in directory.glob("LICENSE*") if p.is_file())
        if not licenses:
            raise ValueError(f"release dependency {name} has no known license file")
        for license_file in licenses:
            sections.append(f"{name} {version} — {license_file.name}\n\n" + license_file.read_text().strip())
    return "\n\n".join(sections) + "\n"


if __name__ == "__main__":
    Path("THIRD-PARTY-NOTICES.txt").write_text(notices())
