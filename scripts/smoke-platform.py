#!/usr/bin/env python3
"""Verify a native release and its npm wrapper on the target operating system."""

import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import tarfile
import tempfile
import zipfile


def smoke(assets, version, go_os, go_arch):
    platform = {"darwin": "darwin", "linux": "linux", "windows": "win32"}[go_os]
    arch = {"amd64": "x64", "arm64": "arm64"}[go_arch]
    extension, executable = ("zip", "beam.exe") if go_os == "windows" else ("tar.gz", "beam")
    native = assets / f"beam_{version}_{go_os}_{go_arch}.{extension}"
    wrapper = assets / f"beam-npm-{platform}-{arch}-{version}.tgz"
    sums = {name: checksum for checksum, name in (
        line.split(maxsplit=1) for line in (assets / "SHA256SUMS").read_text().splitlines()
    )}
    for artifact in (native, wrapper):
        with artifact.open("rb") as data:
            if hashlib.file_digest(data, "sha256").hexdigest() != sums[artifact.name]:
                raise ValueError(f"checksum mismatch for {artifact.name}")
    if go_os == "windows":
        with zipfile.ZipFile(native) as archive:
            binary = archive.read(executable)
    else:
        with tarfile.open(native, "r:gz") as archive:
            binary = archive.extractfile(executable).read()
    with tarfile.open(wrapper, "r:gz") as archive:
        manifest = json.load(archive.extractfile("package/package.json"))
        expected = {"name": f"@openclaw/beam-{platform}-{arch}", "version": version,
                    "os": [platform], "cpu": [arch], "bin": {"beam": "bin/" + executable}}
        if any(manifest.get(key) != value for key, value in expected.items()):
            raise ValueError(f"incorrect npm metadata for {wrapper.name}")
        member = archive.getmember("package/bin/" + executable)
        if not member.isfile() or member.mode != 0o755 or archive.extractfile(member).read() != binary:
            raise ValueError(f"incorrect npm executable in {wrapper.name}")
    fixtures = Path(__file__).resolve().parents[1] / "conformance" / "packet"
    with tempfile.TemporaryDirectory(prefix="Beam release smoke ") as directory:
        cli = Path(directory) / executable
        cli.write_bytes(binary)
        cli.chmod(0o755)
        def run(*args, data=None):
            return subprocess.run([str(cli), *args], cwd=directory, input=data,
                                  capture_output=True, check=True, timeout=40).stdout
        if run("--version").decode().strip() != version:
            raise ValueError("wrong version in release binary")
        run("inspect", str(fixtures / "example.beam.json"))
        json.loads(run("doctor", "--json"))
        packet = run("clean", "--protocol", "1", data=(fixtures / "read.json").read_bytes())
        if packet != (fixtures / "example.beam.json").read_bytes():
            raise ValueError("packaged cleaner disagrees with the golden packet")
    print(f"verified {native.name} and {wrapper.name}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("assets", type=Path)
    parser.add_argument("version")
    parser.add_argument("go_os")
    parser.add_argument("go_arch")
    args = parser.parse_args()
    smoke(args.assets, args.version, args.go_os, args.go_arch)
