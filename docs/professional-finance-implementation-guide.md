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

在 `tdx-api` 中把现有零散的 `gpcw` 专业财务能力升级为一组正式的 `/api/prof-finance/*` 接口，并且**字段目录完整覆盖 `FINVALUE / FINONE` 的全部 `403` 个专业财务字段定义**。

---

## Non-Negotiable Contract

coding agent 必须遵守以下规则，不能自行发明新语义：

- 查询接口只接受：
  - `full_code`
  - `full_codes`
  - `field_codes`
- 查询接口只返回：
  - `field_values`
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

---

## Scope

### In scope

- 新增 `/api/prof-finance/*` API family
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

---

## Required Deliverables

coding agent 最终必须交付以下结果：

1. 字段注册层
2. `/api/prof-finance/fields`
3. `/api/prof-finance/history`
4. `/api/prof-finance/snapshot`
5. `/api/prof-finance/cross-section`
6. `/api/prof-finance/coverage`
7. 对应单元测试和 handler 测试
8. 更新 API 文档

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
  - `/api/prof-finance/*` handlers
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
- `supported` 只表示该字段当前是否进入查询接口 contract，不表示该字段是否存在 `field_code`

可选但推荐：

- `notes`
- `aliases`
- `display_precision`
- `is_derived`

### 2. Full field coverage

字段注册层必须覆盖 baseline 中全部 `403` 个 `source_field_id` 定义。

这里的“覆盖”分两层：

- **目录覆盖**
  - 全部 `403` 个字段在 `/api/prof-finance/fields` 可见
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

---

## API-by-API Build Plan

### Step 1: Build `/api/prof-finance/fields`

这是第一优先级。没有字段目录，后续接口都会漂。

必须实现：

- 按 `category` 过滤
- 按 `query` 搜索
- 返回全部字段元数据
- 明确 `supported`

必须验证：

- 总字段数 = `403`
- public category 数 = `17`
- `source_field_id` 唯一
- 不出现未注册的空洞字段

### Step 2: Build `/api/prof-finance/history`

这是最贴近现有 `/api/financial-reports` 的落点，先做这条最稳。

必须实现：

- `full_code`
- `field_codes`
- `as_of_date`
- `start_report_date`
- `end_report_date`
- `limit`
- `period`

必须保证：

- 响应中的 `field_values` key 只来自字段注册层
- 不允许直接把当前 `Snapshot` 的少数字段硬编码返回
- 每期返回 `source_report_file`

### Step 3: Build `/api/prof-finance/snapshot`

必须实现：

- `period_mode=latest_available|latest_report|exact`
- `report_date`
- `as_of_date`
- `knowledge_cutoff`
- `missing_fields`
- `coverage`

### Step 4: Build `/api/prof-finance/cross-section`

必须实现：

- `full_codes`
- `field_codes`
- `report_date`
- `as_of_date`
- `period_mode`
- `items[]` 中包含：
  - `full_code`
  - `name`
  - `field_values`
  - `missing_fields`
  - `coverage`

注意：

- 横截面接口不能只回代码，不回名称

### Step 5: Build `/api/prof-finance/coverage`

必须实现：

- `latest_report_date`
- `available_reports`
- `available_field_codes`
- `missing_fields`
- `status`

这条接口的目标是显式说明“为什么某字段/某报告期不可用”，而不是让调用方猜。

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
- `full_code -> 6位码` 归一化查询
- 多字段解析
- 报告期过滤
- 最近可得报告优先级

环境约束：

- Parser/service tests 涉及 `gpcw*.zip` 下载、缓存与解析时，如果测试生成了临时 zip、解压目录、缓存目录或其他下载产物，必须在测试结束后清理
- 优先使用 `t.TempDir()` 和 `t.Cleanup()` 管理测试期临时文件
- 不允许把测试生成的下载文件、解压文件、缓存文件残留在 workspace 或 repo 目录内

### C. Handler tests

建议新增：

- `web/prof_finance_handlers_test.go`

必须覆盖：

- `/api/prof-finance/fields`
- `/api/prof-finance/history`
- `/api/prof-finance/snapshot`
- `/api/prof-finance/cross-section`
- `/api/prof-finance/coverage`

必须断言：

- 参数名与 PRD 完全一致
- 不接受裸 `code`
- 返回里是 `field_values`，不是 `fields`
- 返回里是 `full_code`，不是 `security_code`
- `items[]` 有 `name`

环境约束：

- Handler tests 如果为了验证下载、解析或接口联动而生成临时 zip、txt、dat、缓存目录或 mock 下载文件，也必须在测试结束后清理
- 不允许把任何测试产物留在 workspace 内污染仓库状态

### D. Regression tests

必须保证不破坏：

- `/api/financial-reports`
- `/api/profile`
- 现有 `profinance` 相关测试

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

- [ ] 新增 `/api/prof-finance/*` 全家桶接口
- [ ] 字段目录完整覆盖 `403` 个 `source_field_id`
- [ ] PRD 命名被严格执行
- [ ] 不存在旧语义残留
- [ ] handler tests 全过
- [ ] parser/service tests 全过
- [ ] 现有回归测试不坏
- [ ] API 文档已更新

---

## Common Failure Modes

coding agent 最容易犯的错有这些，必须主动规避：

- 只把当前 `Snapshot` 里的 6 个字段扩成 10 个字段，就误以为做完了
- 只做 `/api/prof-finance/history`，不做字段目录
- 把 `source_field_id` 直接暴露成查询参数
- 继续接受裸 6 位 `code`
- 返回结构里写回 `fields`
- 横截面只返回 `full_code`，不返回 `name`
- 把 `FINVALUE` 和 `FINONE` 当成两套字段定义
- 把 `GPJYVALUE/BKJYVALUE/...` 混进专业财务字段范围
- 用 `0` 代表无数据

---

## Suggested Execution Order

coding agent 建议按这个顺序做：

1. 先建字段注册层
2. 跑 registry tests
3. 扩展 `profinance` 查询层
4. 实现 `/api/prof-finance/fields`
5. 实现 `/api/prof-finance/history`
6. 实现 `/api/prof-finance/snapshot`
7. 实现 `/api/prof-finance/cross-section`
8. 实现 `/api/prof-finance/coverage`
9. 跑全部测试
10. 更新 API 文档

不要反过来先写 handler 再补 registry。

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
- 新增 /api/prof-finance/fields
- 新增 /api/prof-finance/history
- 新增 /api/prof-finance/snapshot
- 新增 /api/prof-finance/cross-section
- 新增 /api/prof-finance/coverage
- 字段目录完整覆盖 baseline 中全部 403 个 source_field_id
- 查询接口严格使用 full_code/full_codes、field_codes、field_values
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
