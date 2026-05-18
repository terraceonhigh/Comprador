#!/bin/bash
# test-md5.sh — phone-side md5 verification of a directory transfer
#
# DEVELOPER-ONLY. Uses adb shell md5sum on the phone (gated by
# COMPRADOR_TESTING_ADB=1 in env, per docs/USER.md's CLAUDE.md rule:
# ADB is never used by the shipping product, only by developer tests).
#
# Compares Mac-side md5sums against phone-side md5sums (the
# phone computes its own hash via Android Shell — bypassing the
# bridge entirely, so a bridge-side bug can't mask itself by
# being self-consistent).
#
# Usage:
#   COMPRADOR_TESTING_ADB=1 ./test-md5.sh <mac_dir> <phone_dir>
#
# Example:
#   COMPRADOR_TESTING_ADB=1 ./test-md5.sh \\
#     ~/Documents/ECON101 \\
#     /storage/emulated/0/Download/ECON101
#
# Output classifies each file as:
#   - byte-perfect match
#   - missing on phone
#   - extra on phone (not in mac source)
#   - hash mismatch (corruption)
# AppleDouble companion files (._*) are excluded — they're filtered
# by the bridge's NFS layer and not expected on the phone.

set -u

if [ -z "${COMPRADOR_TESTING_ADB:-}" ]; then
    cat <<EOF >&2
test-md5.sh requires COMPRADOR_TESTING_ADB=1 in the environment.

This is a deliberate guard: ADB is *out of scope for the shipping
product* (CLAUDE.md "Why not ADB?") because enabling USB Debugging
is irrecoverable UX friction for non-technical users. This script
is a developer-side oracle for transfer-fidelity verification only.

Re-run as:
  COMPRADOR_TESTING_ADB=1 \$0 <mac_dir> <phone_dir>
EOF
    exit 64
fi

if [ $# -ne 2 ]; then
    echo "Usage: COMPRADOR_TESTING_ADB=1 \$0 <mac_dir> <phone_dir>" >&2
    echo "  mac_dir:   absolute path to source directory on the Mac" >&2
    echo "  phone_dir: absolute path on the phone (e.g. /storage/emulated/0/Download/Foo)" >&2
    exit 64
fi

MAC_DIR="$1"
PHONE_DIR="$2"

if ! command -v adb >/dev/null 2>&1; then
    echo "adb not found in PATH" >&2
    exit 69
fi

if [ "$(adb devices 2>/dev/null | grep -c -E "^[A-Za-z0-9]+\\s+device$")" -eq 0 ]; then
    echo "no ADB device connected (run 'adb devices' to confirm)" >&2
    exit 69
fi

if [ ! -d "$MAC_DIR" ]; then
    echo "Mac directory not found: $MAC_DIR" >&2
    exit 66
fi

WORK=$(mktemp -d -t comprador-test-md5)
trap 'rm -rf "$WORK"' EXIT

echo "Computing phone-side md5sums via adb (this may take a while for large trees)..." >&2
adb shell "find '$PHONE_DIR' -type f -not -name '._*' -exec md5sum {} +" \
    | sort -k2 > "$WORK/phone.txt"

echo "Computing Mac-side md5sums..." >&2
( cd "$MAC_DIR" && find . -type f -not -name '._*' | sort | while IFS= read -r f; do
    md5 -r "$f" | awk -v f="${f#./}" '{printf "%s  %s\n", $1, f}'
done ) > "$WORK/mac.txt"

# Strip the phone-root prefix from phone-side paths so the path columns align.
sed -E "s|$PHONE_DIR/||" "$WORK/phone.txt" > "$WORK/phone.normalized"
cp "$WORK/mac.txt" "$WORK/mac.normalized"

python3 - "$WORK/mac.normalized" "$WORK/phone.normalized" "$MAC_DIR" "$PHONE_DIR" <<'PY'
import sys

def load(p):
    d = {}
    with open(p) as f:
        for line in f:
            parts = line.rstrip().split("  ", 1)
            if len(parts) == 2:
                d[parts[1]] = parts[0]
    return d

mac_path, phone_path, mac_root, phone_root = sys.argv[1:5]
mac = load(mac_path)
phone = load(phone_path)

missing = sorted(set(mac) - set(phone))
extra = sorted(set(phone) - set(mac))
common = sorted(set(phone) & set(mac))
mismatched = [k for k in common if phone[k] != mac[k]]

total_mac = sum(1 for _ in mac)
total_phone = sum(1 for _ in phone)
byte_perfect = len(common) - len(mismatched)

print("")
print(f"=== Transfer Verification ===")
print(f"  mac source:   {mac_root}")
print(f"  phone target: {phone_root}")
print(f"")
print(f"  mac files:     {total_mac}")
print(f"  phone files:   {total_phone}")
print(f"  byte-perfect:  {byte_perfect}")
print(f"  mismatched:    {len(mismatched)}")
print(f"  missing on phone:    {len(missing)}")
print(f"  extra on phone:      {len(extra)}")

if missing:
    print(f"")
    print(f"Missing on phone:")
    for k in missing[:30]:
        print(f"  - {k}")
    if len(missing) > 30:
        print(f"  ... and {len(missing) - 30} more")

if extra:
    print(f"")
    print(f"Extra on phone (not in source):")
    for k in extra[:30]:
        print(f"  + {k}")
    if len(extra) > 30:
        print(f"  ... and {len(extra) - 30} more")

if mismatched:
    print(f"")
    print(f"Hash mismatches:")
    for k in mismatched:
        print(f"  ~ {k}")
        print(f"      mac:   {mac[k]}")
        print(f"      phone: {phone[k]}")

# Exit code: 0 only if everything matched and nothing missing.
# Extras are an informational note (e.g. user had pre-existing
# files on the phone) — not a failure.
if missing or mismatched:
    sys.exit(1)
sys.exit(0)
PY
