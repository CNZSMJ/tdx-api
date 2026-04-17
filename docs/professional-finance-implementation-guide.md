# Professional Finance API Implementation Guide

## Purpose

这份文档是给 coding agent 的**执行蓝图**，目标不是再解释产品设计，而是把开发和测试工作拆成可以直接执行的工程任务。

本指南配套两份上游文档：

- [professional-finance-api-prd.md](/Users/huangjiahao/workspace/industry-investment-suite/repos/tdx-api/docs/professional-finance-api-prd.md)
- [professional-finance-field-catalog-baseline.md](/Users/huangjiahao/workspace/industry-investment-suite/repos/tdx-api/docs/professional-finance-field-catalog-baseline.md)

职责边界：

- `PRD`：定义外部 API contract
- `Field Catalog Baseline`：定义完整字段宇宙
- `Implementation Guide`：定义 coding agent 的实现路径、文件落点、测试要求和完成标准

---

## One-Sentence Goal

在 `tdx-api` 中把现有零散的 `gpcw` 专业财务能力升级为一组正式的 `/api/v1/prof-finance/*` 接口，并且**字段目录完整覆盖 `FINVALUE / FINONE` 的全部 `403` 个专业财务字段定义**。

---

## Non-Negotiable Contract

coding agent 必须遵守以下规则，不能自行发明新语义：

- 查询接口只接受：
  - `full_code`
  - `full_codes`
  - `field_codes`
- 新接口统一挂在：
  - `/api/v1/prof-finance/*`
- 查询接口只返回：
  - `field_values`
- 所有响应必须包含：
  - `request_id`
- 字段目录接口必须暴露：
  - `field_code`
  - `source_field_id`
  - `concept_code`
  - `field_name_cn`
  - `field_name_en`
  - `category`
  - `statement`
  - `period_semantics`
  - `unit`
  - `value_type`
  - `storage_precision`
  - `display_precision`
  - `rounding_mode`
  - `nullable`
  - `source`
  - `supported`
- 不允许在新接口里重新引入这些旧概念：
  - `security_code`
  - `security_full_code`
  - `view`
  - `raw_fields`
  - `standardized_fields`
  - `field_metadata`
  - `field_ids`
  - `raw_field_ids`
- 不接受裸 6 位代码作为新接口的正式 contract
- 不允许把缺失值伪装成 `0`
- 不允许把未映射字段直接静默丢掉
- 不允许省略稳定错误代码体系
- 不允许让 `cross-section` 无分页地返回整个证券全集

---

## Scope

### In scope

- 新增 `/api/v1/prof-finance/*` API family
- 新建完整字段注册层
- 完整覆盖 `403` 个专业财务 `source_field_id`
- 保留现有 `/api/financial-reports`，但不继续把它当成最终产品形态扩展
- 让 `/api/profile` 后续可复用统一专业财务层

### Out of scope

- 不改造 `/api/finance` 的 raw TDX contract
- 不在本任务里合并 `raw finance` 和 `professional finance`
- 不接入 `GPJYVALUE`、`BKJYVALUE`、`SCJYVALUE`、`GPONEDAT`
- 不在本任务里发明第二套公共字段视图
- 不做向后兼容别名或双写逻辑

---

## Current Code Touchpoints

coding agent 不要盲目找位置，当前主落点已经明确：

### Existing professional finance source

- [profinance/service.go](/Users/huangjiahao/workspace/industry-investment-suite/repos/tdx-api/profinance/service.go)
- [profinance/service_test.go](/Users/huangjiahao/workspace/industry-investment-suite/repos/tdx-api/profinance/service_test.go)

现状：

- 已能读取 `gpcw.txt`
- 已能下载 `gpcw*.zip`
- 已能解析少量字段
- 当前 `Snapshot` 结构只包含很小的字段子集

### Existing web integration

- [web/server.go](/Users/huangjiahao/workspace/industry-investment-suite/repos/tdx-api/web/server.go)
- [web/server_api_extended.go](/Users/huangjiahao/workspace/industry-investment-suite/repos/tdx-api/web/server_api_extended.go)
- [web/profile_snapshot_test.go](/Users/huangjiahao/workspace/industry-investment-suite/repos/tdx-api/web/profile_snapshot_test.go)

