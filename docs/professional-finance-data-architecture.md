# Professional Finance Data Architecture

**Status**: Proposed  
**Repo**: `tdx-api`  
**Scope**: Professional finance data only (`gpcw`, `FINVALUE`, `FINONE`)  
**Last Updated**: 2026-04-18

---

## Purpose

这份文档只回答 **专业财务数据层应该如何设计**。

它不负责：

- 定义对外接口参数和响应 shape
- 定义字段中文名/英文名
- 定义 coding agent 的逐 Sprint 执行步骤

它负责：

- 定义专业财务数据真正的 source of truth
- 定义原始 ZIP、原始解析值、标准化查询层、可见性层之间的边界
- 定义 `FINVALUE` 与 `FINONE` 在数据层的落地方式
- 定义重述、公告日 fallback、`knowledge_cutoff`、查询性能的最终方案
- 定义 schema migration 与 observability 的最终原则

本文件与另外三份文档的边界如下：

- [professional-finance-api-prd.md](/Users/huangjiahao/workspace/industry-investment-suite/repos/tdx-api/docs/professional-finance-api-prd.md)
  - 对外接口 contract
- [professional-finance-field-catalog-baseline.md](/Users/huangjiahao/workspace/industry-investment-suite/repos/tdx-api/docs/professional-finance-field-catalog-baseline.md)
  - 字段宇宙与字段命名基线
- [professional-finance-implementation-guide.md](/Users/huangjiahao/workspace/industry-investment-suite/repos/tdx-api/docs/professional-finance-implementation-guide.md)
  - 内部实现顺序、测试要求、Sprint 拆解
- **本文件**
  - 最终数据层架构

---

## Executive Summary

专业财务 API 的最终查询层 **不能长期直接读 `gpcw*.zip`**。  
正确的最终形态是：

1. 原始 `gpcw.txt` 与 `gpcw*.zip` 作为 **原始事实证据层**
2. ZIP 内 DAT 解析结果作为 **原始事实层**
3. 面向 API 的单证券单报告版本 JSON payload 作为 **正式 serving 层**
4. `announce_date_raw`、`effective_announce_date`、`first_seen_at`、`source watermark` 组成 **可见性层**

因此：

- **原始财务事实的 source of truth**：`gpcw*.zip`
- **对外 API 查询的 source of truth**：规范化数据库 serving 层

这套架构必须一次性按最终形态设计，不采用分裂式版本语义。  
内部可以按 Sprint 逐步施工，但最终 contract 与最终数据层定义只有一套。

---

## Goals

- 完整承接 `FINVALUE / FINONE` 的 `403` 个 `source_field_id`
- 支撑 `/api/v1/prof-finance/fields|history|snapshot|cross-section|coverage`
- 保证横截面查询在 SQLite 下仍可接受
- 保证 `FINONE` 的点位语义可落地，而不是“按文件名猜”
- 支撑财务重述（restatement）而不丢失历史版本
- 支撑重建、审计、追溯、覆盖率和缺失字段计算
- 支撑可观测、可迁移、可持续演进的数据平台能力

## Non-Goals

- 不改造 `/api/finance` 的 raw TDX contract
- 不把 `GPJYVALUE`、`BKJYVALUE`、`SCJYVALUE`、`GPONEDAT` 混入本设计
- 不在这里设计 UI、MCP contract 或回测策略接口
- 不强依赖分布式 OLAP 或外部数仓

---

## Current State

当前 `profinance` 真实行为是：

- 拉取 `gpcw.txt`
- 下载 `gpcwYYYYMMDD.zip`
- 将 ZIP 缓存到本地目录
- 查询时读取 ZIP，在内存中解压 DAT 并解析字段
- 当前只解析少量字段

现状优点：

- 简单
- 易验证
- 适合探索字段语义

现状缺点：

