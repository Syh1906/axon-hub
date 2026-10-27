# AGENTS.md

This file provides guidance to AI coding assistants when working with code in this repository.

> **Detailed rules are split into focused files under `.agent/rules/`**. See [Rules Index](#rules-index) below.

## Global Rules

1. Do NOT run lint or build commands unless explicitly requested by the user.
2. Do NOT restart the development server — it's already started and managed.
3. All summary files should be stored in `.agent/summary` directory if available.

## 仓库维护模式

- 本仓库的直接上游是 [ChouUn/axon-hub](https://github.com/ChouUn/axon-hub)，
  原始上游是 [looplj/axonhub](https://github.com/looplj/axonhub)。两层来源不可混为一条基线。
- 分支、上游同步、版本与发版遵循 [独立发版与部署](docs/zh/development/release.md)。
  发布来源以 `release.config.json` 为准；自己的 tag 使用 `<直接上游 tag>.syh.N`，
  不占用 ChouUn 的 `fork.N`。生产镜像由 GitHub Actions 发布到本仓库 GHCR。
- 正式／预发布由 `release.config.json.prerelease` 独立指定，不根据上游 tag 的 beta/rc
  名称推断。正式发布设为 GitHub 最新版本；不得移动 tag 或替换已发布构建产物来调整级别。
- 本 fork 的 feature、bugfix topic 及其维护检查表记录在
  [Fork 维护台账](docs/fork-maintenance.md)。新增或实质修改 fork topic 时更新台账；
  同步上游 release 时逐项核对。
- 上游 release tag 是定期同步的基线。合并或 cherry-pick 上游 release 前，必须同时检查
  上游的 schema、data migration 和 fork 改动。
- 本 fork 的专有行为和迁移应与上游历史保持可区分。除非有明确要求，不要仅为使分支看起来
  像上游而改写或删除既有的 fork 维护改动。

## 数据库迁移兼容性

- 将 `internal/ent/migrate/migrations/`（schema migration）和
  `internal/ent/migrate/datamigrate/`（data migration）都视为升级路径代码。
- 新增 fork 迁移前，必须对照目标上游 release tag 及其迁移版本；不要复用上游迁移版本，
  也不要假定看似未使用的版本号已经被 fork 保留。
- 新增自己的 data migration 使用 `<直接上游 tag>.syh.N` 形式（例如
  `v1.0.0-beta10-fork.7.syh.1`），已有 ChouUn 迁移保持原版本。版本顺序以 `xversion.Parse`
  的数值感知比较为准（原生 semver 会把 `beta10` 排在 `beta9` 之前）。必须验证它与周边
  上游版本的顺序，并在 `NewMigrator` 中按对应的执行顺序注册。
- 已发布的迁移视为不可变。需要修正时新增幂等迁移，不要修改既有迁移的行为或版本号。
- 每个 fork 迁移都要补充升级路径测试，覆盖相关数据库方言行为；同步上游前检查下一个
  上游 release 是否存在版本或 schema 冲突。

## Configuration

- Backend API: port 8090, Frontend dev server: port 5173 (proxies to backend).
- Configuration: `conf/conf.go` (YAML + env var), SQLite by default.

## Project Overview

AxonHub is an all-in-one AI development platform that serves as a unified API gateway for multiple AI providers. It provides OpenAI and Anthropic-compatible API interfaces with automatic request transformation, enabling seamless communication between clients and various AI providers through a sophisticated bidirectional data transformation pipeline.

## Technology Stack

- **Backend**: Go 1.26.0+ with Gin, Ent ORM, gqlgen, FX
- **Frontend**: React 19 + TypeScript, TanStack Router/Query, Zustand, Tailwind CSS

## Backend Structure

- `cmd/axonhub/main.go` — Application entry point
- `internal/server/` — HTTP server and route handling with Gin
- `internal/server/biz/` — Core business logic and services
- `internal/server/api/` — REST and GraphQL API handlers
- `internal/server/gql/` — GraphQL schema and resolvers
- `internal/ent/` — Ent ORM for database operations
- `internal/ent/schema/` — Database schema definitions
- `internal/contexts/` — Context handling utilities
- `internal/pkg/` — Shared utilities (xerrors, xjson, xcache, xfile, xcontext, etc.)
- `internal/scopes/` — Permission system with role-based access control
- `llm/` — LLM utilities, transformers, and pipeline processing (separate Go module)
- `llm/pipeline/` — Pipeline processing architecture
- `conf/conf.go` — Configuration loading and validation

## Go Modules

- The repository root (`/`) is the main Go module: `github.com/looplj/axonhub`.
- `llm/` is a separate Go module: `github.com/looplj/axonhub/llm`.

### `llm/` Module Notes

- `llm/` is an independent module. Always run Go commands from the `llm/` directory (e.g., `cd llm && go test ./...`).
- Running `go test ./llm/...` from repo root will fail with module boundary errors.

## Frontend Structure

- `frontend/src/routes/` — TanStack Router file-based routing
- `frontend/src/gql/` — GraphQL API communication
- `frontend/src/features/` — Feature-based component organization
- `frontend/src/components/` — Reusable shared components
- `frontend/src/hooks/` — Custom shared hooks
- `frontend/src/stores/` — Zustand state management
- `frontend/src/locales/` — i18n support (en.json, zh.json)
- `frontend/src/lib/` — Core utilities (API client, i18n, permissions, utils)
- `frontend/src/utils/` — Domain-specific utilities (date, format, error handling)
- `frontend/src/config/` — App configuration
- `frontend/src/context/` — React context providers

## Rules Index

All detailed rules are in `.agent/rules/`:

| File | Scope | Description |
|------|-------|-------------|
| [go-general.md](.agent/rules/go-general.md) | `**/*.go` | Go 通用约定、错误处理、依赖注入、开发命令约束 |
| [ent-graphql.md](.agent/rules/ent-graphql.md) | `internal/ent/schema/**/*.go`, `internal/server/gql/**/*.go`, `internal/server/gql/**/*.graphql`, `gqlgen.yml` | Ent、GraphQL、代码生成、schema 变更规则 |
| [database-indexes.md](.agent/rules/database-indexes.md) | `internal/ent/schema/**/*.go` | 数据库索引设计、命名、跨方言兼容与迁移验证规则 |
| [biz-services.md](.agent/rules/biz-services.md) | `internal/server/biz/**/*.go` | Biz service、上下文取值、事务与级联删除规则 |
| [cache-compat.md](.agent/rules/cache-compat.md) | `**/*.go` | 缓存结构兼容性与升级安全规则 |
| [frontend-general.md](.agent/rules/frontend-general.md) | `frontend/**/*.ts`, `frontend/**/*.tsx` | 前端通用开发约定、GraphQL 数据约束、页面作用域 |
| [frontend-i18n.md](.agent/rules/frontend-i18n.md) | `frontend/src/**/*.ts`, `frontend/src/**/*.tsx`, `frontend/src/locales/*.json` | i18n 与货币格式规则 |
| [frontend-ui.md](.agent/rules/frontend-ui.md) | `frontend/**/*.tsx` | 已确认的增量界面风格、组件与弹窗交互规则 |
| [e2e.md](.agent/rules/e2e.md) | `frontend/tests/**/*.ts` | E2E testing rules |
| [docs.md](.agent/rules/docs.md) | `docs/**/*.md` | Documentation rules |
| [workflows/add-channel.md](.agent/rules/workflows/add-channel.md) | Manual | Workflow for adding a new channel |
