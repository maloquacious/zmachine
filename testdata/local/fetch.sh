#!/bin/sh
# Fetch the Lost Treasures of Infocom story files into this directory.
#
# These are commercial story files and are not redistributable, so they are
# gitignored and must never be committed. See README.md for what they are and
# why they are kept out of the tree.
#
# Usage:  sh testdata/local/fetch.sh
#
# The script is idempotent: it does nothing if the story files are already here.

set -eu

dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
iso="$dir/.LostTreasures1.iso"
url="https://archive.org/download/lost-treasures-of-infocom/LostTreasures1.iso"

# The three files this script produces, named as testdata/stories names its
# own: game, release, serial.
zork1="$dir/zork1-r88-840726.z3"
zork2="$dir/zork2-r48-840904.z3"
zork3="$dir/zork3-r17-840727.z3"

if [ -f "$zork1" ] && [ -f "$zork2" ] && [ -f "$zork3" ]; then
	echo "story files already present; nothing to do"
	exit 0
fi

# libarchive's bsdtar reads ISO 9660 directly and ships with macOS. 7z is the
# usual substitute elsewhere.
if command -v bsdtar >/dev/null 2>&1; then
	extract() { bsdtar -xOf "$1" "$2"; }
elif command -v 7z >/dev/null 2>&1; then
	extract() { 7z x -so "$1" "$2" 2>/dev/null; }
else
	echo "need bsdtar or 7z to read the disc image" >&2
	exit 1
fi

echo "fetching disc image (7.3 MB)..."
curl -fsSL -o "$iso" "$url"

echo "extracting..."
extract "$iso" PC/DATA/ZORK1.DAT >"$zork1"
extract "$iso" PC/DATA/ZORK2.DAT >"$zork2"
extract "$iso" PC/DATA/ZORK3.DAT >"$zork3"

rm -f "$iso"

# Check what came out rather than trusting the disc layout. Byte 0 is the
# Z-machine version, bytes 2-3 the release number, bytes 18-23 the serial
# code; a wrong file here would fail much later and much less clearly.
check() {
	file=$1
	want_version=$2
	want_release=$3
	want_serial=$4

	# od's byte-order flags are not portable, so the release word is assembled
	# from its two bytes rather than read as one.
	version=$(od -An -tu1 -N1 -j0 "$file" | tr -d ' ')
	release=$(od -An -tu1 -N2 -j2 "$file" | tr '\n' ' ' | awk '{print $1 * 256 + $2}')
	serial=$(dd if="$file" bs=1 skip=18 count=6 2>/dev/null)

	if [ "$version" != "$want_version" ] ||
		[ "$release" != "$want_release" ] ||
		[ "$serial" != "$want_serial" ]; then
		echo "$(basename "$file"): got v$version r$release/$serial," \
			"want v$want_version r$want_release/$want_serial" >&2
		exit 1
	fi
	echo "  $(basename "$file"): Version $version, release $release, serial $serial"
}

check "$zork1" 3 88 840726
check "$zork2" 3 48 840904
check "$zork3" 3 17 840727

echo "done"