- 横截面查询会反复扫描大量字段
- 当前查询层没有稳定的结构化 serving source of truth
- `coverage`、`missing_fields`、字段单位与时间语义难闭环
- `FINONE`、`latest_available`、`knowledge_cutoff` 无法严谨落地

---

## Architecture Principles

### 1. Raw artifacts are immutable evidence

`gpcw.txt` 与 `gpcw*.zip` 不是临时缓存，而是原始证据。必须能追溯：

- 从哪个远端清单发现
- 哪个文件名
- 哪个 hash
- 何时下载
- 是否校验通过
- 是否发生过同名修订

### 2. ZIP is the raw truth, not the serving truth

`gpcw*.zip` 是原始事实的 truth，但不是正式查询层的 truth。  
正式 API 应该读数据库 serving 层，而不是在 handler 里长期直接扫 ZIP。

### 3. Raw facts and serving facts must be separated

必须区分：

- `source_field_id` 对应的原始解析值
- `field_code` 对应的对外公共字段值

否则后续字段映射、单位归一化、口径调整会污染原始事实。

### 4. Raw ingestion should be append-only

原始文件、原始解析值、首次发现时间应采用追加式记录，避免直接覆盖。

### 5. Serving must be optimized for query shape

专业财务主查询包括：

- 单证券多期历史
- 单证券单期快照
- 多证券同报告期横截面
- 覆盖状态查询

SQLite 下，不应让 EAV 窄表承担这类主查询路径。  
正式 serving 层应采用 **单证券单报告版本一行** 的 payload 设计。

### 6. Time semantics must be stored, not guessed

以下时间语义必须有明确存储或规则：

- `report_date`
- `announce_date_raw`
- `effective_announce_date`
- `first_seen_at`
- `ingested_at`
- `source watermark`
- `knowledge_cutoff`

### 7. Restatement history must not be silently lost

财务重述必须保留历史版本。  
不允许仅按 `(full_code, report_date)` 覆盖旧值后让 point-in-time 消失。

### 8. Schema evolution must be explicit

数据层演进必须依赖显式 schema migration。  
不允许把“删库重建”当成常规 schema 升级策略。

### 9. Observability is part of correctness

专业财务数据层不仅要能查，还要能证明：

- 当前同步到哪里
- 哪些文件解析失败
- 哪些查询超时
- 哪些报告因为 watermark 尚未可见

这些都必须进入最终数据架构，而不是事后临时补日志。

---

## Final Layered Architecture

### Layer 0: Remote Source

远端源：

- `https://data.tdx.com.cn/tdxfin/gpcw.txt`
- `https://data.tdx.com.cn/tdxfin/gpcwYYYYMMDD.zip`

职责：

- 提供原始清单
- 提供原始 ZIP
- 提供远端文件大小、hash、首次观察基础

### Layer 1: Raw Artifact Layer

本地保留：

- `gpcw.txt`
- `gpcw*.zip`

建议目录：

- `${TDX_DATA_DIR}/fundamentals/professional_finance/artifacts/gpcw.txt`
- `${TDX_DATA_DIR}/fundamentals/professional_finance/artifacts/zips/gpcwYYYYMMDD.zip`

职责：

- 保留证据
- 支撑重放
- 支撑重新解析
- 支撑文件级审计

### Layer 2: Parsed Raw Layer

把 DAT 解析成逐证券逐字段 raw facts：

- `full_code`
- `report_date`
- `source_field_id`
- `raw value`
- `source_file_id`

职责：

- 保留最接近源系统的结构化事实
- 为重新映射 `field_code` 提供稳定输入
- 为字段目录和覆盖率计算提供底层支撑

### Layer 3: Normalized Serving Layer

把 raw facts 映射成 API 查询层使用的公共字段载荷：

- 一个证券
- 一个报告期
- 一个报告版本
- 一行记录
- 一个 `field_values` 对象

职责：

- 支撑 `history`
- 支撑 `snapshot`
- 支撑 `cross-section`
- 支撑 `coverage`

### Layer 4: Visibility and Knowledge Layer