现状：

- `/api/financial-reports` 已存在，但只暴露少量字段
- `/api/profile` 会消费 `profinance.Snapshot`

### Documentation source of truth

- [professional-finance-api-prd.md](/Users/huangjiahao/workspace/industry-investment-suite/repos/tdx-api/docs/professional-finance-api-prd.md)
- [professional-finance-field-catalog-baseline.md](/Users/huangjiahao/workspace/industry-investment-suite/repos/tdx-api/docs/professional-finance-field-catalog-baseline.md)
- [professional-finance-data-architecture.md](/Users/huangjiahao/workspace/industry-investment-suite/repos/tdx-api/docs/professional-finance-data-architecture.md)

约束：

- `PRD` 只定义最终对外接口 contract，不承载内部 Sprint 排期、落库细节或阶段性妥协语义
- 内部 Sprint 拆解只写在本指南
- Sprint 可以拆小，但每个 Sprint 应尽量形成一段可独立验证的完整能力闭环

---

## Required Deliverables

coding agent 最终必须交付以下结果：

1. 字段注册层
2. `/api/v1/prof-finance/fields`
3. `/api/v1/prof-finance/history`
4. `/api/v1/prof-finance/snapshot`
5. `/api/v1/prof-finance/cross-section`
6. `/api/v1/prof-finance/coverage`
7. 统一错误 envelope 与错误代码体系
8. `cross-section` cursor 分页
9. 对应单元测试和 handler 测试
10. 更新 API 文档

如果只完成其中一部分，不算完成。

---

## Recommended File Plan

下面是建议落点。agent 可以微调，但不要把逻辑继续散落在已有零散文件里。

### `profinance/`

建议新增：

- `registry.go`
  - 字段注册表
- `types.go`
  - 新的通用专业财务数据模型
- `mapping.go`
  - `source_field_id -> field_code` 映射
- `query.go`
  - snapshot/history/cross-section 的查询协调
- `coverage.go`
  - 覆盖状态与缺失状态逻辑

可保留并扩展：

- `service.go`
  - 继续负责下载、缓存、解析 `gpcw`

### `web/`

建议新增单独文件，不要把实现硬塞回超大文件：

- `prof_finance_handlers.go`
  - `/api/v1/prof-finance/*` handlers
- `prof_finance_handlers_test.go`
  - handler tests

现有文件只做最小接线：

- `server.go`
  - 注册新路由
- `server_api_extended.go`
  - 如果需要，让 `/api/profile` 逐步消费新的统一层

### `docs/`

需要同步更新：

- `API_接口文档.md`
- 如有必要，`README.md`

---

## Data Model Requirements

### 1. Field registry

必须存在一个统一字段注册层，每个字段至少包含：

- `field_code`
- `source_field_id`
- `concept_code`
- `field_name_cn`
- `field_name_en`
- `category`
- `statement`
- `period_semantics`
- `unit`
- `value_type`
- `storage_precision`
- `display_precision`
- `rounding_mode`
- `nullable`
- `source`
- `supported`

约束：

- `field_code` 对字段目录中的每一条注册记录都必填
- `field_code` 是字段目录、映射追踪和覆盖状态表达使用的稳定标识
- `field_code` 在 registry 中必须全局唯一，不能因为 source section 重复或近似同义而复用
- 如果多个 source field 属于同一经济概念族，必须通过 `period_semantics` 和可选的 `concept_code` 来归组，而不是复用同一个 `field_code`
- 基础 per-share / primary statement 字段优先使用 canonical 无后缀 `field_code`
- analysis / flash / preview / extended-statement 变体优先使用 category-style 后缀
- 若同 category 内仍需区分，使用 `<base>_<qualifier>_<category>` 命名
- `category` 在大多数情况下表达经济含义分组；但对于业绩快报、业绩预告字段，`category` 优先表达信息源分组，分别归入 `earnings_flash_report`、`earnings_preview`
- `statement` 与 `category` 冲突时，不允许让实现者自行猜测：
  - `statement` 固定表达会计报表/披露来源属性
  - `category` 固定表达对外字段目录的浏览/过滤分组
