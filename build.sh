#!/usr/bin/env bash
# Build bmr-console for both halves of a bare-metal recovery.
#
# One source file, two targets, no cgo: the Windows binary goes into the PE
# image through the Custom folder and the Linux one goes into the ReaR rescue
# system. CGO_ENABLED=0 is not decoration -- a cgo build links against the
# build host's libc, and a rescue image is precisely the environment where
# that libc is not present.
set -eu
cd "$(dirname "$0")"
unset GOROOT 2>/dev/null || true          # see the toolchain note in the book
mkdir -p dist

for target in windows/amd64 linux/amd64; do
    os=${target%/*}; arch=${target#*/}
    out="dist/bmr-console-$os-$arch"
    [ "$os" = windows ] && out="$out.exe"
    CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "-s -w" -o "$out" .
    printf '%-40s %10d bytes\n' "$out" "$(wc -c < "$out")"
done

echo
echo "sha256:"
if command -v sha256sum >/dev/null; then sha256sum dist/bmr-console-*; fi