这层专门为 `FINONE` 提供时点语义。

职责：

- 存储 `announce_date_raw`
- 计算 `effective_announce_date`
- 记录 `announce_date_source`
- 记录 `first_seen_at`
- 记录 `source watermark`
- 计算 `knowledge_cutoff`
- 支撑 `latest_available`

---

## Storage Model

建议使用独立 SQLite 数据库：

- `${TDX_DATA_DIR}/fundamentals/professional_finance/prof_finance.db`

原始 ZIP 不存为 SQLite BLOB，仍以文件形式保留。  
SQLite 只存结构化元数据和查询层事实。

这样做的原因：

- ZIP 以文件形式更适合作为证据层
- SQLite 更适合结构化索引和查询
- 可以实现“保留原始文件 + 正式 DB 查询”双层模式

---

## Final Tables

以下是最终目标表，不要求一次性全部落地，但所有实现都应朝这套结构收敛。

### 1. `prof_finance_source_file`

记录每一个被发现和下载的源文件版本。

核心字段：

- `source_file_id`
- `source_name`
- `filename`
- `report_date`
- `remote_hash`
- `remote_filesize`
- `stored_path`
- `manifest_seen_at`
- `downloaded_at`
- `validated_at`
- `parse_status`
- `parse_error`
- `supersedes_source_file_id`

说明：

- 同名文件若 hash 改变，不覆盖旧版本
- 通过 `supersedes_source_file_id` 表达修订链

### 2. `prof_finance_source_report`

记录源文件内报告级元数据。

核心字段：

- `source_report_id`
- `source_file_id`
- `report_date`
- `field_count`
- `row_count`
- `parsed_at`

### 3. `prof_finance_source_value_raw`

记录逐证券逐字段的原始解析值。

核心字段：

- `source_value_id`
- `source_file_id`
- `full_code`
- `report_date`
- `source_field_id`
- `raw_numeric_value`
- `raw_text_value`
- `raw_value_type`
- `parsed_at`

索引重点：

- `(full_code, report_date, source_field_id, source_file_id)`
- `(report_date, source_field_id)`
- `(source_file_id, full_code)`

说明：

- 这是 raw layer 的主事实表
- 可以大，可以窄，可以 append-only
- 它不是对外 API 的主查询表

### 4. `prof_finance_field_catalog`

记录字段目录元数据。

核心字段：

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

说明：

- 这张表应可由 baseline 生成
- 它既是字段目录的查询源，也是 serving 映射的规范来源

### 5. `prof_finance_report_version`

记录逐证券逐报告的版本头信息。  
这是最终 point-in-time 能力的关键表。

核心字段：

- `report_version_id`
- `full_code`
- `report_date`
- `source_file_id`
- `version_hash`
- `announce_date_raw`
- `effective_announce_date`
- `announce_date_source`
- `preview_announce_date_raw`
- `flash_report_announce_date_raw`
- `first_seen_at`
- `last_seen_at`
- `ingested_at`
- `is_latest_corrected`
- `supersedes_report_version_id`

说明：

- 同一个 `(full_code, report_date)` 允许存在多个 `report_version_id`
- 这张表不允许只剩一个“被覆盖后的最新值”
- `announce_date_raw` 优先来自：
  - `314 financial_report_announcement_date`
  - `313 earnings_preview_announcement_date`
  - `315 flash_report_announcement_date`

### 6. `prof_finance_report_payload`

正式 serving 层载荷表。  
采用 **单证券单报告版本一行** 设计，而不是 EAV serving。

核心字段：

- `report_version_id`
- `full_code`
- `report_date`
- `field_values`
- `missing_field_codes`
- `supported_field_count`
- `available_field_count`
- `payload_hash`
- `materialized_at`

说明：

