# 妙妙屋X：导入 YYDS 模板与规则集

**适用版本：CoralBay Rules 4.15.0。** 本指南介绍妙妙屋X 模板与规则集的导入方式。使用前请确认 CoralBay 实例已运行支持该功能的版本；发布说明和可用镜像以 [GitHub Releases](https://github.com/sexyfeifan/Coralbay-Rules/releases) 为准。

登录 CoralBay 后，打开“客户端配置 → 妙妙屋X 模板”，或访问本站 `/miaomiaowu`。本页使用 [666OS / YYDS Pro_cn](https://github.com/666OS/YYDS/blob/main/mihomo/config/cn/Pro_cn.yaml) 的规则方案，与已有 PPanel 模板、MihomoPro 覆写和订阅转换分别管理。

## 先选择要导入的内容

| 目标 | CoralBay 提供的内容 | 妙妙屋X 中的入口 |
| --- | --- | --- |
| 让一份订阅使用完整 YYDS 业务分流 | Clash V3 模板，包含策略组和规则引用，不包含节点 | 模板管理 → 新建／导入模板 → Clash |
| 在妙妙屋X 管理某一项规则，例如 Google 或 China IP | `payload` YAML 规则集正文，不包含策略组和节点 | 规则集管理 → 新建规则集 → 手动维护 |

完整模板可直接引用 CoralBay 或上游的 MRS 规则，无需先把 33 项规则全部导入妙妙屋X。“规则集管理”是单独维护规则文件的入口；只需要哪一项，就生成和导入哪一项。

首次使用前，在 CoralBay“规则与配置源 → 666OS 规则资源”完成同步，确保原配置及 33 份原始 MRS 均已验证。本页资源不可用时会显示原因和同步入口。上游模式也以本机记录的已验证版本为依据；不会跳过版本校验或换用其他规则库。

## 导入完整 Clash V3 模板

1. 在 CoralBay 页面上方选择模板的“本机镜像”或“上游源”，查看预览和来源版本。
2. 点击“下载 YAML”或“复制模板正文”。文件名为 `CoralBay_MiaoMiaoWuX_YYDS_local.yaml` 或 `CoralBay_MiaoMiaoWuX_YYDS_upstream.yaml`。
3. 在妙妙屋X 的模板管理中新建 Clash 模板，上传 YAML 文件或粘贴正文并保存。妙妙屋X 应启用 V3 模板系统；具体设置见[官方模板管理说明](https://miaomiaowux.com/docs/templates/)。
4. 在妙妙屋X 的订阅文件中选用这份 V3 模板，并绑定自己的节点或代理集合，由妙妙屋X 生成最终客户端订阅。
5. 用支持 MRS 的 Mihomo 客户端导入妙妙屋X 生成的订阅，检查节点、策略组及规则下载状态。CoralBay 下载的模板文件本身不包含节点。

V3 指妙妙屋X 的模板引擎。导入的是普通 Clash YAML，无需手动添加 `version: 3`。文件中的 `__PROXY_NODES__`、`__PROXY_PROVIDERS__` 等占位符由妙妙屋X 注入；它们不是实际节点，也不是 PPanel 的 `.gotmpl` 语法。只有在妙妙屋X 中主动保存并绑定模板，才会改变对应订阅的规则配置。

### 保留的分流与适配范围

| 内容 | 本模板的处理方式 |
| --- | --- |
| 规则文件 | 保留 YYDS 使用的 33 个 MRS provider，维持各自的 `domain`／`ipcidr` 行为 |
| 规则顺序 | 保留原序的 28 条 `RULE-SET` 和最后一条 `MATCH`，共 29 条；未被引用的备用集合不额外插入路由 |
| 业务策略组 | 保留 17 个业务组名称，以及对应的直连、代理、广告处理默认选项 |
| 节点选择 | 适配为“全球手动”“全球自动”“故障转移”三个组；加上业务组共 20 组 |
| 地区策略 | 不复刻原版多层地区组、地区均衡和美国／新加坡等地区优先级；需要时在妙妙屋X 中自行调整节点或模板 |

该适配面向妙妙屋X 的 V3 渲染与 Mihomo 规则格式。完整来源依据与空地区组的适配原因见[格式说明](reference/miaomiaowux-template-format.md)。

## 单独导入规则集

1. 在同页“单独导入 YYDS 规则集”中选择一项。域名与 IP 规则分别列出，例如 `Google · 域名规则`、`China · IP 规则`。
2. 在规则集自己的来源选项中选择“本机镜像”或“上游源”。此选项独立于上方完整模板的来源。
3. 点击“生成并预览”。首次转换可能需要约 20 秒；页面只转换当前选中的一项，不会预加载全部 33 项。
4. 生成成功后，复制规则集正文或下载 YAML 文件。页面同时显示推荐文件名，例如 `yyds-domain-Google.yaml`。
5. 在妙妙屋X 打开“规则集管理 → 新建规则集”，填写文件名和备注，来源选择“手动维护”。将正文粘贴到内容区，或点击“从文件导入”选择下载的 YAML，然后保存。

大规则集在页面中最多预览前 200 条、64 KiB 正文，并显示实际预览条数；“复制规则集正文”和下载文件始终包含完整规则，不会混入预览提示。

规则集正文形如下面的示例；这是格式示意，不是 Google 规则的实际完整内容：

```yaml
payload:
  - DOMAIN-SUFFIX,example.com
  - IP-CIDR,192.0.2.0/24
```

CoralBay 从原始 MRS 解码并生成可读 YAML，记录原件摘要、产物摘要和规则条数。页面“原始规则来源”中的 MRS 链接用于核对原件；二进制 MRS 不能直接粘贴进规则集内容区。

### 在模板中引用导入后的规则集

规则集保存成功并不等于已有模板已经引用它。使用 CoralBay 提供的“复制引用片段”，将对应 provider 合并到模板的 `rule-providers` 中，并在 `rules` 中引用相同的 provider 名称。

引用片段默认使用 **CoralBay 提供的 YAML 地址**。若要由妙妙屋X 托管规则文件，请将 `url` 改为妙妙屋X 为该文件提供的公开地址。以下是 Google provider 的结构示例：

```yaml
rule-providers:
  Google:
    type: http
    format: yaml
    behavior: classical
    url: "替换为 CoralBay YAML 链接或妙妙屋X 的规则集公开地址"
    path: ./rule-providers/yyds-domain-Google.yaml
    interval: 86400

rules:
  - RULE-SET,Google,谷歌服务
```

将示例合并进现有配置时，保留其他 provider、策略组和规则，并让 `RULE-SET` 位于兜底 `MATCH` 之前。“谷歌服务”须是当前模板中存在的策略组；完整 YYDS 模板已经包含它。

**从 MRS 切换到本页生成的 YAML 时，要一起修改 `url`、`format`、`behavior` 和本地缓存 `path`。** 原 provider 的 `format: mrs` 与 `behavior: domain`／`ipcidr` 应改为 `format: yaml` 与 `behavior: classical`，缓存路径使用对应 `.yaml` 文件名。只换 URL 会导致客户端按错误格式解析。若原模板已经有同名 provider，应替换该项，避免重复定义顶层 `rule-providers` 或同名集合。

## “本机镜像”与“上游源”各控制什么

这里的“本机”始终是 **部署 CoralBay 的服务器**，不是妙妙屋X 主控所在的服务器，也不是用户的手机或电脑。

| 选择位置 | 本机镜像 | 上游源 |
| --- | --- | --- |
| 完整模板 | 客户端按模板中的 provider 地址，从 CoralBay 获取固定版本 MRS | 客户端从同一规则版本的 666OS 上游地址获取 MRS |
| 单独规则集 | CoralBay 从已验证的本机 MRS 原件生成 YAML | CoralBay 获取并核对同提交的上游 MRS，再生成 YAML |

单独规则集无论选择哪一种来源，生成的 YAML 都先由 CoralBay 托管。导入妙妙屋X 后，则是妙妙屋X 保存的一份副本；把模板的引用地址换成妙妙屋X 的公开地址后，客户端才会从妙妙屋X 获取这份文件。

两个选择只影响本页各自的产物，不修改原始节点订阅、不改变其他入口的来源，也不会自动修改妙妙屋X 中已经导入的文件。来源或规则条目切换后，页面会清空上一次结果，新的正文验证成功后才开放复制和下载。

## 更新、固定版本与备份

完整模板和独立规则集都使用固定版本。客户端按 provider 的 `interval` 再次下载，取得的仍是该固定版本；CoralBay 同步到新提交不会让旧 URL 悄悄换成新规则。

更新时，先在 CoralBay 同步 666OS，再刷新本页：完整模板需要重新获取并导入；独立规则集需要重新生成并在妙妙屋X 中重新导入或替换。若 provider 地址发生变化，还应更新模板中对应的引用。通过“手动维护”导入的内容不会自动追踪 CoralBay 或上游。

升级与迁移前，按[搭建指南的备份步骤](deployment.md#备份与回退)保留 `.env`、`compose.yaml` 和**整个 `data/`**。本功能新增的目录包括：

| 数据目录 | 内容 |
| --- | --- |
| `data/rule-templates/miaomiaowu/v1/` | 本机／上游两种完整模板及版本清单 |
| `data/miaomiaowu/rulesets/v1/` | 按版本、来源、规则集保存的 YAML 与摘要记录 |

模板和规则集还依赖 `data/rule-resources/666os/` 的原件及 `data/rule-projections/` 的解码缓存。不要只备份上述新增目录，也不要只保留 SQLite 数据库；恢复整个数据目录才能保留已交付的固定地址和旧订阅所需资源。

本机 HTTP 开发预览仅将同一回环主机和端口的页面下载链接适配为 HTTP。正式部署仍使用 HTTPS；预览链接不能作为对外服务地址分发。
