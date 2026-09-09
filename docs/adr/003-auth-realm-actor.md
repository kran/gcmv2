# ADR-003：Auth Realm 与统一 Actor

- 状态：Accepted / Core Implemented
- 目标版本：v0.9.0
- 日期：2026-09-09

## 背景

当前认证由三部分组成：

- core：auth_methods、sessions 和 Node
- web：Cookie/Bearer、me/logout/bind
- plugin/password：register/login

当前 Web DTO 包含：

```go
Type string `json:"type"`
```

password 和 bind 在 Type 为空时默认使用 `"user"`。

问题：

- 通用框架不应假定存在 user 类型
- association 使用 member
- CRM 可能使用 contact、employee、partner
- 客户端可以决定内部 NodeType
- register/login/bind 的类型决策分散
- 管理员和前台用户是两套无法统一参与 Policy 的身份
- 以后加入 API Key/OAuth 时会继续扩散判断

## 决策

### 1. Core 不定义 User

认证主体仍然是一个声明 authentication capability 的 Node：

```text
member
contact
employee
partner
```

core 只处理：

- credential 与 Node 的绑定
- Session 创建、校验和删除
- 凭证唯一性
- 节点类型一致性

core 不决定：

- 谁可以注册
- 注册默认字段
- 默认角色
- 审核规则
- 登录路由

### 2. Web 使用 Auth Realm

Realm 是服务端名称到 NodeType 的映射：

```go
type AuthRealm struct {
    Name          string
    NodeType      string
    AllowRegister bool
    Default       bool
}
```

配置示例：

```go
site.Auth().Register(AuthRealm{
    Name:          "member",
    NodeType:      "member",
    AllowRegister: true,
    Default:       true,
})
```

多主体示例：

```go
site.Auth().Register(AuthRealm{Name: "customer", NodeType: "contact"})
site.Auth().Register(AuthRealm{Name: "staff", NodeType: "employee"})
```

Realm Name 是外部稳定标识；NodeType 是内部 Schema 名。客户端永远不能直接提交 NodeType。

### 3. Credential Plugin 只处理认证方式

```go
password.Mount(site, password.Options{Realm: "member"})
wechat.Mount(site, wechat.Options{Realm: "member", ...})
oidc.Mount(site, oidc.Options{Realm: "staff", ...})
```

插件职责：

- 解析 credential
- 校验 credential
- 调用 Realm 注册/登录
- 成功后创建 Session

插件不负责：

- 定义 user/member 类型
- 默认 role
- 审核状态
- 业务资料字段

### 4. register/login 请求删除 Type

目标 DTO：

```go
type RegisterInput struct {
    Method     string
    Identifier string
    Secret     string
    Display    string
    Fields     map[string]any
}

type LoginInput struct {
    Method     string
    Identifier string
    Secret     string
}
```

NodeType 由 Mount 的 Realm 决定。

不保留旧 Type 字段，不增加兼容分支；同步修改现有站点。

## 路由

所有 credential 路由统一显式携带 Realm，不根据 Realm 数量切换路由形态：

```text
POST /api/auth/{realm}/register
POST /api/auth/{realm}/login
POST /api/auth/{realm}/bind
POST /api/auth/{realm}/{provider}
```

Session 操作保持全局入口：

```text
POST /api/auth/logout
GET  /api/auth/me
```

例如 association 使用：

```text
POST /api/auth/member/register
POST /api/auth/member/login
POST /api/auth/member/bind
POST /api/auth/member/wechat
```

Realm 名只能包含小写字母、数字、下划线和连字符。插件挂载时必须引用已注册 Realm；未知 Realm 在启动期直接 panic。框架不同时保留无 Realm 的旧路由。

## Actor

认证结果统一转换为 Actor：

```go
type ActorKind string

const (
    ActorAnonymous ActorKind = "anonymous"
    ActorNode      ActorKind = "node"
    ActorAdmin     ActorKind = "admin"
    ActorAPIKey    ActorKind = "api_key"
)

type Actor struct {
    Kind     ActorKind
    NodeID   int64
    NodeType string
    Realm    string
    Scopes   []string
}
```

Actor 是请求身份，不是业务实体副本。

### Anonymous

没有有效凭据时返回明确 Anonymous Actor，而不是到处判断 nil。

### Node Actor

前台 Session 对应一个 auth-enabled Node。

### Admin Actor

现有独立 accounts 管理员通过适配器变成 Admin Actor，不强迫管理员也是 Node。

### API Key Actor

后续 API Key 带 scopes，仍进入同一 Policy。

## Request Context

`CmsCtx.User()` 已替换为：

```go
func (c *CmsCtx) Actor() Actor
```