- `supported` 只表示该字段当前是否进入查询接口 contract，不表示该字段是否存在 `field_code`

可选但推荐：

- `notes`
- `aliases`
- `is_derived`

### 1.1 Precision and nullability rules

字段注册层必须显式补齐以下精度元数据：

- `storage_precision`
- `display_precision`
- `rounding_mode`
- `nullable`

约束：

- `storage_precision` 服务于标准化存储和计算，不得只给展示精度
- `display_precision` 服务于 API 展示和下游渲染
- `rounding_mode` 必须来自 baseline 受控集合
- `nullable` 必须反映字段设计是否允许缺失，而不是随意推断

### 2. Full field coverage

字段注册层必须覆盖 baseline 中全部 `403` 个 `source_field_id` 定义。

这里的“覆盖”分两层：

- **目录覆盖**
  - 全部 `403` 个字段在 `/api/v1/prof-finance/fields` 可见
- **查询覆盖**
  - `supported=true` 的字段可被查询接口读取
  - `supported=false` 的字段仍保留稳定 `field_code`，但不能作为查询接口的可用字段返回

### 3. FINVALUE vs FINONE

这两者共用同一套字段编号，但查询语义不同：

- `FINVALUE`
  - 适合历史序列、最新可用报告
- `FINONE`
  - 适合指定日期或点位语义

实现要求：

- 字段注册层不要为 `FINVALUE` 和 `FINONE` 各建一套字段定义
- 查询层再决定访问语义

### 4. Error model and request envelope

所有 `/api/v1/prof-finance/*` handlers 必须实现统一响应 envelope：

- success
  - `code=0`
  - `message=success`
  - `request_id`
  - `data`
- error
  - `code != 0`
  - `message`
  - `request_id`
  - `error.error_code`
  - `error.error_type`
  - `error.http_status`
  - `error.retryable`
  - `error.details`

最小错误代码集合必须包括：

- `INVALID_ARGUMENT`
- `NOT_FOUND`
- `UNSUPPORTED_FIELD`
- `UNSUPPORTED_PERIOD_MODE`
- `SOURCE_NOT_READY`
- `RATE_LIMITED`
- `INTERNAL_ERROR`

### 5. Cross-section pagination

`/api/v1/prof-finance/cross-section` 必须支持 cursor 分页。

最小要求：

- `limit`
- `cursor`
- `next_cursor`

约束：

- 默认 `limit=100`
- 最大 `limit=500`
- `full_codes` 单次请求最大输入数量也为 `500`
- 分页顺序必须稳定
- 若未显式指定排序，按 `full_code ASC` 形成稳定游标

---

## Data-Layer Non-Negotiables

coding agent 在实现前必须先接受以下最终架构前提，不能为了图快回退到“实时扫 ZIP 当主查询路径”的做法：

- raw artifact layer
  - 保留 `gpcw.txt` 与全量 `gpcw*.zip`
- parsed raw layer
  - 保留 `source_field_id` 级别的可追溯事实
- serving layer
  - 采用“单证券单报告版本一行”的 payload 设计
  - `field_values` 以对象整体存储
  - 不让 EAV 事实表承担主查询层
- visibility layer
  - 必须落地：
    - `announce_date_raw`
    - `effective_announce_date`
    - `announce_date_source`
    - `first_seen_at`
  - `source watermark`
  - `knowledge_cutoff`
- restatement
  - 不允许仅按 `(full_code, report_date)` 覆盖旧版本后丢失历史
- schema evolution
  - 必须有显式 migration 机制
- observability
  - 必须有 source freshness、parse failure、query latency 和 watermark lag 的可观测性

如果数据层还没搭好，就不要提前把最终查询接口挂出来。

### Serialization and SQLite Guardrails

以下两条属于实现级硬约束：

