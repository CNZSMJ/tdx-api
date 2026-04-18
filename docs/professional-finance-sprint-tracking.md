# Professional Finance Sprint Tracking

## Read Source Documents

已完整读取并确认以下 4 份 SSOT 文档：

1. `docs/professional-finance-api-prd.md`
2. `docs/professional-finance-field-catalog-baseline.md`
3. `docs/professional-finance-data-architecture.md`
4. `docs/professional-finance-implementation-guide.md`

当前未发现需要先修正才能继续编码的显式 contract 冲突。若后续发现冲突，按以下优先级修正文档后再继续实现：

1. `professional-finance-api-prd.md`
2. `professional-finance-data-architecture.md`
3. `professional-finance-field-catalog-baseline.md`
4. `professional-finance-implementation-guide.md`

## Final Contract Snapshot

- 新接口统一挂在 `/api/v1/prof-finance/*`
- 查询接口只接受 `full_code` / `full_codes`、`field_codes`
- 查询响应只返回 `field_values`
- 所有响应必须包含 `request_id`
- 成功 envelope 固定为 `code=0`、`message=success`、`request_id`、`data`
- 错误 envelope 固定返回稳定 `error.error_code`
- 最小错误代码集合：
  - `INVALID_ARGUMENT`
  - `NOT_FOUND`
  - `UNSUPPORTED_FIELD`
  - `UNSUPPORTED_PERIOD_MODE`
  - `SOURCE_NOT_READY`
  - `RATE_LIMITED`
  - `INTERNAL_ERROR`
- 字段目录必须完整覆盖 `403` 个 `source_field_id`
- public category 必须稳定为 `17` 个
- `cross-section` 必须使用 cursor 分页，默认 `limit=100`，最大 `limit=500`
- `full_codes` 单次请求最大 `500`
- 时间语义必须显式返回：
  - `report_date`
  - `announce_date`
  - `as_of_date`
  - `knowledge_cutoff`
- `period_mode` 只允许：
  - `exact`
  - `latest_report`
  - `latest_available`

## Data-Layer Red Lines

- 正式查询主路径必须读 serving DB，不能长期以 ZIP 直读作为主查询路径
- raw artifact、raw facts、serving payload、visibility layer 必须分层
- serving 层采用单证券单报告版本一行的 JSON payload，而不是 EAV serving
- 财务重述必须保留 `report_version_id`，严禁覆盖旧版本
- 缺失值与零值严格区分，严禁把缺失序列化成 `0` 或 `0.0`
- `field_values` 必须按存在性写入 key，优先使用 `map[string]interface{}`
- 依赖 SQLite JSON 查询前必须显式校验 `SELECT json_valid('{}')`
- `knowledge_cutoff = min(as_of_date, watermark_date)` 的可见性语义必须落地
- `announce_date_raw` 优先来自 `314/313/315`，缺失时只能 fallback 到 `first_seen_at` 的日期部分
- migration 必须显式记录，不允许用删库重建替代 schema evolution
- 测试必须使用 `t.TempDir()` / `t.Cleanup()`，不得污染仓库

## Current Sprint

`Sprint 7 (Completed)`

## Sprint 1 Goal

- 建立完整字段注册层
- 生成并校验 `403` 个字段目录项和 `17` 个 category
- 落地统一错误 envelope
- 实现 `/api/v1/prof-finance/fields`
- 为后续 ingestion / serving / query 层提供稳定 registry contract

## Sprint 1 First Test Plan

- `profinance/registry_test.go`
  - 字段总数为 `403`
  - category 总数为 `17`
  - `source_field_id` 唯一
  - 关键字段存在并元数据完整：
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
- `web/prof_finance_handlers_test.go`
  - `/api/v1/prof-finance/fields` 路由可用
  - 成功响应包含 `request_id`
  - 支持 `category` / `query` 过滤
  - 错误响应包含稳定 `error_code`
  - payload 使用字段目录 contract，不回退到旧命名

## Completed

- 已确认 4 份 SSOT 文档全部读取完成
- 已确认 baseline 当前包含 `403` 个字段、`17` 个 public categories
- 已确认当前仓库仅存在旧版 `profinance.Service` 和 `/api/financial-reports`，尚未具备最终 Professional Finance API
- `Sprint 1` 已完成：
  - 新增完整字段注册层
  - baseline `403` 字段已生成为 Go registry 数据文件
  - 字段目录元数据已补齐：
    - `concept_code`
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
  - 新增 `/api/v1/prof-finance/fields`
  - 新增 Professional Finance 专属 success / error envelope 与 `request_id`
  - `category` / `query` 过滤与非法 category 校验已落地
- `Sprint 2` 核心数据层已完成：
  - SQLite migration 基础设施已落地
  - SQLite JSON capability 已通过 `SELECT json_valid('{}')` 校验
  - 原始 artifact 目录已切到：
    - `artifacts/gpcw.txt`
    - `artifacts/zips/gpcwYYYYMMDD.zip`
  - 已落地结构化表：
    - `prof_finance_source_file`
    - `prof_finance_source_report`
    - `prof_finance_source_value_raw`
    - `prof_finance_field_catalog`
    - `prof_finance_report_version`
    - `prof_finance_report_payload`
    - `prof_finance_source_watermark`
  - `Sync()` 已实现：
    - manifest 拉取
    - ZIP 下载与校验
    - raw facts 入库
    - serving payload materialization
    - watermark 更新
- `Sprint 3` 服务层核心已完成：
  - `History()` 已基于 serving DB 查询，不再依赖 handler 直扫 ZIP
  - `latest_available` 的可见性选择已基于：
    - `effective_announce_date`
    - `knowledge_cutoff`
  - legacy `LatestForCode()` / `ListForCode()` 已改为复用统一 query 层
