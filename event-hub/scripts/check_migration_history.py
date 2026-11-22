"""Reject rewriting/removing baseline migrations or inserting older versions."""
import pathlib
import re
import subprocess
import sys

base = sys.argv[1] if len(sys.argv) == 2 else ""
if not base:
    sys.exit("Pass BASE_REF=<reviewed base commit> to make check-history")
root = pathlib.Path(__file__).resolve().parents[2]
prefix = "event-hub/database/postgres/migrations/"

def git(*args):
    return subprocess.check_output(["git", "-C", str(root), *args])

# Resolve the revision before passing paths, and fail on missing/shallow history.
revision = git("rev-parse", "--verify", base + "^{commit}").decode().strip()
paths = git("ls-tree", "-r", "--name-only", revision, "--", prefix).decode().splitlines()
old = {path: git("show", revision + ":" + path) for path in paths if path.endswith(".sql")}
for path, content in old.items():
    current = root / path
    if not current.is_file() or current.read_bytes() != content:
        sys.exit("Published migration changed or removed: " + path)
highest = max((int(pathlib.PurePosixPath(p).name.split("_", 1)[0]) for p in old), default=0)
seen = set()
for path in sorted((root / prefix).glob("*.sql")):
    match = re.fullmatch(r"([0-9]{6,})_[a-z0-9_]+\.sql", path.name)
    if not match or int(match[1]) <= 0:
        sys.exit("Invalid migration filename: " + path.name)
    number = int(match[1])
    if number in seen:
        sys.exit("Duplicate migration number: " + path.name)
    seen.add(number)
    if path.relative_to(root).as_posix() not in old and number <= highest:
        sys.exit("New migration must follow baseline versions: " + path.name)
print("Migration history preserved")