需要业务 Node 时：

```go
func (c *CmsCtx) Principal() (*core.Node, error)
```

这样 Policy 可以统一处理匿名、会员、管理员和 API Key，而业务代码只有确实需要资料时才加载 Node。

## Session

### Session 数据

```text
token_hash
node_id
realm
expires_at
created_at
```

建议数据库不保存明文 Token，只保存哈希。客户端只持有原始 Token。

### 规则

- [x] Token 使用 crypto/rand。
- [x] 数据库只保存 SHA-256 Token hash。
- [x] Session 有服务端过期时间。
- [x] logout 删除当前 Session。
- [x] Core 提供删除某个 Node 全部 Session 的原语。
- [ ] 修改密码流程调用删除该 Node 的全部 Session。
- [ ] 归档认证 Node 后删除全部 Session。
- [x] Session 续期频率受限，不每次请求写库。
- [ ] 支持列出和撤销当前账号的其他 Session。

## Bind

Bind 必须遵守：

```text
当前 Actor.NodeID
→ 加载 Node
→ Node.Type 必须等于 Realm.NodeType
→ credential method 允许绑定
→ credential identifier 未被占用
→ 写 auth_methods
```

请求中不再接受 Type。

对于 password：

- secret 最短长度由插件配置
- bcrypt/argon2 错误必须返回
- 不在日志中记录 identifier 和 secret

## 注册 Policy

Realm 的 `AllowRegister` 只是是否开放入口。具体业务规则通过 Hook/Policy：

```go
BeforeRegister(ctx, realm, input, node)
AfterRegister(ctx, actor)
BeforeLogin(ctx, realm, method, identifier)
AfterLogin(ctx, actor)
```

站点可以：

- 设置默认角色
- 强制待审核状态
- 限制邮箱域名
- 关闭自助注册
- 记录登录审计

## 安全

- [ ] Login/Register/Bind 有独立限流 Hook（后续安全中间件阶段）。
- [x] 登录失败响应不区分账号不存在和密码错误。
- [x] Cookie Secure/SameSite/TTL 由 Site 配置；Domain 暂不开放。
- [x] Bearer 和 Cookie 使用同一 Session，但提取规则明确。
- [x] Session Token/hash 不进入 SQL 参数日志；站点可关闭敏感 SQL 参数日志。
- [x] OAuth session_key/access_token 不进入 Node.Fields。
- [x] 管理员认证和前台认证使用不同 Cookie。

## API Key

API Key 不伪装成 Node Session：

- Key 自身有 ID、名称、hash、scopes、过期时间
- 解析后产生 ActorAPIKey
- 可选关联一个 Node 作为 owner
- Policy 根据 Actor Kind 和 Scopes 判断

## 直接迁移

v0.9 已执行：

1. 删除 RegisterInput.Type 和 LoginInput.Type。
2. 删除 `type == "" -> "user"`。
3. 使用认证插件的 Site 必须显式注册 Realm。
4. password.Mount 必须指定 Realm。
5. association 注册 member Realm。
6. 一次性修改测试、前端和示例。
7. 不保留旧 DTO、旧路由和 fallback user。
8. 前台 Session 迁移为 Realm-bound hash 存储；升级时旧 Session 失效并要求重新登录。

数据库中的 `auth_methods.type` 继续保存 NodeType，因为凭据绑定的是 Node；`sessions.realm` 保存创建会话时使用的 Realm。Realm 到 NodeType 的权威映射只存在于 Site 配置。迁移 `00010_auth_realms.sql` 将明文 Token 改为 hash，并主动使旧前台 Session 失效。

## 影响

### 正面

- 框架不依赖 user 模型
- 客户端不能选择内部认证类型
- 多认证主体有明确边界
- credential 插件可以独立复用
- Policy 获得统一 Actor

### 代价

- password.Mount 和请求 DTO 发生破坏性变化
- association 和其他站点需要同步修改
- Admin 与前台 Actor 合并需要重新梳理 Hook

## 验收条件

- [x] 不定义 user 类型的站点可以使用 password 登录。
- [x] 多个自定义 NodeType 可以分别注册 Realm。
- [x] 客户端请求 DTO 中不存在 NodeType 字段，未知 `type` 被拒绝。
- [x] bind 不能跨 Realm。
- [x] 非 auth-enabled Type 不能注册 Realm。
- [x] Session 和 Admin 可解析为 Actor；API Key 中间件可通过 `SetActor` 安装 Actor。
- [ ] Policy 测试不依赖具体业务类型名（Policy 阶段完成）。
- [x] 生产代码中不存在 `"user"` 默认认证类型。
