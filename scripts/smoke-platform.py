#!/usr/bin/env python3
"""Verify a native release and its npm wrapper on the target operating system."""

import argparse
import base64
import gzip
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
    platforms = assets / "platforms.json"
    cleaner_index = assets / "cleaners.json"
    cleaner_archive = assets / f"beam-clean_{version}_{go_os}_{go_arch}.gz"
    for artifact in (native, wrapper, platforms, cleaner_archive, cleaner_index):
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
    index = json.loads(platforms.read_bytes())
    with wrapper.open("rb") as data:
        integrity = base64.b64encode(hashlib.file_digest(data, "sha512").digest()).decode("ascii")
    expected_package = {
        "package": manifest["name"], "archive": wrapper.name, "sha256": sums[wrapper.name],
        "integrity": "sha512-" + integrity, "executable": manifest["bin"]["beam"],
        "executableSha256": hashlib.sha256(binary).hexdigest(),
    }
    if index["version"] != version or index["platforms"][f"{platform}-{arch}"] != expected_package:
        raise ValueError(f"incorrect platform manifest for {wrapper.name}")
    cleaner_data = gzip.decompress(cleaner_archive.read_bytes())
    index = json.loads(cleaner_index.read_bytes())
    expected_cleaner = {
        "archive": cleaner_archive.name, "bytes": cleaner_archive.stat().st_size,
        "sha256": sums[cleaner_archive.name], "executableBytes": len(cleaner_data),
        "executableSha256": hashlib.sha256(cleaner_data).hexdigest(),
    }
    if index["version"] != version or index["protocol"] != 1 or index["platforms"][f"{platform}-{arch}"] != expected_cleaner:
        raise ValueError(f"incorrect cleaner manifest for {cleaner_archive.name}")
    fixtures = Path(__file__).resolve().parents[1] / "conformance" / "packet"
    with tempfile.TemporaryDirectory(prefix="Beam release smoke ") as directory:
        cli = Path(directory) / executable
        cleaner = Path(directory) / ("beam-clean.exe" if go_os == "windows" else "beam-clean")
        cli.write_bytes(binary)
        cli.chmod(0o755)
        cleaner.write_bytes(cleaner_data)
        cleaner.chmod(0o755)
        def run(command, *args, data=None, check=True):
            return subprocess.run([str(command), *args], cwd=directory, input=data,
                                  capture_output=True, check=check, timeout=40)
        for exe in (cli, cleaner):
            if run(exe, "--version").stdout.decode().strip() != version:
                raise ValueError("wrong version in release binary")
        run(cli, "inspect", str(fixtures / "example.beam.json"))
        json.loads(run(cli, "doctor", "--json").stdout)
        good = (fixtures / "read.json").read_bytes()
        for protocol, data in (("1", good), ("1", b"{}"), ("1", b" " * (4 * 1024 * 1024 + 1)), ("2", good)):
            full = run(cli, "clean", "--protocol", protocol, data=data, check=False)
            small = run(cleaner, "clean", "--protocol", protocol, data=data, check=False)
            if (full.returncode, full.stdout, full.stderr) != (small.returncode, small.stdout, small.stderr):
                raise ValueError("packaged cleaners disagree on output, diagnostics or exit status")
            if protocol == "1" and data == good:
                full.check_returncode()
                if full.stdout != (fixtures / "example.beam.json").read_bytes() or "redactions" not in json.loads(full.stderr):
                    raise ValueError("packaged cleaner disagrees with the golden packet or omission report")
            elif full.returncode == 0 or full.stdout:
                raise ValueError("packaged cleaner accepted an invalid request")
    print(f"verified {native.name}, {wrapper.name} and {cleaner_archive.name}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("assets", type=Path)
    parser.add_argument("version")
    parser.add_argument("go_os")
    parser.add_argument("go_arch")
    args = parser.parse_args()
    smoke(args.assets, args.version, args.go_os, args.go_arch)
