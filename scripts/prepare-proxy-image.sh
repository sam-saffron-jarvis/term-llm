#!/usr/bin/env bash
# Prepare, never build/publish, a multi-arch OCI context from a released version.
set -euo pipefail
version=${1:?Usage: prepare-proxy-image.sh vVERSION NEW_CONTEXT_DIRECTORY}
out=${2:?Specify a new context directory}
[[ "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-+.][A-Za-z0-9.-]+)?$ ]] || { echo 'Expected an explicit version tag' >&2; exit 1; }
[[ ! -e "$out" ]] || { echo 'Context already exists; choose a new directory' >&2; exit 1; }
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
umask 077
mkdir -p "$out/artifacts"
version_number=${version#v}
gh release download "$version" --repo term-llm/term-llm --dir "$out/artifacts" \
  --pattern checksums.txt --pattern "term-llm_${version_number}_linux_amd64.tar.gz" --pattern "term-llm_${version_number}_linux_arm64.tar.gz"
for arch in amd64 arm64; do
  archive="term-llm_${version_number}_linux_${arch}.tar.gz"
  (cd "$out/artifacts"; awk -v archive="$archive" '$2 == archive && $1 ~ /^[[:xdigit:]]+$/ && length($1) == 64' checksums.txt > "$arch.check"; sha256sum -c "$arch.check")
  mkdir "$out/linux_$arch"
  tar -xOf "$out/artifacts/$archive" term-llm > "$out/linux_$arch/term-llm"
  chmod 0555 "$out/linux_$arch/term-llm"
  # Reject old dynamic releases rather than quietly creating an unusable image.
  go version -m "$out/linux_$arch/term-llm" | grep -q 'CGO_ENABLED=0' || { echo "Release $version is not a verified static Go build" >&2; exit 1; }
done
cp "$root/examples/proxy/Dockerfile" "$out/Dockerfile"
printf 'Prepared %s; no image was built or published.\n' "$out"
printf 'Inspect and pin the distroless base digest before production.\n'
