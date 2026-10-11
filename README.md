# Licetum

## Languages

**English** · [Русский](docs/README.ru.md) · [简体中文](docs/README.zh-CN.md)

Licetum is a cross-service authorization platform for a standard way to ask which business entities
a user can access.

The main problem it solves is duplicated permission contracts and exports. A service should not
need a custom endpoint for every consumer that asks for the user's projects, tickets, documents, or
other resources. Licetum provides one common contract for this question and returns the permission
projection that another system can apply to its own data.

## The problem

A business entity is often owned by one service but used by several others.

For example:

- Service A owns projects.
- An MCP service, search service, or reporting service needs to show only projects available to
  the current user.
- A private-data service needs to filter database rows so that every request returns only allowed
  entity IDs.
- A support system may let a person create tickets only for themselves, while team members can see
  tickets created by any member of their team.

Without a common contract, every consuming service has to implement its own permissions endpoint,
export format, synchronization job, and interpretation of access rules. These implementations
drift apart. One system may show a project that another hides, or every service may repeat the same
logic for users, groups, and shared resources.

The core question is:

> How can any system ask for the same user's access to the same business entities without building
> a new permission integration for every pair of services?

## The standard visibility contract

A consuming service asks Licetum for the current subject's permissions in a registered service:

    GET /service/{service_id}/permission
    Authorization: Bearer <user-token>

Conceptually, the response looks like this:

    {
      "project": {
        "type": "sync",
        "resources": [
          { "id": "1", "name": "Project 1", "access": ["read"] },
          { "id": "2", "name": "Project 2", "access": ["read", "write"] }
        ]
      }
    }

The actual response also describes resource types without concrete resources. Those types return
access to the type as a whole, which is useful for global capabilities such as a feature, operation,
or mode.

This contract gives every consuming system the same answer to the same question:

> “Hello, what can this user access in that service?”

The consumer can then use the returned IDs in its own API, UI, MCP tool, search query, or database
filter. Licetum does not return business records and does not take over CRUD ownership.

## How resource publication works

The source service remains responsible for deciding which of its business entities are published
as authorization resources and what relationships or metadata they expose.

Licetum can read that published resource view through:

- a read-only SQL view available to a service account, including a database replica;
- an HTTP resource endpoint;
- a synchronizer that keeps the shared resource catalog current.

This removes the need for every consumer to implement its own permission export. The source service
keeps its business logic and data ownership; Licetum standardizes the permission contract used by
all consumers.

The same model also allows common resources and relationships to be synchronized once and reused by
several services. This helps keep visibility consistent when the same project, organization, team,
or customer appears in multiple systems.

## What problem does Licetum solve?

Licetum builds a shared permission view over resources that remain owned by their source services:

- **One visibility contract.** Consumers ask one standard API instead of implementing a custom
  “my projects” or “export permissions” endpoint for every system.
- **Exact resource IDs.** A permission response can list every resource available to a subject and
  the access levels granted on each resource.
- **Consistent cross-service visibility.** MCP, search, UI, reporting, support, and private-data
  systems can apply the same result.
- **Database-friendly access control.** Applications can use the returned IDs in a WHERE filter or
  use them to build a row-level security (RLS) policy.
- **Business logic stays at the source.** The owning service decides which resources and relationships
  are published; Licetum evaluates access to them.
- **Less repeated integration work.** Consumers no longer need separate contracts, permission exports,
  and synchronization code for every upstream service.

## Direct permission checks

Sometimes a service needs one decision rather than a complete visibility list:

- may this subject perform write on project 42?
- may this user create a new ticket?
- may this team member read this conversation?

The check API supports multiple questions in one request and can explain the result: which rules
granted access, which denials applied, and which narrower exceptions lifted them.

## Groups, roles, hierarchies, and rules

The core value is the common resource-permission contract. Groups, roles and other policy features make that
contract practical and understandable:

- **Groups** gather people. A group is a subject of its own, groups can be nested, and membership may depend on
  claims such as department, region or organization.
- **Roles** are named sets of permissions. A role says *what* can be done (access levels on a resource type, on a
  resource, or on a resource found through a claim); a group says *who*. An access rule grants a role to a user, to a
  group, or to everyone who matches some claims, so the same role can be reused everywhere.
- Resource types can reference parents from the same or another service: a permission on a parent cascades to the
  resources below it.
- Allow rules, deny rules and narrower exceptions cover both common and exceptional business cases.
- Start and expiration dates support temporary access.
- Global type-level permissions cover capabilities that have no concrete resource ID.

For example, a project service can publish projects, a team service can publish team membership, and a support
service can publish conversations. A "support agent" role can read conversations, and an access rule grants it to the
"support team" group: every new member of the group gets access at once. Consumers do not need to invent a different
permission protocol for each case.

## What Licetum is not

- It is not an identity provider. It uses OAuth2/OIDC tokens and their claims.
- It is not a business database. Source services keep their records and remain responsible for CRUD.
- It is not a database RLS extension. Licetum provides authorized IDs or decisions; the consuming
  service applies them to its own query and storage layer.
- It is not only a frontend filter. Backend and data-access layers must enforce the result.

## Try it

The repository includes PostgreSQL, Redis, a local Dex OIDC provider, the runtime, the admin API and the
admin interface:

    task dev

Open http://localhost:3000 and use admin@example.com / password. These credentials are for
development only.

For implementation details, see the [core guide](core/README.md) (the runtime and the check API),
the [admin API guide](admin-api/README.md) and the [web guide](web/README.md).

## Repositories

The project lives in the [Licetum](https://github.com/Licetum) organization:

- [licetum](https://github.com/Licetum/licetum) — this repository: the runtime and the admin API (Go), and the web interface (Next.js).
- [licetum-go-sdk](https://github.com/Licetum/licetum-go-sdk), [licetum-ts-sdk](https://github.com/Licetum/licetum-ts-sdk),
  [licetum-python-sdk](https://github.com/Licetum/licetum-python-sdk) — client libraries for the check API (to be published;
  each carries its own license file).
- [licetum-helm](https://github.com/Licetum/licetum-helm) — the Helm chart.
- [licetum-docs](https://github.com/Licetum/licetum-docs) — the documentation.

## License

Licetum is made of three parts, each under its own license (see [LICENSE](LICENSE)):

- [`core/`](core/LICENSE) — the engine, the check API, the database schema and the runtime: MIT License.
- [`admin-api/`](admin-api/LICENSE) — the admin API: GNU Affero General Public License v3.0 only.
- [`web/`](web/LICENSE) — the admin interface: GNU Affero General Public License v3.0 only.

If you run a modified `admin-api/` or `web/` as a network service, the AGPL requires you to offer its source to
the users of that service. The names and logos of Licetum are not covered by these licenses ([TRADEMARKS.md](TRADEMARKS.md)).
