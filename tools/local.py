#!/usr/bin/env python3
"""Upgrade bridge for old launchers; new start.sh never calls Python."""
import os
from pathlib import Path
import sys


if __name__ == "__main__":
    start = Path(__file__).resolve().parent.parent / "start.sh"
    os.execv(str(start), [str(start), *sys.argv[1:]])
