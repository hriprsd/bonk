#!/usr/bin/env bash
# Upload a darwin/arm64 release tarball to an existing GitHub release (no CI).
# Requires a github.com PAT with repo scope (classic) or contents:write (fine-grained).
#
# Token resolution (first non-empty):
#   $GITHUB_TOKEN, then ~/.my_secrets keys: github_token, gh_token, ghe_token
#
# SSH: used for Git during go build only (private modules). Override with SSH_KEY.

set -euo pipefail

REPO="${GITHUB_REPOSITORY:-hriprsd/bonk}"
OWNER="${REPO%%/*}"
NAME="${REPO##*/}"
TAG="${1:-1.0.0}"
SECRETS="${SECRETS_FILE:-$HOME/.my_secrets}"
SSH_KEY="${SSH_KEY:-$HOME/.ssh/id_ed25519}"

export CGO_ENABLED=0
export GOPRIVATE=github.com/taigrr/apple-silicon-accelerometer
if [[ -f "$SSH_KEY" ]]; then
  export GIT_SSH_COMMAND="ssh -i ${SSH_KEY} -o IdentitiesOnly=yes"
fi

token_from_secrets() {
  [[ -f "$SECRETS" ]] || return 1
  jq -r '(.github_token // .gh_token // .ghe_token) // empty' "$SECRETS"
}

TOKEN="${GITHUB_TOKEN:-}"
if [[ -z "$TOKEN" ]]; then
  TOKEN="$(token_from_secrets 2>/dev/null || true)"
fi
if [[ -z "$TOKEN" || "$TOKEN" == "null" ]]; then
  echo "Set GITHUB_TOKEN or add github_token (recommended for github.com) to $SECRETS" >&2
  exit 1
fi

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
mkdir -p dist
go build -ldflags="-s -w -X main.version=${TAG}" -o dist/bonk .
chmod +x dist/bonk
ARCHIVE="bonk_${TAG}_darwin_arm64.tar.gz"
rm -f "dist/${ARCHIVE}"
( cd dist && tar -czf "${ARCHIVE}" bonk )

REL="$(curl -sS -H "Authorization: Bearer ${TOKEN}" -H "Accept: application/vnd.github+json" \
  "https://api.github.com/repos/${OWNER}/${NAME}/releases/tags/${TAG}")"
MSG="$(echo "$REL" | jq -r '.message // empty')"
if [[ -n "$MSG" ]]; then
  echo "GitHub API: ${MSG}" >&2
  exit 1
fi
UPLOAD_URL="$(echo "$REL" | jq -r '.upload_url // empty' | sed 's/{?name,label}//')"
if [[ -z "$UPLOAD_URL" ]]; then
  echo "No upload_url for tag ${TAG}" >&2
  exit 1
fi

HTTP_CODE="$(curl -sS -o /tmp/bonk_upload_resp.json -w '%{http_code}' -X POST \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Accept: application/vnd.github+json" \
  -H "Content-Type: application/octet-stream" \
  --data-binary "@dist/${ARCHIVE}" \
  "${UPLOAD_URL}?name=${ARCHIVE}")"

if [[ "$HTTP_CODE" != "201" ]]; then
  echo "Upload failed HTTP ${HTTP_CODE}" >&2
  jq '.' /tmp/bonk_upload_resp.json >&2 || cat /tmp/bonk_upload_resp.json >&2
  exit 1
fi

echo "Uploaded dist/${ARCHIVE}"
jq '{name, size, browser_download_url}' /tmp/bonk_upload_resp.json
