#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# A release ships deploy/docker-compose.yml alone, so it carries copies of deploy/db/db-init.sh and
# deploy/db/bootstrap.sql in its `configs` (DB2, docs/adr/0069-database-roles.md; "$" is written "$$" in a
# compose file). This rewrites the copies from the files; DeployDatabaseComposeTest fails when they differ.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
python3 - "$root" <<'PY'
import re
import sys

root = sys.argv[1]
compose_path = f"{root}/deploy/docker-compose.yml"
compose = open(compose_path).read()
for name, source in (("db-init-script", "db-init.sh"), ("bootstrap-sql", "bootstrap.sql")):
    text = open(f"{root}/deploy/db/{source}").read().replace("$", "$$")
    body = "".join(("      " + line if line.strip() else line) for line in text.splitlines(True))
    pattern = re.compile(rf"(  {name}:\n    content: \|\n)(.*?)(?=\n  \S|\n\S)", re.S)
    if not pattern.search(compose):
        sys.exit(f"sync-compose-db: no config {name} in {compose_path}")
    compose = pattern.sub(lambda m: m.group(1) + body.rstrip("\n") + "\n", compose, count=1)
open(compose_path, "w").write(compose)
PY
