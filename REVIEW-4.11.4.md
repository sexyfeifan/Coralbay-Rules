# v4.11.4：修复 Stash VLESS 模板缺少 TLS 开关

## 问题与修复

仓库内 `templates/clients/perfect-panel/stash.gotmpl` 的 VMess 分支会根据 Security 输出 `tls: true`，VLESS 分支却没有对应逻辑。因此通过该模板生成的 VLESS + TLS / Reality 配置可能缺少 TLS 开关，导致客户端无法正确加载或连接。

本次在 VLESS 分支中增加相同的条件：仅当 Security 为 `tls` 或 `reality` 时输出 `tls: true`。普通 VLESS 保持原样；不改变 UUID、Reality 公钥、short ID、SNI、flow、规则与策略组。

规则同步会同时生成修正后的原始 Stash 模板与 CoralBay 适配版。生成器版本提升至 4.11.4，避免命中旧的不可变发布目录。

## 验证与边界

- 从实际模板提取 VLESS 分支渲染，覆盖 TLS、Reality、none、空 Security，以及 TCP、WebSocket、gRPC 共 12 个组合，并解析生成的 YAML 校验字段。
- Go 竞态测试与静态检查通过。
- 检查时线上 PPanel 数据库中的 Stash 模板已经包含该字段，未再次覆盖它。
- 检查时当前 CoralBay 转换订阅中的 7 条 VLESS Reality 节点均已包含 `tls: true`。因此不能把用户当前故障直接归因于这份转换结果缺少 TLS。
- 服务器兼容内核对照检查中，去掉 TLS 字段会导致配置被拒绝加载。这不是 Stash iOS/macOS 实机验证，客户端版本、本地网络和客户端已缓存配置仍可能影响使用。

Stash 协议文档：https://stash.wiki/proxy-protocols/proxy-types

用户应在 Stash 内重新更新订阅配置；替换 PPanel 模板时使用 4.11.4 生成的模板，避免再次导入旧版本。
