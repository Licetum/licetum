# Licetum

## 语言

[English](../README.md) · [Русский](README.ru.md) · **简体中文**

Licetum 是一个跨服务授权平台，为不同系统提供一种标准方式来询问用户可以访问哪些业务实体。

它的核心并不只是存储角色。Licetum 将权限与不同服务拥有的真实资源关联起来，并把最终的
可见性提供给 API、用户界面、MCP、搜索、客服系统和数据访问层。

## 问题

一个业务实体通常由一个服务拥有，但其他多个系统也需要使用它。

例如：

- 服务 A 拥有项目数据。
- MCP 服务、搜索服务或报表系统只应向当前用户展示其有权访问的项目。
- 私有数据服务需要过滤数据库行，让每个请求只返回被允许的实体 ID。
- 客服系统可以只允许个人创建自己的工单，同时允许团队成员查看团队中任何成员创建的工单。

没有统一契约时，每个使用方都必须实现自己的权限 endpoint、导出格式、同步任务和规则解释。
这些实现会逐渐产生差异：一个系统可能展示另一个系统隐藏的项目，用户、群组和共享资源的
相同逻辑也会在多个地方重复。

核心问题是：

> 如何让任何系统询问同一个用户对同一批业务实体的访问权限，而不必为每一对服务创建新的
> 权限集成？

## 标准可见性契约

使用方可以向 Licetum 询问当前主体在某个已注册服务中的权限：

    GET /service/{service_id}/permission
    Authorization: Bearer <user-token>

概念上的响应如下：

    {
      "project": {
        "type": "sync",
        "resources": [
          { "id": "1", "name": "Project 1", "access": ["read"] },
          { "id": "2", "name": "Project 2", "access": ["read", "write"] }
        ]
      }
    }

实际响应也会描述没有具体资源的类型。此时返回的是整个类型的权限，适合没有行 ID 的全局
能力，例如某个功能、操作或工作模式。

这个契约让所有使用方都能得到同一个答案：

> “你好，这个用户在该服务中可以访问哪些实体？”

使用方可以将返回的 ID 应用到自己的 API、界面、MCP 工具、搜索请求或数据库过滤器中。
Licetum 不返回业务记录，也不接管 CRUD。

## 资源如何发布

资源所属服务负责决定哪些业务实体要作为授权资源发布，以及要提供哪些关系和元数据。

Licetum 可以通过以下方式读取已发布的资源视图：

- 提供给服务账号的只读 SQL view，包括数据库副本中的 view；
- 资源 HTTP endpoint；
- 持续更新共享资源目录的同步器。

这样，每个使用方都不必实现自己的权限导出。源服务保留业务逻辑和数据所有权，Licetum
为所有使用方提供统一的权限契约。

公共资源和它们之间的关系也可以同步一次，并被多个服务复用。当同一个项目、组织、团队或
客户出现在多个系统中时，这有助于保持一致的可见性。

## Licetum 解决什么问题

Licetum 在仍由源服务拥有的资源之上构建统一的权限视图：

- **统一的可见性契约。** 使用方不必为每个系统实现“我的项目”或“权限导出” endpoint。
- **准确的资源 ID。** 响应可以列出主体可访问的全部实体以及每个实体的权限级别。
- **跨服务的一致可见性。** MCP、搜索、UI、报表、客服和私有数据系统可以使用同一个结果。
- **适合数据库访问控制。** 应用可以在 WHERE 条件中使用这些 ID，或据此构建 row-level
  security（RLS）策略。
- **业务逻辑留在源服务。** 资源所属服务决定发布哪些资源和关系，Licetum 负责计算访问权限。
- **减少重复集成。** 使用方不再需要针对每个上游服务单独实现契约、导出和同步。

## 具体权限决策

如果服务不需要完整的可见性列表，也可以直接提问：

- 该主体是否可以对项目 42 执行 write；
- 该用户是否可以创建新的工单；
- 该团队成员是否可以读取这个对话。

Check API 支持一次请求发送多个问题，还可以解释决策：哪些规则授予权限、哪些拒绝生效，以及
哪些更具体的例外取消了拒绝。

## 用户组、角色、层级和规则

统一的资源权限契约是核心价值。用户组、角色和其他策略功能让该契约更实用、更易理解：

- **用户组**汇集人员。用户组本身就是一个 subject，可以相互嵌套，成员关系可以依据 claims（部门、区域或组织）判定；
- **角色**是一组命名权限。角色回答“能做什么”（对资源类型、资源或通过 claim 找到的资源的访问级别），用户组回答
  “是谁”。授权规则可以把角色授予用户、用户组或符合 claims 的所有人，因此同一角色可以在各处复用；
- 资源类型可以引用同一服务或其他服务中的父类型：对父级的权限会级联到其下方的资源；
- 允许、拒绝和更窄的例外覆盖常见及罕见的业务场景；
- 生效时间和过期时间支持临时访问；
- 不针对具体资源 ID 的全局类型权限覆盖无需资源 ID 的能力。

例如，项目服务发布项目，团队服务发布团队成员关系，客服服务发布工单。“客服坐席”角色可以读取工单，授权规则把它授予
“客服团队”用户组：新加入该组的成员立即获得权限。各个使用方不需要为每种情况发明不同的权限协议。

## Licetum 不是什么

- 它不是身份提供商。Licetum 使用 OAuth2/OIDC token 及其 claims。
- 它不是业务数据库。源服务仍然保存自己的记录并负责 CRUD。
- 它不是数据库 RLS 扩展。Licetum 返回允许的 ID 或决策，使用方负责将结果应用到自己的查询和
  存储层。
- 它不只是前端过滤器。后端和数据访问层必须强制执行结果。

## 试用

仓库包含 PostgreSQL、Redis、本地 Dex OIDC、Go 后端和管理界面：

    task dev

打开 http://localhost:3000，使用 admin@example.com / password 登录。这些账号仅用于开发。

实现细节请参阅[core 指南](../core/README.md)、[admin API 指南](../admin-api/README.md)和[Web 指南](../web/README.md)。

## 仓库

项目位于 [Licetum](https://github.com/Licetum) 组织：

- [licetum](https://github.com/Licetum/licetum) — 本仓库：运行时和 admin API（Go），以及 Web 界面（Next.js）。
- [licetum-go-sdk](https://github.com/Licetum/licetum-go-sdk)、[licetum-ts-sdk](https://github.com/Licetum/licetum-ts-sdk)、
  [licetum-python-sdk](https://github.com/Licetum/licetum-python-sdk) — 检查 API 的客户端库（即将发布；各自带有自己的许可证文件）。
- [licetum-helm](https://github.com/Licetum/licetum-helm) — Helm chart。
- [licetum-docs](https://github.com/Licetum/licetum-docs) — 文档。

## 许可证

Licetum 由三部分组成，每部分采用各自的许可证（见 [LICENSE](../LICENSE)）：

- [`core/`](../core/LICENSE) — 引擎、检查 API、数据库结构和运行时：MIT 许可证。
- [`admin-api/`](../admin-api/LICENSE) — 管理 API：仅 GNU Affero General Public License v3.0。
- [`web/`](../web/LICENSE) — 管理界面：仅 GNU Affero General Public License v3.0。

如果您将修改后的 `admin-api/` 或 `web/` 作为网络服务运行，AGPL 要求您向该服务的用户提供其源代码。Licetum 的名称和标志不受这些许可证覆盖（见 [TRADEMARKS.md](../TRADEMARKS.md)）。
