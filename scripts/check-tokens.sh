#!/usr/bin/env sh
# Fails if a copy of the design tokens has drifted from the source.
#
# desktop/frontend/src/styles/tokens.css is the only place a token value is
# declared (docs/design/01-tokens.md, section 1). The landing site and the docs
# site each carry a byte-identical copy, made by `make tokens`. This script is
# what makes "byte-identical" true rather than aspirational: run it in CI, and
# when it fails, run `make tokens` and commit the result.
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
src="$root/desktop/frontend/src/styles/tokens.css"

if [ ! -f "$src" ]; then
  echo "check-tokens: missing source $src" >&2
  exit 1
fi

status=0
for copy in "$root/site/tokens.css" "$root/docs/stylesheets/tokens.css"; do
  # A copy that has not been created yet is not drift; only a copy that exists
  # and disagrees is.
  [ -f "$copy" ] || continue
  if cmp -s "$src" "$copy"; then
    echo "ok   ${copy#"$root"/}"
  else
    echo "DRIFT ${copy#"$root"/} differs from ${src#"$root"/}" >&2
    diff -u "$src" "$copy" >&2 || true
    status=1
  fi
done

if [ "$status" -ne 0 ]; then
  echo >&2
  echo "Run 'make tokens' and commit the copies." >&2
fi

exit "$status"
