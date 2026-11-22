"""Reverse the reviewed patch in a temporary copy and verify upstream hashes."""
import hashlib
import json
import pathlib
import shutil
import subprocess
import sys
import tempfile

source = pathlib.Path(__file__).resolve().parents[1] / "third_party/goose"
manifest = json.loads((source / "upstream-files.json").read_text())
with tempfile.TemporaryDirectory(prefix="event-hub-goose-") as temp:
    copy = pathlib.Path(temp) / "goose"
    shutil.copytree(source, copy)
    subprocess.run(["git", "apply", "--reverse", str(source / "connection.patch")], cwd=copy, check=True)
    expected = set(manifest)
    actual = {p.relative_to(copy).as_posix() for p in copy.rglob("*.go")} | {"LICENSE", "go.mod"}
    if actual != expected:
        sys.exit("Goose source inventory differs from upstream-files.json")
    for name, digest in manifest.items():
        if hashlib.sha256((copy / name).read_bytes()).hexdigest() != digest:
            sys.exit("Unreviewed Goose change outside connection.patch: " + name)
print("Goose patch matches recorded upstream sources")