- `field_values` 序列化
  - `prof_finance_report_payload.field_values` 必须只写入**真实存在且有效**的字段
  - 不允许因为 Go 的零值序列化，把源数据中不存在的字段写成 `0`、`0.0`、空字符串或默认布尔值
  - 推荐实现方式：
    - 优先使用 `map[string]interface{}`
    - 仅在字段真实存在、通过合法性校验、且确定进入当前 payload 时才写入 key
  - 如果必须使用 struct，则必须使用 pointer fields，并避免把零值误当成存在值

- SQLite JSON 支持
  - serving 层默认使用 JSON payload
  - 在实现依赖 `json_extract`、`json_each`、`json_valid` 等 SQLite JSON 函数前，必须先验证当前运行环境已启用 JSON 扩展能力
  - 对 `github.com/mattn/go-sqlite3`，应显式验证运行时可执行：
    - `SELECT json_valid('{}')`
  - 不允许假设部署环境一定具备 JSON 查询能力而不做校验

---

## Sprint Build Plan

本项目只存在一套最终 contract。  
所有 Sprint 都是在实现这同一套最终 contract，只是按依赖顺序逐层交付。  
拆分原则是：在保证依赖顺序正确的前提下，尽量让每个 Sprint 对应一段可独立验证的完整能力。

### Sprint 1: Field Registry and `/api/v1/prof-finance/fields`

这是第一优先级。没有字段目录，后续查询接口都会漂。

必须完成：

- 完整字段注册层
- `403` 个 `source_field_id`
- `17` 个 public category
- `/api/v1/prof-finance/fields`
- 全量元数据：
  - `field_code`
  - `source_field_id`
  - `concept_code`
  - `field_name_cn`
  - `field_name_en`
  - `category`
  - `statement`
  - `period_semantics`
  - `unit`
  - `value_type`
  - `storage_precision`
  - `display_precision`
  - `rounding_mode`
  - `nullable`
  - `source`
  - `supported`
- 统一错误 envelope
- `request_id`

完成定义：

- 调用方已能稳定发现字段
- 不存在未注册空洞

### Sprint 2: Artifact Ingestion and Raw Facts

必须完成：

- 原始 ZIP 预抓与每日增量同步
- `source_file` manifest
- `source_report`
- `source_value_raw`
- 逐证券报告头：
  - `report_date`
  - `announce_date_raw`
  - `first_seen_at`
  - `source_file_id`
- 公告日期字段接入：
  - `314`
  - `313`
  - `315`
- 日期归一化与合法性校验
- schema migration 基础设施
- source freshness / watermark / parse failure 指标打点

完成定义：

- 原始数据层可重放、可追溯
- `announce_date_raw` 已进入结构化存储

### Sprint 3: Serving Report Payload and `/api/v1/prof-finance/history`

必须完成：

- `report_version` 设计
- `report_payload` 设计
- `field_values` JSON payload
- `effective_announce_date`
- `announce_date_source`
- `/api/v1/prof-finance/history`

必须保证：

- 不直接从当前 `Snapshot` 结构硬编码少数字段返回
- history 已走 serving layer
- 每期可返回：
  - `report_date`
  - `announce_date`
  - `field_values`
  - `missing_fields`
  - `source_report_file`

完成定义：

- 单证券多期历史查询完整可用

### Sprint 4: `/api/v1/prof-finance/snapshot` and `/api/v1/prof-finance/coverage`

必须完成：

- `/api/v1/prof-finance/snapshot`
- `/api/v1/prof-finance/coverage`
- `knowledge_cutoff`
- `coverage` 字段宇宙判定
- `status` / `status_reason`

必须保证：

- `coverage` 接受：
  - `full_code`
  - `report_date`
  - `field_codes`
  - `as_of_date`
- snapshot 与 coverage 使用同一套 visibility 规则

完成定义：

- 单证券当前可见视角查询完整可用
- 调用方无需猜测缺失原因

### Sprint 5: `/api/v1/prof-finance/cross-section` and Performance

必须完成：

