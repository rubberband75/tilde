#!/usr/bin/env bash
# Copies the Mint-Y icons Tilde uses into web/public/icons/Mint-Y and writes
# web/public/icons/index.json (icon name -> context).
#
# Source, in order: $MINT_Y_DIR, /usr/share/icons/Mint-Y (installed on Linux
# Mint), or a sparse clone of https://github.com/linuxmint/mint-y-icons.
#
# The Mint-Y theme is licensed CC BY-SA 4.0 (some icons GPLv3); its credits
# and license notice are copied next to the icons. Keep them when bundling.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEST="$ROOT/web/public/icons/Mint-Y"
INDEX="$ROOT/web/public/icons/index.json"
LIST="$ROOT/web/icons.txt"
# 2x displays use the next size up (16->32, 24->48, 32->64), and 48@2x for 48.
SIZES="16 24 32 48 64 48@2x"

src="${MINT_Y_DIR:-}"
tmp=""
# An "if", not "&&": as the last command, a false test would fail the script.
cleanup() { if [ -n "$tmp" ]; then rm -rf "$tmp"; fi; }
trap cleanup EXIT

if [ -z "$src" ] && [ -d /usr/share/icons/Mint-Y ]; then
  src=/usr/share/icons/Mint-Y
fi
if [ -z "$src" ]; then
  tmp="$(mktemp -d)"
  echo "Cloning linuxmint/mint-y-icons (sparse)..."
  git clone -q --depth 1 --filter=blob:none --sparse https://github.com/linuxmint/mint-y-icons "$tmp/repo"
  (cd "$tmp/repo" && git sparse-checkout set usr/share/icons/Mint-Y debian)
  src="$tmp/repo/usr/share/icons/Mint-Y"
  cp "$tmp/repo/README.md" "$tmp/README.md" 2>/dev/null || true
  cp "$tmp/repo/debian/copyright" "$tmp/copyright" 2>/dev/null || true
fi
echo "Using icons from $src"

# Keep upstream credits from an earlier run: an installed theme has no
# README, only a clone does.
keep="$(mktemp -d)"
cp "$DEST"/*.upstream "$keep"/ 2>/dev/null || true
rm -rf "$DEST"
mkdir -p "$DEST"
cp "$keep"/* "$DEST"/ 2>/dev/null || true
rm -rf "$keep"

# Names to copy: everything listed in web/icons.txt, plus every mimetype icon.
mapfile -t wanted < <(grep -v '^\s*#' "$LIST" | sed '/^\s*$/d')
mapfile -t mimes < <(find -L "$src/mimetypes/48" -maxdepth 1 -name '*.png' -printf '%f\n' 2>/dev/null | sed 's/\.png$//' | sort -u)

declare -A ctx_of=()
copy_icon() {
  local name="$1" found=""
  for ctx in places devices apps mimetypes categories actions status legacy; do
    for size in $SIZES; do
      local f="$src/$ctx/$size/$name.png"
      if [ -e "$f" ]; then
        mkdir -p "$DEST/$ctx/$size"
        cp -L "$f" "$DEST/$ctx/$size/$name.png"
        found="$ctx"
      fi
    done
    if [ -n "$found" ]; then
      ctx_of["$name"]="$found"
      return 0
    fi
  done
  echo "  not found: $name" >&2
}

for n in "${wanted[@]}" "${mimes[@]}"; do
  [ -n "${ctx_of[$n]:-}" ] || copy_icon "$n"
done

# Credits and license, as the theme requires.
{
  echo "Mint-Y icon theme, from https://github.com/linuxmint/mint-y-icons"
  echo "License: Creative Commons Attribution-ShareAlike 4.0; some icons GPLv3."
  echo "See the upstream README and debian/copyright for full credits."
} > "$DEST/LICENSE.txt"
for f in "${tmp:+$tmp/README.md}" "${tmp:+$tmp/copyright}" /usr/share/doc/mint-y-icons/copyright; do
  [ -n "$f" ] && [ -f "$f" ] && cp "$f" "$DEST/$(basename "$f").upstream" || true
done

# index.json: {"theme":"Mint-Y","sizes":[...],"icons":{"name":"context",...}}
{
  printf '{"theme":"Mint-Y","sizes":['
  first=1
  for s in $SIZES; do
    case "$s" in *@2x) continue ;; esac
    [ $first -eq 1 ] && first=0 || printf ','
    printf '%s' "$s"
  done
  printf '],"icons":{'
  first=1
  for n in $(printf '%s\n' "${!ctx_of[@]}" | sort); do
    [ $first -eq 1 ] && first=0 || printf ','
    printf '"%s":"%s"' "$n" "${ctx_of[$n]}"
  done
  printf '}}\n'
} > "$INDEX"

echo "Copied ${#ctx_of[@]} icons to $DEST ($(du -sh "$DEST" | cut -f1))"
