#!/bin/sh
set -eu

repository="${RULES_REPOSITORY:-https://github.com/666OS/rules.git}"
branch="${RULES_BRANCH:-release}"
interval="${SYNC_INTERVAL:-21600}"
mirror_domain="${MIRROR_DOMAIN:-rules.coralbay.top}"
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
    mv "$staging" "$release_dir"
  else
    rm -rf "$staging"
  fi

  # Regenerate deployment-specific files even if the upstream commit is the
  # same, so image upgrades and domain changes take effect immediately.
  mkdir -p "$release_dir/_mirror" "$release_dir/_templates"
  rules_base_url="https://$mirror_domain/"
  sed "s|__RULES_BASE_URL__|$rules_base_url|g" \
    /app/templates/ppanel_openclash_pro_cn.gotmpl \
    > "$release_dir/_templates/ppanel_openclash_pro_cn.gotmpl.next"
  mv -f "$release_dir/_templates/ppanel_openclash_pro_cn.gotmpl.next" \
    "$release_dir/_templates/ppanel_openclash_pro_cn.gotmpl"
  cat > "$release_dir/_mirror/status.json.next" <<EOF
{"ok":true,"repository":"$repository","branch":"$branch","commit":"$commit","synced_at":"$(date -u +%Y-%m-%dT%H:%M:%SZ)","validated_files":$VALIDATED_COUNT}
EOF
  mv -f "$release_dir/_mirror/status.json.next" "$release_dir/_mirror/status.json"
  cat > "$release_dir/index.html.next" <<EOF
<!doctype html>
<html lang="zh-CN">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>CoralBay Rules</title></head>
<body style="font-family:system-ui,sans-serif;max-width:760px;margin:60px auto;padding:0 20px;line-height:1.7">
<h1>CoralBay Rules</h1>
<p>666OS Mihomo MRS 自托管规则镜像。</p>
<ul>
  <li><a href="/_mirror/status.json">同步状态</a></li>
  <li><a href="/_templates/ppanel_openclash_pro_cn.gotmpl" download>PPanel OpenClash Pro_cn 订阅模板</a></li>
  <li><a href="/mihomo/domain/AI.mrs">规则文件示例：AI.mrs</a></li>
</ul>
<p>当前上游版本：<code>$commit</code></p>
</body></html>
EOF
  mv -f "$release_dir/index.html.next" "$release_dir/index.html"

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
