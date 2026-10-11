#!/usr/bin/env bash
# PostToolUse hook for Claude Code and Codex. After the agent edits files it runs go-task on the
# touched packages: formatters and auto-fixes first, then the unit tests of those packages.
# Auto-fixes apply silently and the agent is told which files changed. Problems that remain go
# back to the agent as {"decision":"block"} so it fixes them right away.
#
# Input (stdin): Claude Code sends tool_input.file_path (Edit, Write, MultiEdit); Codex sends the
# patch text in tool_input.command (apply_patch). Needs jq and go-task on PATH.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 0

if ! command -v task >/dev/null 2>&1 || ! command -v jq >/dev/null 2>&1; then
  jq -n '{systemMessage: "after-edit: go-task and jq are needed for the edit checks, skipping them"}' 2>/dev/null \
    || echo "after-edit: go-task and jq are needed for the edit checks, skipping them" >&2
  exit 0
fi

input="$(cat)"
cwd="$(jq -r '.cwd // empty' <<<"$input")"
cwd="${cwd:-$ROOT}"

# Edited paths: Claude Code's file_path, or the file lines of an apply_patch.
mapfile -t edited < <(
  {
    jq -r '.tool_input.file_path // empty' <<<"$input"
    jq -r '.tool_input.command // empty' <<<"$input" | sed -n 's/^\*\*\* \(Add File\|Update File\|Move to\): //p'
  } | sed 's/\r$//'
)

go=false
web=false
locales=false
for path in "${edited[@]}"; do
  [[ -n "$path" ]] || continue
  [[ "$path" == /* ]] || path="$cwd/$path"
  rel="$(realpath -m --relative-to="$ROOT" "$path")"
  case "$rel" in
    ../*) ;; # outside the repository
    core/*.go|admin-api/*.go|core/*/*.go|admin-api/*/*.go|core/*/*/*.go|admin-api/*/*/*.go)
      # The Go modules are checked as a whole: the fixers are quick, and the tests run in CI.
      go=true
      ;;
    web/src/api/*) ;; # generated client, not committed
    web/src/i18n/locales/*.json) web=true; locales=true ;;
    web/.next/* | web/node_modules/*) ;;
    web/*.ts | web/*.tsx | web/*.js | web/*.jsx | web/*.mjs | web/*.json | web/*.css)
      web=true
      ;;
  esac
done

unique() { printf '%s\n' "$@" | sed '/^$/d' | sort -u; }

# sha256 of every modified or untracked file: tells which files the tools changed.
snapshot() {
  git ls-files -m -o --exclude-standard -z | xargs -0 -r sha256sum -- 2>/dev/null
}

failures=""
run() {
  local label="$1"
  shift
  local out
  out="$(mktemp)"
  if ! task --silent "$@" >"$out" 2>&1; then
    failures+="== $label =="$'\n'"$(tail -n 150 "$out")"$'\n\n'
  fi
  rm -f "$out"
}

before="$(snapshot)"

if [[ "$go" == true ]]; then
  run "golangci-lint: formatting and auto-fixes, then the remaining issues" go:fix
fi

if [[ "$web" == true ]]; then
  # The typecheck needs the generated client; it is gitignored and generated from admin-api/schema.
  if [[ ! -d web/src/api ]]; then
    run "orval: generate the API client" web:orval
  fi
  run "oxfmt: formatting" web:format
  run "tsc: typecheck" web:typecheck
  if [[ "$locales" == true ]]; then
    run "locale files: same keys in en and ru" web:i18n
  fi
fi

after="$(snapshot)"
changed="$(diff <(printf '%s\n' "$before") <(printf '%s\n' "$after") | sed -n 's/^> [0-9a-f]\{64\}  //p' | sort -u)"
changed_inline="$(tr '\n' ' ' <<<"$changed" | sed 's/ *$//')"

if [[ -n "$failures" ]]; then
  reason="The edit is applied, but the checks after it found problems that auto-fix could not solve. Fix them now."
  if [[ -n "$changed_inline" ]]; then
    reason+=$'\n'"Auto-fix changed these files, re-read them before editing: $changed_inline"
  fi
  reason+=$'\n\n'"$failures"
  jq -n --arg reason "$reason" '{decision: "block", reason: $reason}'
  exit 0
fi

if [[ -n "$changed_inline" ]]; then
  message="Auto-fix (formatting, lint) changed these files. Re-read them before editing them again: $changed_inline"
  jq -n --arg message "$message" '{hookSpecificOutput: {hookEventName: "PostToolUse", additionalContext: $message}}'
fi
exit 0