- `field_values` 是对象，建议存 JSON
- `missing_field_codes` 可存 JSON array
- `report_version_id` 与 `prof_finance_report_version` 一一对应
- 这是 `history/snapshot/cross-section/coverage` 的正式 serving source
- `field_values` 只允许写入真实存在的字段 key，不允许把缺失字段序列化成默认 `0` 或 `0.0`
- 推荐 materialization 方式：
  - 优先用 `map[string]interface{}` 逐字段写入
  - 只在字段真实存在且通过合法性校验时落 key
  - 避免使用会把零值字段批量序列化出来的固定 struct 作为主 payload 构造方式
- 若 serving 查询需要使用 `json_extract` 等 SQLite JSON 函数，必须先验证当前运行环境具备对应 JSON 扩展能力

为什么不用 EAV serving：

- 5000 只股票 x 400 字段 x 多期历史在 SQLite 下会造成极重扫描
- 横截面查询不应依赖几十次 self join 或大规模 pivot
- PRD 本身就要求 API 返回 `field_values` 对象，底层 serving 也应贴合这个查询形态

### 7. `prof_finance_source_watermark`

记录系统对上游专业财务源的同步边界。

核心字段：

- `source_name`
- `manifest_fetched_at`
- `latest_report_date_seen`
- `latest_report_date_ingested`
- `watermark_date`
- `updated_at`

说明：

- `watermark_date` 表示系统承认自己已经完整同步到的日期边界
- 它是 `knowledge_cutoff` 的组成部分

### 8. Optional: `prof_finance_projection_cache`

可选热点投影层，只为性能优化服务。

可能字段：

- `projection_name`
- `report_version_id`
- `full_code`
- `report_date`
- 若干热点字段

说明：

- 只有在 cross-section 热点查询确实需要时再建
- 它不是规范 truth 层

### 9. `prof_finance_schema_migration`

记录 schema migration 历史。

核心字段：

- `version`
- `name`
- `applied_at`
- `checksum`

说明：

- migration 必须 forward-only
- 同一版本不得重复执行
- rebuild 可以重建 serving 数据，但不能替代 migration 记录本身

---

## Write Strategy

### Raw Artifact Layer

- append-only 为主
- 同名 ZIP 若 hash 改变，不覆盖旧 `source_file`
- 文件级修订通过 `supersedes_source_file_id` 追踪

### Parsed Raw Layer

- append-only
- `source_file_id + full_code + source_field_id` 是最小可追溯单元
- 永远能反查到原始 ZIP

### Serving Layer

- 不以 `(full_code, report_date)` 单行直接覆盖全部历史
- serving 以 `report_version_id` 为主键
- “当前最新修订版”只是一个选择视图，不是物理覆盖策略

### Schema Migration

- 所有结构化表变更都必须通过 migration 文件执行
- raw artifact 层允许长期稳定不变；serving 层与 visibility 层允许演进
- migration 必须可重放、可审计、可在空库和已有库上运行
- rebuild 流程只负责重建 raw/serving 内容，不负责替代 schema 版本管理

---

## Restatement Policy

### Problem

企业可能修订旧财报。  
若只保留“修订后值”，则严格 point-in-time 会失真。

### Final decision

- raw layer 永远保留所有版本
- serving layer 也保留多个 `report_version_id`
- `is_latest_corrected=true` 仅表示“当前最新修订版”
- point-in-time 查询不能只看 latest corrected

### Query implications

- `latest_report`
  - 返回最新 `report_date` 下的 latest corrected version
- `exact`
  - 若未传 `as_of_date`，返回该 `report_date` 的 latest corrected version
  - 若传 `as_of_date`，需按可见性规则选择当时已可见版本
- `latest_available`
  - 在 `as_of_date` 下，从所有可见报告版本中选最新 `report_date`

---

## Date and Visibility Semantics

### `report_date`

报告所属期间，如：

- `20251231`
- `20260331`

### `announce_date_raw`

原始公告日期，优先来自：

- `314 financial_report_announcement_date`
- `313 earnings_preview_announcement_date`
- `315 flash_report_announcement_date`

说明：

