#!/usr/bin/env python3
"""Wrap the released executables as optional npm dependencies, without install hooks."""

import argparse
import base64
import gzip
import hashlib
import io
import json
import re
import shutil
import tarfile
import zipfile
from pathlib import Path

PLATFORMS = {"darwin": "darwin", "linux": "linux", "windows": "win32"}
ARCHITECTURES = {"amd64": "x64", "arm64": "arm64"}


def digest(path):
    with path.open("rb") as data:
        return hashlib.file_digest(data, "sha256").hexdigest()


def extract_binary(path, name):
    if path.suffix == ".zip":
        with zipfile.ZipFile(path) as archive:
            return archive.read(name)
    with tarfile.open(path, "r:gz") as archive:
        member = archive.getmember(name)
        if not member.isfile():
            raise ValueError(f"missing regular binary {name} in {path.name}")
        return archive.extractfile(member).read()


def add_member(archive, name, data, mode):
    info = tarfile.TarInfo("package/" + name)
    info.mode = mode
    info.size = len(data)
    archive.addfile(info, io.BytesIO(data))


def wrap(path, version, platform, arch, executable, binary, license_text):
    metadata = {
        "name": f"@openclaw/beam-{platform}-{arch}",
        "version": version,
        "description": "Platform binary for the Beam session portability CLI.",
        "license": "MIT",
        "os": [platform],
        "cpu": [arch],
        "bin": {"beam": "bin/" + executable},
        "files": ["bin", "LICENSE"],
    }
    with path.open("wb") as out, gzip.GzipFile(filename="", fileobj=out, mode="wb", mtime=0) as compressed:
        with tarfile.open(mode="w", fileobj=compressed, format=tarfile.USTAR_FORMAT) as archive:
            add_member(archive, "package.json", (json.dumps(metadata, indent=2) + "\n").encode(), 0o644)
            add_member(archive, "bin/" + executable, binary, 0o755)
            add_member(archive, "LICENSE", license_text, 0o644)


def package(dist, version, destination):
    if not re.fullmatch(r"\d+\.\d+\.\d+(?:-[\w.-]+)?", version):
        raise ValueError("expected a release version, without leading v")
    checksums = {}
    for line in (dist / "SHA256SUMS").read_text().splitlines():
        checksum, name = line.split(maxsplit=1)
        checksums[name] = checksum
    destination.mkdir(parents=True, exist_ok=True)
    files = []
    packages = {}
    license_text = (Path(__file__).resolve().parents[1] / "LICENSE").read_bytes()
    for go_os, platform in PLATFORMS.items():
        for go_arch, arch in ARCHITECTURES.items():
            extension, executable = ("zip", "beam.exe") if go_os == "windows" else ("tar.gz", "beam")
            name = f"beam_{version}_{go_os}_{go_arch}.{extension}"
            original = dist / name
            if digest(original) != checksums.get(name):
                raise ValueError(f"archive checksum mismatch for {name}")
            native = destination / name
            with original.open("rb") as src, native.open("wb") as dst:
                shutil.copyfileobj(src, dst)
            wrapper = destination / f"beam-npm-{platform}-{arch}-{version}.tgz"
            binary = extract_binary(native, executable)
            wrap(wrapper, version, platform, arch, executable, binary, license_text)
            files.extend([native, wrapper])
            with wrapper.open("rb") as data:
                integrity = hashlib.file_digest(data, "sha512").digest()
            packages[f"{platform}-{arch}"] = {
                "package": f"@openclaw/beam-{platform}-{arch}",
                "archive": wrapper.name,
                "sha256": digest(wrapper),
                "integrity": "sha512-" + base64.b64encode(integrity).decode("ascii"),
                "executable": "bin/" + executable,
                "executableSha256": hashlib.sha256(binary).hexdigest(),
            }
    manifest = destination / "platforms.json"
    manifest.write_text(json.dumps({"version": version, "platforms": packages}, indent=2) + "\n")
    files.append(manifest)
    sums = "".join(f"{digest(path)}  {path.name}\n" for path in sorted(files))
    (destination / "SHA256SUMS").write_text(sums)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("dist", type=Path)
    parser.add_argument("version")
    parser.add_argument("destination", type=Path)
    args = parser.parse_args()
    package(args.dist, args.version, args.destination)
