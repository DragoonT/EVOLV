#!/usr/bin/env bash
# Verify a packaged EVOLV distribution before it can be published:
#   - exactly the six supported platform binaries are present,
#   - the Linux binary reports the version being released,
#   - checksums are generated and self-consistent.
#
# usage: scripts/verify-artifacts.sh dist 0.33.0
set -euo pipefail

DIST="${1:-dist}"
VERSION="${2:-}"

if [[ -z "$VERSION" ]]; then
  echo "usage: scripts/verify-artifacts.sh <dist-dir> <version>" >&2
  exit 2
fi
if [[ ! -d "$DIST" ]]; then
  echo "verify: missing dist directory: $DIST" >&2
  exit 1
fi

expected=(
  evolv_linux_amd64
  evolv_linux_arm64
  evolv_darwin_amd64
  evolv_darwin_arm64
  evolv_windows_amd64.exe
  evolv_windows_arm64.exe
)

missing=0
for name in "${expected[@]}"; do
  if [[ ! -f "${DIST}/${name}" ]]; then
    echo "verify: missing release artifact: ${DIST}/${name}" >&2
    missing=1
  fi
done
if [[ "$missing" -ne 0 ]]; then
  exit 1
fi

# Refuse unexpected binaries: a stale artifact from a previous matrix must
# never ride along into a release.
shopt -s nullglob
for path in "${DIST}"/evolv_*; do
  base="$(basename "$path")"
  if [[ "$base" == *.spdx.json || "$base" == *.txt ]]; then
    continue
  fi
  found=0
  for name in "${expected[@]}"; do
    if [[ "$base" == "$name" ]]; then
      found=1
      break
    fi
  done
  if [[ "$found" -ne 1 ]]; then
    echo "verify: unexpected artifact in dist: ${base}" >&2
    exit 1
  fi
done

# The version injected at link time must actually be the one reported.
if [[ "$(uname -s)" == "Linux" ]]; then
  chmod +x "${DIST}/evolv_linux_amd64"
  got="$("${DIST}/evolv_linux_amd64" --version)"
  want="evolv ${VERSION}"
  if [[ "$got" != "$want" ]]; then
    echo "verify: version mismatch: got '${got}', want '${want}'" >&2
    exit 1
  fi
fi

if command -v sha256sum >/dev/null 2>&1; then
  SHA=(sha256sum)
else
  SHA=(shasum -a 256)
fi

(
  cd "$DIST"
  "${SHA[@]}" "${expected[@]}" > checksums.txt
  "${SHA[@]}" -c checksums.txt
)

echo "verify: ${#expected[@]} artifacts verified for v${VERSION}"