- `/api/v1/prof-finance/cross-section`
- 批量查询路径
- 排序、过滤与分页策略
- 针对 JSON payload 的横截面读取实现
- `cross-section` 页级 SLA 验证

必须保证：

- `items[]` 中逐证券返回：
  - `full_code`
  - `name`
  - `report_date`
  - `announce_date`
  - `field_values`
  - `missing_fields`
  - `coverage`

完成定义：

- 多证券同报告期横截面查询完整可用

### Sprint 6: Restatement and Strict FINONE

必须完成：

- 重述版本保留策略
- `latest_report`
- `exact`
- `latest_available`
- source watermark
- 严格 `knowledge_cutoff`

必须保证：

- 不因为 serving 覆盖而丢失历史版本
- `latest_available` 真正基于 visibility 语义，而不是文件名猜测

完成定义：

- strict point-in-time 语义闭环

### Sprint 7: Integration, Rebuild, and Operations

必须完成：

- `/api/profile` 接入统一 serving layer
- raw -> serving rebuild 流程
- 对账与回归测试
- 文档与示例收口
- observability dashboard / metrics checklist
- migration replay / rebuild rehearsal

完成定义：

- 数据层、接口层、轻量集成层全部一致
 
---

## Public Field Mapping Strategy

完整覆盖所有 `source_field_id`，不等于每个字段都必须立刻进入查询接口。

允许两层状态：

- `supported=true`
  - 已有稳定 `field_code`
  - 可进入查询接口
- `supported=false`
  - 已知原始字段定义
  - 已有稳定 `field_code`
  - 已在目录中暴露
  - 暂不进入查询接口 contract

但以下做法不允许：

- 在目录里完全漏掉字段
- 给同一经济含义的不同时间语义字段强行复用一个模糊 `field_code`
- 给 source-section 重复项继续复用同一个 `field_code`
- 不写 `period_semantics`

特别要注意多口径字段，例如：

- 营业收入：
  - `74`
  - `230`
  - `283`
  - `312`
  - `319`
- 净利润：
  - `95`
  - `96`
  - `232`
  - `276`
  - `287`
- ROE：
  - `6`
  - `197`
  - `281`
  - `292`
  - `293`

这些不能偷懒合并。

对于 source-section 重复但又需要保留 source coverage 的字段，采用以下规则：

- 选择一个 canonical `field_code` 进入查询 contract
- 其他重复来源保留独立、唯一的 `field_code`
- 如果重复来源来自基础 per-share / primary statement section，则基础字段占用 canonical 无后缀 `field_code`
- analysis / flash / preview / extended-statement 重复来源的 `field_code` 优先使用 category-style 后缀
- 若同 category 内仍需区分，再使用 `<base>_<qualifier>_<category>` 命名
- 重复来源默认 `supported=false`

当前 baseline 中应按这个原则处理的典型例子：

- `6 -> roe`
- `197 -> roe_profitability`
- `7 -> operating_cash_flow_per_share`
- `219 -> operating_cash_flow_per_share_cash_flow_analysis`
- `59 -> provisions`
- `423 -> provisions_extended_balance_sheet`

---

## Testing Plan

### A. Registry tests

建议新增：

- `profinance/registry_test.go`

必须覆盖：

- 字段总数为 `403`
- public category 总数为 `17`
- 所有 `source_field_id` 唯一
- 关键字段存在：
  - `4`
  - `40`
  - `74`
  - `107`
  - `238`
  - `242`
  - `276`
  - `281`
  - `283`
  - `304`
  - `319`
  - `501`
  - `580`

### B. Parser/service tests

扩展：

- [profinance/service_test.go](/Users/huangjiahao/workspace/industry-investment-suite/repos/tdx-api/profinance/service_test.go)

必须覆盖：

- `gpcw.txt` 列表更新
- `gpcw*.zip` 下载和缓存复用
- source file manifest 更新
- `full_code -> 6位码` 归一化查询
- 多字段解析
- 报告期过滤
- 最近可得报告优先级
- `announce_date_raw`
- `effective_announce_date`
- `announce_date_source`
- raw layer 重建 serving layer 的可行性

