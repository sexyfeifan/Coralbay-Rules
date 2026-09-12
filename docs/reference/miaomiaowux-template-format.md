# 妙妙屋 X V3 模板适配依据

核实日期：2026-09-12。目标是向妙妙屋 X 提供基于 YYDS666 / 666OS 的 Clash V3 模板，由妙妙屋管理和注入节点，最终交给 Mihomo 客户端使用。

## 官方依据

- [妙妙屋 X 模板管理](https://miaomiaowux.com/docs/templates/)：V3 的节点引入、占位符、筛选、导入和订阅绑定方式。中文页核实时显示更新于 2026-09-10。
- [妙妙屋 X 系统设置](https://miaomiaowux.com/docs/system-settings/#模板版本)：在功能设置中启用新模板系统并选择 V3；外部代理集合还有独立功能开关。
- [官方完整 redirhost V3 模板](https://github.com/iluobei/miaomiaowuX/blob/9012f4dc3e0881d35c1cc6708f8e221c8ed7fb50/rule_templates/redirhost__v3.yaml)：固定到核实时的主仓库提交，包含节点占位符及 HTTP `rule-providers`。
- [官方插件 V3 渲染器](https://github.com/mmwx-group/mmwX-plugins/blob/82caefb215a0174efeb0d20c9d4d43e44baec477/proxyparser/substore/template_v3.go)：固定提交 `82caefb`；旧组织 `MMWOrg` 的仓库已重定向到 `mmwx-group`。该仓库标明是妙妙屋 X 项目插件。
- [官方插件空组清理测试](https://github.com/mmwx-group/mmwX-plugins/blob/82caefb215a0174efeb0d20c9d4d43e44baec477/proxyparser/substore/template_v3_test.go#L671)：覆盖单层地区空组删除和引用清理。
- [Mihomo 规则集合格式](https://wiki.metacubex.one/config/rule-providers/#format)：`mrs` 支持 `domain`、`ipcidr`，与本项目的 666OS 规则文件对应。

主仓库当前没有公开完整 X 管理后台实现。插件源码可用于直接检验模板渲染，但不能据此推断用户已部署的私有后台版本、节点权限或导入页面行为。

## 导出格式约定

V3 文件是普通 Clash YAML；“V3”是妙妙屋选择的模板引擎，不是 YAML 顶层 `version: 3`、`$schema` 或 JSON 包装。下载文件采用 `.yaml`，名称可以带 `__v3` 便于识别。

顶层不放真实节点或机场订阅凭据，可使用 `proxies: null`。自动引入节点的组显式包含 `include-all: true`，并在 `proxies` 中按需要的顺序放置 `__PROXY_NODES__` 和 `__PROXY_PROVIDERS__`。这是官方模板采用的形式；占位符由妙妙屋渲染，不是给 Mihomo 直接执行的节点名。

导出时将 YAML 锚点和合并项展开成显式字段。官方插件的 `parseProxyGroup` 直接读取 YAML 节点，没有在此函数中展开 `<<` 合并或 `proxies` 的别名节点。

下面仅展示节点注入接口，不是完整业务模板：

```yaml
mode: rule
proxies: null
proxy-groups:
  - name: 全球手动
    type: select
    include-all: true
    proxies:
      - __PROXY_NODES__
      - __PROXY_PROVIDERS__
rules:
  - MATCH,全球手动
```

`proxy-providers` 是节点集合，`rule-providers` 是规则集合，二者独立。官方插件从调用方传入的集合数据展开节点；不能用一个占位符凭空创建机场订阅。模板使用者须先在妙妙屋配置自己的节点或代理集合。

## 20 组适配范围

本模块保留本项目收录的 YYDS Pro 中文版中全部 33 个 666OS MRS provider、28 条 `RULE-SET` 及最后的 `MATCH`，保持规则顺序和 17 个业务策略组名称。广告组默认阻断，苹果及国内组默认直连，其余业务组默认代理。

节点选择层改为“全球手动”“全球自动”“故障转移”三个稳定组，共 20 组。三个节点组不依赖国家筛选结果，节点由妙妙屋注入。此适配保留业务分流，未复刻原版九个地区的“策略 → 自动 / 均衡”结构，也不承诺保留原版美国、新加坡等地区优先级。需要指定地区时，可在妙妙屋另行调整所用节点或模板。

这一调整针对实际渲染行为：插件先删除渲染为空的组，再清理剩余组对已删除组的引用，没有继续递归删除清理后新变空的组。原版多层地区组可能在叶组消失后留下空的父组。`empty-fallback` 没有参与模板阶段的空组判定；`use`、`include-all`、`filter` 等还会在生成时被移除，不能假定这些字段会留到 Mihomo 再解决空组问题。

该问题已在隔离目录直接调用上述固定版本插件复现：只注入一个日本合成节点时，美国自动组和均衡组被删除，仍被业务组引用的美国策略组留下空列表；同时，官方针对单层地区组的引用清理测试通过。复现没有修改项目依赖、访问真实节点或操作妙妙屋后台。

## 镜像、上游及导入过程

本机镜像与上游模式只替换规则下载地址和相关资源地址，不更换 666OS 规则分类或改变规则顺序。本机镜像依赖 CoralBay 对外地址可以被客户端访问；导入模板后，是客户端按 provider 地址读取规则。

在妙妙屋选择“模板管理 → 新建模板 → Clash”，可用上传文件、粘贴文本或远程 YAML 链接导入，再在订阅文件的 V3 模板项绑定。远程链接导入的文档语义是拉取并保存，不能宣传为永久追踪远程模板。

本模块将同一模板中的规则固定到同一已验证版本。客户端按 provider 周期重新获取的仍是该版本；CoralBay 同步到新的规则版本后，应重新获取并导入新模板，才会切换到新版本。原模板 URL 不会悄悄改成另一版内容。

模板绑定会替换所绑定订阅的原规则配置。CoralBay 不应替用户修改妙妙屋中的既有订阅或登录凭据；本模块只提供可审核和导入的模板。

## 验证边界

2026-09-12 对本模块实际导出的本机、上游两份模板执行了以下验证：

| 验证层 | 实际结果 |
| --- | --- |
| 官方 V3 插件渲染 | 两种来源分别注入日本单节点、未知地区单节点、港美双节点，共 6 个场景通过 |
| 渲染产物完整性 | 每份保持 20 个非空策略组、33 个 MRS provider、29 条规则；规则及 provider 内容与输入一致，无残留占位符、无失效组引用、无组循环 |
| Mihomo 原生加载 | 上述 6 份渲染结果均通过 Mihomo v1.19.30 Linux arm64 语法检查及运行时加载；每份读入 33 个实际 MRS 文件，内核报告计数合计 171,227，20 个组及合成节点均可从本地控制接口读取 |
| 单独规则集导出 | 33 份实际 classical YAML 全部被真实内核加载；导出 payload 与加载计数逐文件相等，总和为 171,111，原模板 20 个组保持有效 |
| 转换语义核验 | 将每份 classical YAML 反向还原 domain / ipcidr 条目，再用同一内核生成 MRS；33 份重编码文件与原件解压后，除计数头外的字节全部相同 |

原生验证使用项目 Dockerfile 固定来源的 Mihomo 二进制，验证镜像固定为 `sha256:3b9c956373c77ae0dd68c319d79516ed66fe4917644902c03e65e1561366aa59`。规则文件来自已验证的 666OS 版本 `4109a511f4386346520b9c552038d6681b318c9c-38ba1c1cc085-v4.14.0-1bf689e6236f`。

为避免真实联网，所有内核运行均在独立 `network:none` 容器中完成：规则 URL 替换为同一批真实 MRS 的本地文件路径，关闭测试配置的 DNS 和配置记忆，测速组开启惰性检测。策略组类型和业务规则保留；没有连接真实代理节点，也没有重启预览或生产服务。因此这项验证证明模板渲染和 MRS 加载兼容，不验证公网 HTTP 拉取、DNS 解析或真实代理连通性。

MRS 的 171,227 与 YAML 的 171,111 相差 116，差额分布于 12 个集合，不代表少了匹配范围。MRS 头保存插入计数，实际域名集合和 IP 区间则会规范化；解码枚举实际集合，所以行数可能减少。以上逐文件重编码核验确认匹配结构完全相同，仅 8 字节计数头可能不同。对应实现见 [MRS 计数头](https://github.com/MetaCubeX/mihomo/blob/v1.19.30/rules/provider/mrs_converter.go#L57)、[域名集合计数与导出](https://github.com/MetaCubeX/mihomo/blob/v1.19.30/rules/provider/domain_strategy.go#L37)、[IP 区间合并](https://github.com/MetaCubeX/mihomo/blob/v1.19.30/rules/provider/ipcidr_strategy.go#L41)。

同轮项目验收还覆盖 Go race / vet、前端与脚本回归、桌面和手机布局，以及 Google 规则上游首次获取后的 YAML 摘要与本机版本一致。

直接调用固定版本的官方插件不等于已在用户的妙妙屋 X 私有管理后台完成上传、绑定、订阅生成。发布或验收记录必须明确实际跑过哪一层，不能声称覆盖所有 Clash、Surge、Loon 或其他客户端。
