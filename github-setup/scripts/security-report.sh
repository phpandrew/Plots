#!/usr/bin/env bash
# Turns scan results into GitHub issues and decides pass or fail.
#
# Each scanner leaves <name>.status (clean | found | error | skipped) and,
# when it found something, <name>.txt with the findings.
#
# One issue per scanner, labelled "security", matched by a hidden marker in
# its body (so renaming the issue is fine):
#   found  -> open it, or update it (comment only when the findings change)
#   clean  -> close it with a comment
#   error  -> no issue, but the run fails so it isn't missed
# On pull requests no issues are touched; the check just passes or fails.
set -uo pipefail

dir=${1:-scan}
event=${GITHUB_EVENT_NAME:-local}
run_url=${RUN_URL:-}
fail=0

issues_enabled=1
[ "$event" = "pull_request" ] && issues_enabled=0
[ -z "${GH_TOKEN:-}" ] && issues_enabled=0

if [ $issues_enabled = 1 ]; then
  gh label create security --color B60205 --description "Found by the security scans" --force >/dev/null 2>&1 || true
fi

# find_issue <marker>: number of the open issue carrying the marker, if any
find_issue() {
  gh issue list --state open --label security --limit 100 --json number,body \
    --jq ".[] | select(.body | contains(\"<!-- security-scan:$1 \")) | .number" | head -n1
}

report() {
  local key=$1 title=$2 fix=$3
  local status findings hash marker body num old
  status=$(tr -d ' \r\n' 2>/dev/null < "$dir/$key.status" || echo missing)
  echo "== $title: $status"

  case $status in
    clean | skipped) ;;
    found) fail=1 ;;
    *)
      echo "::error::$title did not finish (status: $status). See the job log."
      fail=1
      return
      ;;
  esac
  [ $issues_enabled = 1 ] || return
  [ "$status" = skipped ] && return

  num=$(find_issue "$key")

  if [ "$status" = clean ]; then
    if [ -n "$num" ]; then
      gh issue comment "$num" --body "The latest scan is clean, closing. ([run]($run_url))" >/dev/null
      gh issue close "$num" >/dev/null
      echo "closed #$num"
    fi
    return
  fi

  findings=$(cat "$dir/$key.txt" 2>/dev/null)
  hash=$(printf '%s' "$findings" | sha256sum | cut -c1-12)
  marker="<!-- security-scan:$key hash:$hash -->"
  body=$(cat <<EOF
$marker
**$title** found problems, so the release is held back until a scan is clean.

Last scan: $(date -u '+%Y-%m-%d %H:%M UTC') ([run]($run_url))

### Findings
$( [ "$key" = go ] && printf '```\n%s\n```' "$findings" || printf '%s' "$findings" )

### How to fix
$fix

This issue updates itself on every scan and closes when a scan is clean.
EOF
)

  if [ -z "$num" ]; then
    num=$(gh issue create --title "Security: $title found problems" --label security --body "$body" | grep -oE '[0-9]+$')
    echo "opened #$num"
  else
    old=$(gh issue view "$num" --json body --jq .body | grep -oE "hash:[0-9a-f]+" | head -n1)
    gh issue edit "$num" --body "$body" >/dev/null
    if [ "$old" != "hash:$hash" ]; then
      gh issue comment "$num" --body "The findings changed. The description now shows the latest. ([run]($run_url))" >/dev/null
    fi
    echo "updated #$num"
  fi
}

report go "govulncheck" \
"- **Go module:** \`go get <module>@<fixed version> && go mod tidy\`, then push.
- **Go itself (standard library):** CI always builds with the newest Go 1.26 patch, so re-running usually fixes it. If the fix needs a newer Go, raise the \`go\` line in \`go.mod\`."

report codeql "CodeQL" \
"Open each alert for the exact line and an explanation. Fix it, or if it's a false positive, dismiss it on GitHub (**Security › Code scanning › the alert › Dismiss**) with a reason. Dismissed alerts don't count."

if [ $fail = 1 ]; then
  echo "::error::Security scans found problems or failed. No release."
  exit 1
fi
echo "All scans clean."