环境约束：

- Parser/service tests 涉及 `gpcw*.zip` 下载、缓存与解析时，如果测试生成了临时 zip、解压目录、缓存目录或其他下载产物，必须在测试结束后清理
- 优先使用 `t.TempDir()` 和 `t.Cleanup()` 管理测试期临时文件
- 不允许把测试生成的下载文件、解压文件、缓存文件残留在 workspace 或 repo 目录内

### C. Handler tests

建议新增：

- `web/prof_finance_handlers_test.go`

必须覆盖：

- `/api/v1/prof-finance/fields`
- `/api/v1/prof-finance/history`
- `/api/v1/prof-finance/snapshot`
- `/api/v1/prof-finance/cross-section`
- `/api/v1/prof-finance/coverage`

必须断言：

- 参数名与 PRD 完全一致
- 版本路径必须是 `/api/v1/prof-finance/*`
- 不接受裸 `code`
- 返回里是 `field_values`，不是 `fields`
- 返回里是 `full_code`，不是 `security_code`
- 成功响应必须包含 `request_id`
- 错误响应必须包含稳定 `error_code`
- `items[]` 有 `name`
- `items[]` 有 `report_date`
- `items[]` 有 `announce_date`
- `items[]` 有 `coverage`
- `snapshot/history` 响应有 `knowledge_cutoff`
- `coverage` 响应支持 `field_codes` 与 `as_of_date`
- `cross-section` 响应支持 `limit`、`cursor`、`next_cursor`
- `full_codes` 超过 `500` 时必须返回 `INVALID_ARGUMENT`

环境约束：

- Handler tests 如果为了验证下载、解析或接口联动而生成临时 zip、txt、dat、缓存目录或 mock 下载文件，也必须在测试结束后清理
- 不允许把任何测试产物留在 workspace 内污染仓库状态

### D. Regression tests

必须保证不破坏：

- `/api/financial-reports`
- `/api/profile`
- 现有 `profinance` 相关测试

### E. SLA and observability tests

必须补充：

- `cross-section` 单页 `limit=500` 的性能基准测试
- `snapshot/history/coverage` 热路径延迟断言或基准报告
- `source watermark`、`parse failure`、`query latency` 指标存在性校验
- migration smoke test
  - 空库迁移
  - 已有库迁移
  - rebuild 后查询一致性

---

## Required Validation Commands

coding agent 完成实现后，至少要跑：

```bash
cd /Users/huangjiahao/workspace/industry-investment-suite/repos/tdx-api
go test ./profinance/...
cd /Users/huangjiahao/workspace/industry-investment-suite/repos/tdx-api/web
TDX_WEB_SKIP_INIT=1 go test ./...
```

如果新增了更多包测试，也要一起跑。

如果任何测试不能跑，必须在最终说明中明确指出原因，不能跳过不报。

## Test Artifact Cleanup Rule

由于该模块涉及专业财务包下载（ZIP）与解析（TXT / DAT 读取），coding agent 在运行 parser/service tests、handler tests 或任何本地验证时，必须额外遵守下面这条规则：

- 若验证过程中生成了下装临时文件、测试 zip、解压目录、缓存目录、mock 响应文件或其他测试产物，必须在验证结束后清理环境
- 推荐使用 `t.TempDir()`、`t.Cleanup()` 或等价机制保证自动回收
- 绝不能将测试产物残留在 workspace 内，避免仓库被污染
- 最终交付前，`git status --short` 不应因为测试产物而出现额外脏文件

---

## Completion Checklist

只有同时满足下面这些条件，才算完成：