- `gpcw` 中通常为 `YYMMDD`
- 入库时统一归一成 `YYYYMMDD`
- 若原值非法、空值或 `000000`，不得伪装成真实公告日

### `effective_announce_date`

可见性计算使用的有效日期。

规则：

- 若 `announce_date_raw` 合法，`effective_announce_date = announce_date_raw`
- 若 `announce_date_raw` 缺失或非法，`effective_announce_date = date(first_seen_at)`

说明：

- 这是 fallback visibility date
- 它不是原始公告日期

### `announce_date_source`

取值建议：

- `gpcw_314`
- `gpcw_313`
- `gpcw_315`
- `first_seen_fallback`

### `first_seen_at`

系统第一次成功抓取并摄取该报告版本的时间。  
它是系统级时间，不是公司公告时间。

### `knowledge_cutoff`

系统在本次查询里承认自己“最晚知道到哪里”的边界。

建议规则：

- `knowledge_cutoff = min(as_of_date, watermark_date)`

### Visibility Rule

报告版本可见，当且仅当：

1. `effective_announce_date <= knowledge_cutoff`
2. 该 `report_version_id` 已成功摄取
3. 若存在更晚修订版，则只有在该修订版自己的 `effective_announce_date <= knowledge_cutoff` 时才可替换旧版本

---

## Query Serving Rules

### Rule 1: Production APIs must read serving DB

最终正式查询路径：

- `/api/v1/prof-finance/fields`
  - `prof_finance_field_catalog`
- `/api/v1/prof-finance/history`
  - `prof_finance_report_version + prof_finance_report_payload`
- `/api/v1/prof-finance/snapshot`
  - `prof_finance_report_version + prof_finance_report_payload`
- `/api/v1/prof-finance/cross-section`
  - `prof_finance_report_version + prof_finance_report_payload`
- `/api/v1/prof-finance/coverage`
  - `prof_finance_field_catalog + prof_finance_report_version + prof_finance_report_payload`

### Rule 2: ZIP direct-read is bootstrap/recovery only

允许：

- 初次导入
- 重建
- 校验
- 调试

不允许：

- 正式 handler 长期把 ZIP 直读当主查询路径

### Rule 3: Cross-section reads item-level visibility

`cross-section` 不能只返回顶层 `report_date`。  
每个 item 都要绑定自己的：

- `report_date`
- `announce_date`
- `coverage`

### Rule 4: Cross-section must be paginated

`cross-section` 不得尝试一次性返回整个证券全集。

最终查询层必须支持：

- `limit`
- `cursor`
- `next_cursor`
- `full_codes <= 500`

建议约束：

- 默认 `limit=100`
- 最大 `limit=500`
- `full_codes` 最大输入数量为 `500`
- 默认稳定顺序为 `full_code ASC`

---

## Ingestion Workflow

### Source Sync

固定动作：

1. 刷新 `gpcw.txt`
2. 对比已知 `source_file`
3. 下载新增或修订 ZIP
4. 校验 ZIP
5. 写 `prof_finance_source_file`

### Raw Parse

固定动作：

1. 解析 DAT
2. 生成 `prof_finance_source_report`
3. 生成 `prof_finance_source_value_raw`
4. 抽取 `314/313/315`
5. 记录 `first_seen_at`

### Serving Materialization

固定动作：

1. 依据 `field_catalog` 做映射
2. 生成 `report_version`
3. 生成 `report_payload`
4. 更新 `watermark`

### Rebuild

必须支持：

- 不重新下载 ZIP
- 基于 artifacts + raw facts 重新构建 serving 层

---

## Performance Notes

### Why raw EAV is acceptable

raw layer 的核心职责是：

- 追溯
- 重建
- 字段重映射

因此它可以是 EAV 窄表。

### Why serving EAV is not acceptable

SQLite 下，横截面查询如果以 EAV 作为主 serving，会遇到：

- 巨量行扫描
- 大量 pivot / 条件聚合
- 排序与分页代价高

