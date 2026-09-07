# 自定义分流模块的规则来源

新模块直接使用 [MetaCubeX/meta-rules-dat](https://github.com/MetaCubeX/meta-rules-dat) 的 `meta` 分支，与现有 666OS 同步和存储分开。

目录映射见仓库根目录 `routing_catalog.json`；参考站来源核验见 `nextin-rule-sources-2026-09-07.json`。参考站用于核对名称和来源映射，其 UI、脚本和文案不随本模块分发。

- 域名规则：`geo/geosite/classical/<file>.yaml`，保留精确域名、域名后缀、关键词与正则语义。
- IP 规则：`geo/geoip/<file>.yaml`，规范化为 CIDR 并添加 `no-resolve`。
- 每批读取 `meta` 分支的提交 SHA 后，只从该固定提交抓取。API 暂不可用且没有可用缓存时，可以使用经核验的固定提交 `8964c30fc18a52dfc6761d30c664faaac0c219b1`，并显示版本检查警告。
- 快照记录固定来源 URL 和原始文件的 SHA-256；规范化快照另有完整 SHA-256 校验。生成配置内嵌规则，不依赖 Nextin。
- BT Tracker 是 BitTorrent Tracker 集合，默认代理且不列入推荐拦截项。

上游仓库以 GNU General Public License v3.0 发布；本目录保留原样的 [上游许可证](metacubex-meta-rules-dat-LICENSE)。上游还合并多个规则项目，相关来源和许可信息应参见其 [构建流程](https://github.com/MetaCubeX/meta-rules-dat/blob/master/.github/workflows/run.yml) 及各上游项目。规则数据的许可与 CoralBay 自身代码的许可分别保留。

缓存只写入 `$DATA_DIR/routing/rules`，不会改写 `$DATA_DIR/current`、旧规则快照或旧订阅记录。

2026-09-07 实现验证时，以上固定提交的 78 个实际 classical/IP 文件均通过正常 TLS 下载，并由生产同一解析器逐项校验，共接受 161,729 条规则。验证包含关键词、含逗号量词的正则以及 IPv4/IPv6；普通单元测试使用本地模拟 HTTP 响应，不依赖联网。该结果验证源格式和语义保留，不替代各代理客户端的运行验收。