- [ ] 新增 `/api/v1/prof-finance/*` 全家桶接口
- [ ] 所有新接口挂在 `/api/v1/prof-finance/*`
- [ ] 字段目录完整覆盖 `403` 个 `source_field_id`
- [ ] raw artifact layer、raw facts、serving layer、visibility layer 全部落地
- [ ] serving 查询主路径不再依赖实时解 ZIP
- [ ] PRD 命名被严格执行
- [ ] 不存在旧语义残留
- [ ] strict `latest_available` 与 `knowledge_cutoff` 语义闭环
- [ ] 财务重述版本策略已落地
- [ ] 稳定错误代码体系已落地
- [ ] `cross-section` cursor 分页已落地
- [ ] 字段精度/舍入/可空性元数据已落地
- [ ] 性能 SLA 已验证
- [ ] observability 最小集合已落地
- [ ] schema migration 机制已落地
- [ ] handler tests 全过
- [ ] parser/service tests 全过
- [ ] 现有回归测试不坏
- [ ] API 文档已更新

---

## Common Failure Modes

coding agent 最容易犯的错有这些，必须主动规避：

- 只把当前 `Snapshot` 里的 6 个字段扩成 10 个字段，就误以为做完了
- 只做 `/api/v1/prof-finance/history`，不做字段目录
- 把 `source_field_id` 直接暴露成查询参数
- 继续接受裸 6 位 `code`
- 返回结构里写回 `fields`
- 横截面只返回 `full_code`，不返回 `name`
- 横截面不做分页，直接尝试返回全部证券
- 省略 `request_id` 和错误代码体系
- 把 `FINVALUE` 和 `FINONE` 当成两套字段定义
- 把 `GPJYVALUE/BKJYVALUE/...` 混进专业财务字段范围
- 用 `0` 代表无数据
- 让 EAV 表直接承担主 serving 查询路径
- 遇到财务重述时直接覆盖旧版本
- 把 `first_seen_at` 冒充成原始 `announce_date`
- 字段目录没有 `storage_precision / display_precision / rounding_mode / nullable`

---

## Suggested Execution Order

coding agent 建议严格按 Sprint 顺序推进：

1. Sprint 1：字段注册层和 `/api/v1/prof-finance/fields`
2. Sprint 2：artifact/manifest/raw facts/report headers
3. Sprint 3：serving payload 层和 `/api/v1/prof-finance/history`
4. Sprint 4：`/api/v1/prof-finance/snapshot` 与 `/api/v1/prof-finance/coverage`
5. Sprint 5：`/api/v1/prof-finance/cross-section` 与性能校验
6. Sprint 6：restatement、strict `latest_available`、`knowledge_cutoff`
7. Sprint 7：`/api/profile` 集成、重建、回归、文档收口

不要反过来先写 handler 再补 registry，也不要先暴露最终查询接口、再事后补数据层。

---

## Handoff Prompt Template

如果需要把这项工作直接交给 coding agent，可使用下面这段：

```text
请在 /Users/huangjiahao/workspace/industry-investment-suite/repos/tdx-api 内实现 professional finance API。

你必须同时遵守以下三份文档：
1. /Users/huangjiahao/workspace/industry-investment-suite/repos/tdx-api/docs/professional-finance-api-prd.md
2. /Users/huangjiahao/workspace/industry-investment-suite/repos/tdx-api/docs/professional-finance-field-catalog-baseline.md
3. /Users/huangjiahao/workspace/industry-investment-suite/repos/tdx-api/docs/professional-finance-implementation-guide.md

要求：
- 新增 /api/v1/prof-finance/fields
- 新增 /api/v1/prof-finance/history
- 新增 /api/v1/prof-finance/snapshot
- 新增 /api/v1/prof-finance/cross-section
- 新增 /api/v1/prof-finance/coverage
- 所有接口挂在 /api/v1/prof-finance/*
- 字段目录完整覆盖 baseline 中全部 403 个 source_field_id
- 查询接口严格使用 full_code/full_codes、field_codes、field_values
- 实现稳定错误代码体系和 cursor 分页
- 不允许引入旧语义名
- 增加完整测试并运行通过

完成后请说明：
- 新增/修改了哪些文件
- 哪些测试已通过
- 是否还有未标准化但已在字段目录中标记 supported=false 的字段
```

---

## Final Standard

这项工作完成时，coding agent 交付的不能是“多加了一些字段”，而必须是：

**一个有完整字段目录、完整查询 contract、完整测试门槛的 professional finance data product foundation。**