因此最终 serving 层必须以 **JSON payload per report version** 为主。  
必要时再对热点字段做投影缓存，而不是反过来把 EAV 当主查询形态。

## Performance SLA

以下 SLA 是最终生产目标，面向 serving 层，而不是 bootstrap ZIP 直读路径：

- `/api/v1/prof-finance/fields`
  - 热路径 `p95 <= 300ms`
- `/api/v1/prof-finance/snapshot`
  - 单证券、最多 `50` 字段，热路径 `p95 <= 800ms`
- `/api/v1/prof-finance/history`
  - 单证券、最多 `40` 报告期、最多 `50` 字段，热路径 `p95 <= 1500ms`
- `/api/v1/prof-finance/cross-section`
  - 单页最多 `500` 证券、最多 `50` 字段，热路径 `p95 <= 3000ms`
- `/api/v1/prof-finance/coverage`
  - 单证券覆盖查询，热路径 `p95 <= 800ms`

这些 SLA 必须基于：

- serving DB
- 正常 watermark
- 正常本地缓存

不能拿冷启动实时解 ZIP 的路径冒充正式 SLA。

## Observability Model

最小可观测集合必须包括：

### Logs

- `request_id`
- `route`
- `latency_ms`
- `result_status`
- `error_code`
- `full_code_count`
- `field_code_count`

### Metrics

- `prof_finance_source_manifest_refresh_total`
- `prof_finance_source_zip_download_total`
- `prof_finance_source_zip_download_failure_total`
- `prof_finance_source_parse_failure_total`
- `prof_finance_serving_query_latency_ms`
- `prof_finance_cross_section_page_size`
- `prof_finance_watermark_lag_days`
- `prof_finance_restatement_version_count`

### Data freshness

必须能回答：

- 最新 `report_date` 看到多少
- 最新 `report_date` 入库到哪里
- 当前 `watermark_date` 是多少
- 哪些 source file 处于 `parse_status != success`

---

## Operational Decisions

1. 全量 `gpcw*.zip` 默认保留。
2. 原始 artifacts、raw facts、serving payload 必须都能追溯。
3. `announce_date_raw` 缺失时，允许使用 `first_seen_at` 生成 `effective_announce_date`。
4. 不允许把 `first_seen_at` 冒充为原始公告日。
5. point-in-time 查询必须基于 `effective_announce_date` 与 `knowledge_cutoff`。
6. restatement 历史必须保留。
7. Sprint 顺序和开发拆解只放在 Implementation Guide，不放在本文件。

---

## Testing Constraints

涉及 ZIP/TXT/DAT 的测试必须：

- 使用 `t.TempDir()`
- 使用 `t.Cleanup()`
- 不得把测试 ZIP、缓存目录、解压产物残留在 workspace
- 最终 `git status --short` 不得因为测试产物变脏

---

## Normative Decisions

1. `gpcw*.zip` 是原始事实 source of truth。
2. 正式查询 API 的 serving source of truth 是数据库层，不是 ZIP 直读。
3. raw layer 可以使用 EAV；serving layer 不能以 EAV 作为主查询路径。
4. serving layer 采用“单证券单报告版本一行”的 JSON payload 设计。
5. `FINVALUE` 与 `FINONE` 共享字段目录，不共享查询语义。
6. `announce_date_raw` 优先来自 `314/313/315`。
7. `effective_announce_date` 是可见性日期，不等于原始公告日。
8. `knowledge_cutoff` 必须基于 `as_of_date` 与 `watermark_date` 共同定义。
9. 财务重述不能通过覆盖式 upsert 抹平历史。
10. 正式 handler 不应长期依赖“请求时实时解 ZIP”。
11. `cross-section` 必须使用 cursor 分页，且 serving 层按页提供稳定顺序。
12. schema migration 是正式数据层的一部分，不是可选工程装饰。
13. observability 与 freshness 指标属于 correctness contract 的组成部分。
