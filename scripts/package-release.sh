#!/bin/sh
set -eu

VERSION=${1:-}
PLUGIN_ID="cliproxyapi-copilot"
GOOS=${2:-linux}
GOARCH=${3:-amd64}

case "$VERSION" in
  "" | *[!0-9.]* | .* | *. | *..*)
    printf 'error: version must be dotted numeric without a leading v\n' >&2
    exit 1
    ;;
esac
case "$VERSION" in
  *.*) ;;
  *)
    printf 'error: version must contain at least two numeric components\n' >&2
    exit 1
    ;;
esac

case "$GOOS/$GOARCH" in
  linux/amd64) PLUGIN_EXT="so" ;;
  darwin/arm64) PLUGIN_EXT="dylib" ;;
  *)
    printf 'error: unsupported target: %s/%s\n' "$GOOS" "$GOARCH" >&2
    exit 1
    ;;
esac

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
REPO_DIR=$(CDPATH='' cd -- "$SCRIPT_DIR/.." && pwd)
PLUGIN="$REPO_DIR/build/plugins/$GOOS/$GOARCH/$PLUGIN_ID.$PLUGIN_EXT"
DIST_DIR="$REPO_DIR/dist"
ARCHIVE="$PLUGIN_ID"_"$VERSION"_"$GOOS"_"$GOARCH".zip

[ -f "$PLUGIN" ] || {
  printf 'error: plugin artifact is missing; run make build first\n' >&2
  exit 1
}
command -v python3 >/dev/null 2>&1 || {
  printf 'error: python3 is required\n' >&2
  exit 1
}

mkdir -p "$DIST_DIR"
rm -f "$DIST_DIR/$ARCHIVE" "$DIST_DIR/checksums.txt"
python3 - "$PLUGIN" "$DIST_DIR/$ARCHIVE" "$DIST_DIR/checksums.txt" <<'PY'
import hashlib
import pathlib
import sys
import zipfile

plugin = pathlib.Path(sys.argv[1])
archive = pathlib.Path(sys.argv[2])
checksums = pathlib.Path(sys.argv[3])
with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as output:
    output.write(plugin, plugin.name)
digest = hashlib.sha256()
with archive.open("rb") as package:
    while chunk := package.read(1024 * 1024):
        digest.update(chunk)
checksums.write_text(f"{digest.hexdigest()}  {archive.name}\n", encoding="ascii")
PY

printf 'Created %s and checksums.txt\n' "$DIST_DIR/$ARCHIVE"
