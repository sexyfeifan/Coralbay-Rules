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
  geo_staging="/data/.geo-staging.$$"
  rm -rf "$staging"
  rm -rf "$geo_staging"
  trap 'rm -rf "$staging" "$geo_staging"' EXIT INT TERM

  log "开始同步 $repository ($branch)"
  git clone --quiet --depth 1 --single-branch --branch "$branch" "$repository" "$staging"
  commit="$(git -C "$staging" rev-parse HEAD)"
  validate_release "$staging"

  log "开始同步可读规则源 ($repository geo)"
  git clone --quiet --depth 1 --single-branch --branch geo "$repository" "$geo_staging"
  geo_commit="$(git -C "$geo_staging" rev-parse HEAD)"

  release_dir="/data/releases/$commit"
  if [ ! -d "$release_dir" ]; then
    rm -rf "$staging/.git"
    mv "$staging" "$release_dir"
  else
    rm -rf "$staging"
  fi

  # Regenerate deployment-specific files even if the upstream commit is the
  # same, so image upgrades and domain changes take effect immediately.
  mkdir -p "$release_dir/_mirror" "$release_dir/_templates/clients" "$release_dir/_assets" "$release_dir/_sources"
  rm -rf "$release_dir/_assets/icons"
  cp -R /app/assets/icons "$release_dir/_assets/icons"
  rm -rf "$release_dir/_sources/geo"
  mkdir -p "$release_dir/_sources/geo"
  cp -R "$geo_staging/site" "$geo_staging/ip" "$release_dir/_sources/geo/"
  cp "$geo_staging/LICENSE.txt" "$release_dir/_sources/geo/LICENSE.txt"
  rules_base_url="https://$mirror_domain/"
  assets_base_url="https://$mirror_domain/_assets/icons/"
  sed -e "s|__RULES_BASE_URL__|$rules_base_url|g" \
    -e "s|https://github.com/Koolson/Qure/raw/master/IconSet/Color/|$assets_base_url|g" \
    /app/templates/ppanel_openclash_pro_cn.gotmpl \
    > "$release_dir/_templates/ppanel_openclash_pro_cn.gotmpl.next"
  mv -f "$release_dir/_templates/ppanel_openclash_pro_cn.gotmpl.next" \
    "$release_dir/_templates/ppanel_openclash_pro_cn.gotmpl"

  # Template center: preserve every Perfect Panel client template locally.
  for source_template in /app/templates/clients/perfect-panel/*.gotmpl; do
    client="$(basename "$source_template" .gotmpl)"
    [ "$client" = "clash" ] && continue
    [ "$client" = "stash" ] && continue
    sed "s|https://github.com/Koolson/Qure/raw/master/IconSet/Color/|$assets_base_url|g" \
      "$source_template" > "$release_dir/_templates/clients/$client.gotmpl.next"
    mv -f "$release_dir/_templates/clients/$client.gotmpl.next" "$release_dir/_templates/clients/$client.gotmpl"
  done
  cp "$release_dir/_templates/ppanel_openclash_pro_cn.gotmpl" "$release_dir/_templates/clients/clash.gotmpl"

  # Keep Perfect Panel's node renderer, then map the complete 666OS Pro_cn
  # policy group and MRS rule layers to Stash-native fields.
  awk '/^proxy-groups:/{exit} {print}' /app/templates/clients/perfect-panel/stash.gotmpl \
    > "$release_dir/_templates/clients/stash.gotmpl.next"
  sed "s|__ASSETS_BASE_URL__|$assets_base_url|g" /app/templates/clients/stash-proxy-groups.yaml \
    >> "$release_dir/_templates/clients/stash.gotmpl.next"
  printf '\nrules:\n' >> "$release_dir/_templates/clients/stash.gotmpl.next"
  while IFS= read -r relative_path; do
    [ -n "$relative_path" ] || continue
    base="$(basename "$relative_path" .mrs)"
    suffix=""
    [ "${relative_path#mihomo/ip/}" != "$relative_path" ] && suffix="IP"
    provider="${base}${suffix}"
    case "$base" in
      Tracking|Advertising) target="广告拦截" ;;
      Private) target="DIRECT" ;;
      Speedtest) target="网络测试" ;;
      TM|Telegram) target="即时通讯" ;;
      SocialMedia) target="社交平台" ;;
      AI) target="人工智能" ;;
      Dev) target="开发服务" ;;
      Emby) target="EMBY" ;;
      Netflix|Disney|Streaming|YouTube|Spotify) target="国际媒体" ;;
      Games) target="游戏平台" ;;
      Crypto) target="货币平台" ;;
      Google) target="谷歌服务" ;;
      Facebook) target="脸书服务" ;;
      Microsoft) target="微软服务" ;;
      Apple) target="苹果服务" ;;
      Proxy) target="国外流量" ;;
      China) target="国内流量" ;;
      *) target="国外流量" ;;
    esac
    printf '  - RULE-SET,%s,%s\n' "$provider" "$target" >> "$release_dir/_templates/clients/stash.gotmpl.next"
  done < "$expected_file"
  printf '  - MATCH,漏网之鱼\n\nrule-providers:\n' >> "$release_dir/_templates/clients/stash.gotmpl.next"
  while IFS= read -r relative_path; do
    [ -n "$relative_path" ] || continue
    base="$(basename "$relative_path" .mrs)"
    behavior="domain"; suffix=""
    if [ "${relative_path#mihomo/ip/}" != "$relative_path" ]; then behavior="ipcidr"; suffix="IP"; fi
    provider="${base}${suffix}"
    printf '  %s: { type: http, behavior: %s, format: mrs, path: ./rules/%s.mrs, url: %s%s, interval: 86400 }\n' \
      "$provider" "$behavior" "$provider" "$rules_base_url" "$relative_path" \
      >> "$release_dir/_templates/clients/stash.gotmpl.next"
  done < "$expected_file"
  mv -f "$release_dir/_templates/clients/stash.gotmpl.next" "$release_dir/_templates/clients/stash.gotmpl"
  cat > "$release_dir/_mirror/status.json.next" <<EOF
{"ok":true,"repository":"$repository","branch":"$branch","commit":"$commit","geo_commit":"$geo_commit","synced_at":"$(date -u +%Y-%m-%dT%H:%M:%SZ)","validated_files":$VALIDATED_COUNT}
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
