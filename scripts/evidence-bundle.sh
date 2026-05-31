#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

export SCRAP_REPO_ROOT="${SCRAP_REPO_ROOT:-$REPO_ROOT}"
export BUNDLE_DIR="${BUNDLE_DIR:-$REPO_ROOT/evidence}"

if [[ -n "${SCRAPCTL:-}" ]]; then
  exec "$SCRAPCTL" evidence bundle "$@"
fi

exec "${GO:-go}" run "$REPO_ROOT/cmd/scrapctl" evidence bundle "$@"