- `Sprint 4` 已完成：
  - `/api/v1/prof-finance/snapshot`
  - `/api/v1/prof-finance/coverage`
  - 单证券快照和覆盖状态已统一复用 serving / visibility 规则
- `Sprint 5` 已完成：
  - `/api/v1/prof-finance/cross-section`
  - `cursor` 分页已落地
  - `full_codes > 500` 已返回 `INVALID_ARGUMENT`
- `Sprint 6` 已完成：
  - restatement 版本链测试已落地
  - `exact` / `latest_report` / `latest_available` 三种语义已测试闭环
  - `knowledge_cutoff = min(as_of_date, watermark_date)` 已通过服务层选择逻辑落地
- `Sprint 7` 已完成：
  - `/api/profile` 已通过新的 `LatestForCode()` 兼容层复用统一 serving query
  - `Rebuild()` 已支持从 raw facts 重新 materialize serving layer
  - Professional Finance handlers 已补充结构化访问日志：
    - `request_id`
    - `route`
    - `latency_ms`
    - `result_status`
    - `error_code`
    - `field_code_count`
    - `full_code_count`
  - `README.md` 与 `API_接口文档.md` 已收口新增 API family
  - `API_接口文档.md` 已按实现补齐：
    - `request_id + error` envelope 说明
    - strict `full_code/full_codes` 约束
    - `history` 的默认/最大 `limit=40`
    - `snapshot/cross-section` 的默认 `period_mode` 与必填条件
- 重复证券行处理已补齐：
  - 完全重复的同证券行在 parser 层去重
  - 仅未跟踪字段差异的同证券行在 parser 层去重
  - 已跟踪字段冲突的同证券行在 parser 层直接报错，禁止静默选边
  - 本地 `stock-web` 重启后，`20251231` 启动预取不再触发 `(source_file_id, full_code)` 唯一约束

## Important Implementation Decisions

- 字段 registry 以 `docs/professional-finance-field-catalog-baseline.md` 为生成源，生成静态 Go 数据文件，避免运行时依赖文档路径
- `statement` 按 category 映射到受控集合：
  - `shareholder` / `institutional_holding -> disclosure`
  - `earnings_preview -> preview`
  - `earnings_flash_report -> flash_report`
  - 分析类 category -> `analysis`
- `period_semantics` 当前按文档规则和字段命名启发式补齐：
  - `single_quarter -> single_quarter`
  - `*_ttm -> ttm`
  - `balance_sheet` / `shareholder` / `institutional_holding` / `capital_structure -> instant`
  - 其余主表和分析项默认 `report_period`
- `unit` / 精度元数据当前按字段名、category 和时间语义做稳定推导：
  - `日期 -> date_yyyymmdd`
  - `每股 -> yuan`
  - `万元 -> ten_thousand_yuan`
  - `%/率 -> percent`
  - `股/持股 -> shares`
  - `机构数/股东人数 -> count`
- source ingest 的重要实现决策：
  - 测试与 parser 将 `NaN` 视为缺失值，用于验证 missing-field 行为
  - 未缺失的数值字段按源值直落 serving payload，不再像旧实现那样对 `operating_revenue_ttm` 强制乘 `10000`
  - legacy `/api/profile` 兼容层仍在适配时把 `operating_revenue_ttm` 转回 yuan，以维持旧 valuation 逻辑
- visibility / restatement 基础策略：
  - `announce_date_raw` 优先取 `314 -> 313 -> 315`
  - 缺失时 `effective_announce_date` fallback 到 `first_seen_at` 日期部分
  - `report_version` 保留版本链，后续继续补 restatement 场景测试
- 针对源 DAT 中的重复证券行：
  - 去重发生在 parser 层，避免进入 raw facts / serving 层后再撞 `(source_file_id, full_code)` 唯一约束
  - 判断等价性只基于当前 registry 已跟踪字段
  - 若重复行在已跟踪字段上完全一致，则视为可安全合并
  - 若重复行只在未跟踪字段上不同，当前对外 contract 下仍视为可安全合并
  - 若重复行在已跟踪字段上出现差异，直接返回 parse error，等待字段映射或源数据规则进一步确认后再处理

## Validation Commands Passed

- `git rev-parse --show-toplevel`
- baseline 字段统计脚本：
  - `403` fields
  - `17` public categories
- `go test ./profinance/...`
- `cd web && TDX_WEB_SKIP_INIT=1 go test ./...`
- `go test ./profinance -run 'TestParseDATReportRawDeduplicatesExactDuplicateRows|TestParseDATReportRawDeduplicatesRowsWhenOnlyUntrackedFieldsDiffer|TestParseDATReportRawRejectsRowsWhenTrackedFieldsConflict|TestSyncDeduplicatesRowsWhenOnlyUntrackedFieldsDiffer'`
- `cd web && go build -o stock-web.new .`
- 本地进程重启后运行验证：
  - `GET http://127.0.0.1:8080/api/health`
  - `GET /api/v1/prof-finance/fields`
  - `GET /api/v1/prof-finance/history?full_code=sz300750&field_codes=book_value_per_share&limit=1&period=all`
- 最终工作树检查：
  - 当前变更仅位于 `repos/tdx-api`
  - 存在一个预先存在的未跟踪日志文件 `web/stock-web.local.log`，未纳入本任务变更

## Current Blockers

- 无 blocker

## Next Steps

1. 如需提交，先仅 stage 本任务相关文件
2. 明确保留或清理非本任务未跟踪文件 `web/stock-web.local.log`
