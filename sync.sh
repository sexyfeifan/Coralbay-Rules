#!/bin/sh
set -eu

repository="${RULES_REPOSITORY:-https://github.com/666OS/rules.git}"
branch="${RULES_BRANCH:-release}"
interval="${SYNC_INTERVAL:-21600}"
expected_file="/app/expected-files.txt"

log() {
  printf '%s [sync] %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"
}

validate_release() {
  release_dir="$1"
  count=0
  while IFS= read -r relative_path; do
    [ -n "$relative_path" ] || continue
    if [ ! -s "$release_dir/$relative_path" ]; then
      log "校验失败：缺少或为空 $relative_path" >&2
      return 1
    fi
    count=$((count + 1))
  done < "$expected_file"

  expected_count="$(grep -cve '^[[:space:]]*$' "$expected_file")"
  [ "$count" -eq "$expected_count" ] || return 1
  VALIDATED_COUNT="$count"
}

sync_once() {
  mkdir -p /data/releases
  staging="/data/.staging.$$"
  rm -rf "$staging"
  trap 'rm -rf "$staging"' EXIT INT TERM

  log "开始同步 $repository ($branch)"
  git clone --quiet --depth 1 --single-branch --branch "$branch" "$repository" "$staging"
  commit="$(git -C "$staging" rev-parse HEAD)"
  validate_release "$staging"

  release_dir="/data/releases/$commit"
  if [ ! -d "$release_dir" ]; then
    rm -rf "$staging/.git"
    mkdir -p "$staging/_mirror"
    cat > "$staging/_mirror/status.json" <<EOF
{"ok":true,"repository":"$repository","branch":"$branch","commit":"$commit","synced_at":"$(date -u +%Y-%m-%dT%H:%M:%SZ)","validated_files":$VALIDATED_COUNT}
EOF
    mv "$staging" "$release_dir"
  else
    rm -rf "$staging"
  fi

  ln -sfn "releases/$commit" /data/current

  # 保留当前版本以及最近两个历史版本。
  ls -1dt /data/releases/* 2>/dev/null | awk 'NR > 3' | while IFS= read -r old_release; do
    [ "$old_release" = "$release_dir" ] || rm -rf "$old_release"
  done

  trap - EXIT INT TERM
  log "同步完成，当前版本 $commit，共校验 $VALIDATED_COUNT 个文件"
}

run_once="false"
[ "${1:-}" = "once" ] && run_once="true"

if [ "$run_once" = "true" ]; then
  sync_once
  exit 0
fi

while :; do
  if ! sync_once; then
    log "同步失败，继续保留上一次有效版本" >&2
  fi
  sleep "$interval"
done
