#!/usr/bin/env bash
# Clones the pinned target repo (see TARGET_REPO at the repo root) into a
# gitignored repos/ directory, checked out at the exact pinned commit SHA.
# The SHA is committed to TARGET_REPO (not just this script) so eval labels
# built against a specific commit stay valid even if this script changes.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
target_repo_file="$repo_root/TARGET_REPO"

if [[ ! -f "$target_repo_file" ]]; then
  echo "error: $target_repo_file not found" >&2
  exit 1
fi

url=""
commit=""
while IFS='=' read -r key value; do
  case "$key" in
    url) url="$value" ;;
    commit) commit="$value" ;;
  esac
done < "$target_repo_file"

if [[ -z "$url" || -z "$commit" ]]; then
  echo "error: TARGET_REPO must set both url= and commit=" >&2
  exit 1
fi

dest="$repo_root/repos/fastapi"

if [[ -d "$dest/.git" ]]; then
  echo "fetching updates for existing checkout at $dest"
  git -C "$dest" fetch --quiet origin "$commit"
else
  echo "cloning $url into $dest"
  mkdir -p "$(dirname "$dest")"
  git clone --quiet "$url" "$dest"
fi

git -C "$dest" checkout --quiet --detach "$commit"

actual_commit="$(git -C "$dest" rev-parse HEAD)"
if [[ "$actual_commit" != "$commit" ]]; then
  echo "error: checked out $actual_commit, expected $commit" >&2
  exit 1
fi

echo "checked out fastapi at $commit in $dest"
