#!/usr/bin/env bash
# Detect a new Tencent/MMKV release that is NOT yet covered by our CI matrix and
# open a compatibility-tracking issue (deduplicated). Run daily by
# .github/workflows/mmkv-release-watch.yml.
#
# Requires: git, gh (authenticated via GH_TOKEN with `issues: write`).
# The CI matrix version list in .github/workflows/ci.yml is the single source of
# truth for "what we already test".
set -euo pipefail

UPSTREAM="Tencent/MMKV"
REPO="${GITHUB_REPOSITORY:?set GITHUB_REPOSITORY}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
LABEL="mmkv-release"

# Highest semver tag upstream (robust: doesn't depend on how releases are marked).
latest="$(git ls-remote --tags --refs "https://github.com/$UPSTREAM.git" \
  | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+$' | sort -V | tail -1)"
if [ -z "$latest" ]; then
  echo "could not determine the latest $UPSTREAM tag" >&2
  exit 1
fi
echo "upstream latest tag: $latest"

# Already in our CI matrix?
if grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' "$ROOT/.github/workflows/ci.yml" | sort -u | grep -qx "$latest"; then
  echo "$latest is already in the CI matrix — nothing to do"
  exit 0
fi

# Already tracked by an issue (open or closed)? Exact title match — no --label
# filter, so this works even before the label exists.
title="Track MMKV $latest compatibility"
existing="$(gh issue list --repo "$REPO" --state all --limit 500 --json title --jq '.[].title' 2>/dev/null \
  | grep -cFx "$title" || true)"
if [ "${existing:-0}" != "0" ]; then
  echo "a tracking issue for $latest already exists — nothing to do"
  exit 0
fi

echo "new untracked release $latest — opening tracking issue"
gh label create "$LABEL" --repo "$REPO" --color 1d76db \
  --description "MMKV upstream release compatibility tracking" 2>/dev/null || true

body="$(cat <<EOF
A new upstream release **[$latest](https://github.com/$UPSTREAM/releases/tag/$latest)** is not yet covered by our CI matrix.

Compatibility checklist:
- [ ] Add \`$latest\` to the version list in \`.github/workflows/ci.yml\` (replace the same major-line tag, or add a line).
- [ ] cgo≡purego differential gate passes on \`$latest\` × {amd64, arm64}.
- [ ] On-disk format still supported (no \`ErrUnsupportedVersion\`); purego parses \`$latest\`-written files.
- [ ] Encryption + key-expiration differential still pass (latest line carries the \`mmkvconfig\` build tag in \`run_cell.sh\`).
- [ ] Update the compatibility table in \`README.md\` (+ \`doc/\`).

Notes: https://github.com/$UPSTREAM/releases/tag/$latest
_Opened automatically by the \`mmkv-release-watch\` workflow._
EOF
)"

gh issue create --repo "$REPO" --title "$title" --label "$LABEL" --body "$body"
echo "issue created for $latest"
