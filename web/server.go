package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/injoyai/tdx"
	collectorpkg "github.com/injoyai/tdx/collector"
	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

var (
	client             *tdx.Client
	manager            *tdx.Manage
	taskManager        = NewTaskManager()
	databaseDir        string
	collectorRuntime   *collectorpkg.Runtime
	collectorRunActive atomic.Bool
	collectorJobState  = newCollectorExecutionState()
)

const (
	collectorDailySyncSpec             = "0 0 18 * * *"
	collectorDailyReconcileSpec        = "0 0 19 * * *"
	collectorRunTimeout                = 6 * time.Hour // default for daily_full_sync / reconcile; override with COLLECTOR_RUN_TIMEOUT
	collectorDefaultWorkers            = 4
	collectorMaxCatchupWorkers         = 32 // TDX 侧连接过多易被限流；需要更高请改此常量并自担风险
	collectorDefaultRequestMinInterval = 150 * time.Millisecond
)

// collectorCatchUpContext returns a context for runCollectorCatchUp.
// Root cause of startup failures: a single 6h deadline is shorter than full
// startup catch-up for thousands of symbols (trade/live/order per trading day).
// Startup trigger defaults to no overall deadline; set COLLECTOR_STARTUP_CATCHUP_TIMEOUT to cap it.
func collectorCatchUpContext(trigger string) (context.Context, context.CancelFunc) {
	if trigger == "startup" {
		if d := collectorStartupCatchUpTimeoutFromEnv(); d > 0 {
			return context.WithTimeout(context.Background(), d)
		}
		return context.WithCancel(context.Background())
	}
	return context.WithTimeout(context.Background(), collectorGeneralRunTimeout())
}

func collectorStartupCatchUpTimeoutFromEnv() time.Duration {
	raw := strings.TrimSpace(os.Getenv("COLLECTOR_STARTUP_CATCHUP_TIMEOUT"))
	if raw == "" || raw == "0" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		log.Printf("collector: 忽略无效的 COLLECTOR_STARTUP_CATCHUP_TIMEOUT=%q", raw)
		return 0
	}
	return d
}

func collectorGeneralRunTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("COLLECTOR_RUN_TIMEOUT"))
	if raw == "" {
		return collectorRunTimeout
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		log.Printf("collector: 忽略无效的 COLLECTOR_RUN_TIMEOUT=%q，使用默认 %v", raw, collectorRunTimeout)
		return collectorRunTimeout
	}
	return d
}

type collectorJobSnapshot struct {
	Name        string    `json:"name"`
	Trigger     string    `json:"trigger"`
	Date        string    `json:"date,omitempty"`
	Status      string    `json:"status"`
	Error       string    `json:"error,omitempty"`
	ReportPath  string    `json:"report_path,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
}

type collectorExecutionState struct {
	mu      sync.RWMutex
	current *collectorJobSnapshot
	last    map[string]collectorJobSnapshot
}

func newCollectorExecutionState() *collectorExecutionState {
	return &collectorExecutionState{
		last: make(map[string]collectorJobSnapshot),
	}
}

func (s *collectorExecutionState) start(name, trigger, date string) *collectorJobSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.current = &collectorJobSnapshot{
		Name:      name,
		Trigger:   trigger,
		Date:      date,
		Status:    "running",
		StartedAt: time.Now(),
	}
	return s.current
}

func (s *collectorExecutionState) finish(job *collectorJobSnapshot, status, reportPath string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if job == nil {
		return
	}
	job.Status = status
	job.ReportPath = reportPath
	job.CompletedAt = time.Now()
	if err != nil {
		job.Error = err.Error()
	}
	s.last[job.Name] = *job
	s.current = nil
}

func (s *collectorExecutionState) snapshot() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()

	last := make(map[string]collectorJobSnapshot, len(s.last))
	for key, value := range s.last {
		last[key] = value
	}
	out := map[string]any{
		"active": collectorRunActive.Load(),
		"last":   last,
	}
	if s.current != nil {
		current := *s.current
		out["current"] = current
	}
	return out
}

func configureDatabaseDir() {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		return
	}

	webDir := filepath.Dir(sourceFile)
	projectRoot := filepath.Dir(webDir)
	targetDir := filepath.Join(projectRoot, "data", "database")
	legacyDir := filepath.Join(projectRoot, "web", "data", "database")
	databaseDir = targetDir

	if _, err := os.Stat(targetDir); err == nil {
		return
	}

	if _, err := os.Stat(legacyDir); err != nil {
		return
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		log.Printf("创建统一数据目录失败: %v", err)
		return
	}

	for _, name := range []string{"codes.db", "workday.db"} {
		src := filepath.Join(legacyDir, name)
		dst := filepath.Join(targetDir, name)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		if err := copyFile(src, dst); err != nil {
			log.Printf("迁移数据库 %s 失败: %v", name, err)
			continue
		}
		log.Printf("已迁移数据库: %s -> %s", src, dst)
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
	}()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}

	return out.Sync()
}

func init() {
	var err error
	configureDatabaseDir()
	// 连接通达信服务器
	client, err = tdx.DialDefault(tdx.WithDebug(false))
	if err != nil {
		log.Fatalf("连接服务器失败: %v", err)
	}
	log.Println("成功连接到通达信服务器")

	// 初始化代码缓存
	if err = os.MkdirAll(databaseDir, 0755); err != nil {
		log.Printf("创建数据目录失败: %v", err)
	}
	if codes, err := tdx.NewCodesSqlite(client, filepath.Join(databaseDir, "codes.db")); err != nil {
		log.Printf("初始化代码库失败: %v", err)
	} else {
		tdx.DefaultCodes = codes
		if err := tdx.DefaultCodes.Update(); err != nil {
			log.Printf("更新代码库失败: %v", err)
		} else {
			log.Printf("已加载股票代码，共 %d 条", len(tdx.DefaultCodes.Map))
		}
	}

	manager, err = tdx.NewManage(&tdx.ManageConfig{
		Number:          collectorCatchupWorkersForPool(),
		CodesFilename:   filepath.Join(databaseDir, "codes.db"),
		WorkdayFileName: filepath.Join(databaseDir, "workday.db"),
	})
	if err != nil {
		log.Fatalf("初始化数据管理器失败: %v", err)
	}
	if err := manager.Codes.Update(); err != nil {
		log.Printf("更新管理器代码库失败: %v", err)
	}
	if err := manager.Workday.Update(); err != nil {
		log.Printf("更新交易日数据失败: %v", err)
	}
	manager.Cron.Start()
	initCollectorRuntime()
}

func initCollectorRuntime() {
	store, err := collectorpkg.OpenStore(filepath.Join(databaseDir, "collector.db"))
	if err != nil {
		log.Printf("初始化 collector store 失败: %v", err)
		return
	}

	runtime, err := collectorpkg.NewRuntime(
		store,
		collectorpkg.NewTDXProvider(manager, client),
		collectorpkg.RuntimeConfig{
			ScheduleName:          "collector_startup_catchup",
			DailySyncScheduleName: "collector_daily_full_sync",
			ReconcileScheduleName: "collector_daily_reconcile",
			ReportDir:             filepath.Join(databaseDir, "collector_reports"),
			BootstrapStartDate:    strings.TrimSpace(os.Getenv("COLLECTOR_BOOTSTRAP_START")),
			RequestMinInterval:    collectorRequestMinInterval(),
			CatchUpWorkers:        collectorCatchUpWorkers(),
			KlinePeriods:          collectorKlinePeriods(),
			Metadata: collectorpkg.MetadataConfig{
				CodesDBPath:   filepath.Join(databaseDir, "codes.db"),
				WorkdayDBPath: filepath.Join(databaseDir, "workday.db"),
			},
			Kline:        collectorpkg.KlineConfig{BaseDir: filepath.Join(databaseDir, "kline")},
			Trade:        collectorpkg.TradeConfig{BaseDir: filepath.Join(databaseDir, "trade")},
			OrderHistory: collectorpkg.OrderHistoryConfig{BaseDir: filepath.Join(databaseDir, "order_history")},
			Live:         collectorpkg.LiveCaptureConfig{BaseDir: filepath.Join(databaseDir, "live")},
			Fundamentals: collectorpkg.FundamentalsConfig{BaseDir: filepath.Join(databaseDir, "fundamentals")},
		},
	)
	if err != nil {
		log.Printf("初始化 collector runtime 失败: %v", err)
		_ = store.Close()
		return
	}
	collectorRuntime = runtime

	if _, err := manager.Cron.AddFunc(collectorDailySyncSpec, func() {
		go runCollectorCatchUp("daily-18:00")
	}); err != nil {
		log.Printf("注册 collector 每日 18:00 全量同步失败: %v", err)
		return
	}

	if _, err := manager.Cron.AddFunc(collectorDailyReconcileSpec, func() {
		go runCollectorReconcile("daily-19:00", time.Now().Format("20060102"))
	}); err != nil {
		log.Printf("注册 collector 每日 19:00 对账失败: %v", err)
		return
	}

	bootstrapStart := strings.TrimSpace(os.Getenv("COLLECTOR_BOOTSTRAP_START"))
	if bootstrapStart == "" {
		bootstrapStart = "provider-earliest"
	}
	log.Printf("已启用 collector 计划: startup catch-up + 每日 18:00 全量同步 + 每日 19:00 对账，本地时区=%s bootstrap_start=%s min_request_interval=%s catch_up_workers=%d", time.Now().Location(), bootstrapStart, collectorRequestMinInterval(), collectorCatchUpWorkers())

	go runCollectorStartupSequence()
}

func collectorRequestMinInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv("COLLECTOR_REQUEST_MIN_INTERVAL"))
	if raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	return collectorDefaultRequestMinInterval
}

// collectorCatchupWorkersForPool 在 init 阶段创建 TDX 连接池时使用（此时 manager 尚未就绪）。
// 须与 collectorCatchUpWorkers / 限流 slot 数一致，否则 worker 会在 manage.Do 上空等连接。
func collectorCatchupWorkersForPool() int {
	if n, ok := collectorCatchupWorkersFromEnv(); ok {
		return n
	}
	return collectorDefaultWorkers
}

func collectorCatchupWorkersFromEnv() (int, bool) {
	raw := strings.TrimSpace(os.Getenv("COLLECTOR_CATCHUP_WORKERS"))
	if raw == "" {
		return 0, false
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		log.Printf("collector: 忽略无效的 COLLECTOR_CATCHUP_WORKERS=%q", raw)
		return 0, false
	}
	if v > collectorMaxCatchupWorkers {
		log.Printf("collector: COLLECTOR_CATCHUP_WORKERS=%d 超过上限 %d，已使用 %d", v, collectorMaxCatchupWorkers, collectorMaxCatchupWorkers)
		v = collectorMaxCatchupWorkers
	}
	return v, true
}

func collectorCatchUpWorkers() int {
	if n, ok := collectorCatchupWorkersFromEnv(); ok {
		return n
	}
	if manager != nil && manager.Config != nil && manager.Config.Number > 0 {
		return manager.Config.Number
	}
	return collectorDefaultWorkers
}

func collectorKlinePeriods() []collectorpkg.KlinePeriod {
	return []collectorpkg.KlinePeriod{
		collectorpkg.PeriodMinute,
		collectorpkg.Period5Minute,
		collectorpkg.Period15Minute,
		collectorpkg.Period30Minute,
		collectorpkg.Period60Minute,
		collectorpkg.PeriodDay,
		collectorpkg.PeriodWeek,
		collectorpkg.PeriodMonth,
		collectorpkg.PeriodQuarter,
		collectorpkg.PeriodYear,
	}
}

func runCollectorStartupSequence() {
	if err := runCollectorCatchUp("startup"); err != nil {
		log.Printf("collector startup catch-up 失败: %v", err)
	}
	if err := runCollectorMissedMaintenance(); err != nil {
		log.Printf("collector 漏跑补偿失败: %v", err)
	}
}

func runCollectorCatchUp(trigger string) error {
	if collectorRuntime == nil {
		return fmt.Errorf("collector runtime 未初始化，跳过触发: %s", trigger)
	}
	if !collectorRunActive.CompareAndSwap(false, true) {
		return fmt.Errorf("collector 全量补采仍在运行，跳过触发: %s", trigger)
	}
	defer collectorRunActive.Store(false)

	jobName := "daily_full_sync"
	runFn := collectorRuntime.RunDailyFullSync
	if trigger == "startup" {
		jobName = "startup_catchup"
		runFn = collectorRuntime.RunStartupCatchUp
	}
	job := collectorJobState.start(jobName, trigger, "")
	if trigger == "startup" && collectorStartupCatchUpTimeoutFromEnv() == 0 {
		log.Printf("collector 全量补采开始: trigger=%s (启动补采未设置 COLLECTOR_STARTUP_CATCHUP_TIMEOUT，无整体 deadline)", trigger)
	} else {
		log.Printf("collector 全量补采开始: trigger=%s", trigger)
	}
	ctx, cancel := collectorCatchUpContext(trigger)
	defer cancel()

	started := time.Now()
	if err := runFn(ctx); err != nil {
		collectorJobState.finish(job, "failed", "", err)
		log.Printf("collector 全量补采失败: trigger=%s duration=%s err=%v", trigger, time.Since(started), err)
		return err
	}
	collectorJobState.finish(job, "passed", "", nil)
	log.Printf("collector 全量补采完成: trigger=%s duration=%s", trigger, time.Since(started))
	return nil
}

func runCollectorReconcile(trigger, date string) (*collectorpkg.ReconcileReport, error) {
	if collectorRuntime == nil {
		return nil, fmt.Errorf("collector runtime 未初始化，跳过对账: trigger=%s date=%s", trigger, date)
	}
	if !collectorRunActive.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("collector 任务仍在运行，跳过对账: trigger=%s date=%s", trigger, date)
	}
	defer collectorRunActive.Store(false)

	jobName := "daily_reconcile"
	if trigger == "manual" {
		jobName = "manual_reconcile"
	}
	job := collectorJobState.start(jobName, trigger, date)
	log.Printf("collector 对账开始: trigger=%s date=%s", trigger, date)
	ctx, cancel := context.WithTimeout(context.Background(), collectorGeneralRunTimeout())
	defer cancel()

	started := time.Now()
	report, err := collectorRuntime.ReconcileDateWithTrigger(ctx, date, trigger)
	if err != nil {
		collectorJobState.finish(job, "failed", "", err)
		log.Printf("collector 对账失败: trigger=%s date=%s duration=%s err=%v", trigger, date, time.Since(started), err)
		return nil, err
	}
	collectorJobState.finish(job, report.Status, report.ReportPath, nil)
	log.Printf("collector 对账完成: trigger=%s date=%s status=%s duration=%s report=%s", trigger, date, report.Status, time.Since(started), report.ReportPath)
	return report, nil
}

func runCollectorMissedMaintenance() error {
	if collectorRuntime == nil {
		return nil
	}
	now := time.Now()
	syncAnchor := mostRecentScheduleAnchor(now, 18)
	if hasRun, err := collectorRuntime.HasSuccessfulRunInWindow(collectorRuntime.DailySyncScheduleName(), syncAnchor, syncAnchor.Add(24*time.Hour)); err != nil {
		return err
	} else if !hasRun {
		log.Printf("collector 检测到 18:00 全量同步漏跑，开始补偿: anchor=%s", syncAnchor.Format(time.RFC3339))
		if err := runCollectorCatchUp("compensate-daily-18:00"); err != nil {
			return err
		}
	}

	reconcileDate := mostRecentReconcileDate(now)
	hasReconcile, err := collectorRuntime.HasSuccessfulReconcileForDate(reconcileDate)
	if err != nil {
		return err
	}
	if !hasReconcile {
		log.Printf("collector 检测到 19:00 对账漏跑，开始补偿: date=%s", reconcileDate)
		if _, err := runCollectorReconcile("compensate-daily-19:00", reconcileDate); err != nil {
			return err
		}
	}
	return nil
}

func mostRecentScheduleAnchor(now time.Time, hour int) time.Time {
	anchor := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	if now.Before(anchor) {
		return anchor.Add(-24 * time.Hour)
	}
	return anchor
}

func mostRecentReconcileDate(now time.Time) string {
	anchor := time.Date(now.Year(), now.Month(), now.Day(), 19, 0, 0, 0, now.Location())
	if now.Before(anchor) {
		return now.AddDate(0, 0, -1).Format("20060102")
	}
	return now.Format("20060102")
}

func handleCollectorStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errorResponse(w, "只支持GET请求")
		return
	}
	if collectorRuntime == nil {
		errorResponse(w, "collector runtime 未初始化")
		return
	}
	status, err := collectorRuntime.Status()
	if err != nil {
		errorResponse(w, "读取collector状态失败: "+err.Error())
		return
	}
	successResponse(w, map[string]any{
		"runtime": status,
		"jobs":    collectorJobState.snapshot(),
		"schedule": map[string]string{
			"daily_full_sync": collectorDailySyncSpec,
			"daily_reconcile": collectorDailyReconcileSpec,
		},
	})
}

func handleCollectorReconcile(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		if collectorRuntime == nil {
			errorResponse(w, "collector runtime 未初始化")
			return
		}
		date := strings.TrimSpace(r.URL.Query().Get("date"))
		if date == "" {
			var req struct {
				Date string `json:"date"`
			}
			if r.Body != nil {
				_ = json.NewDecoder(r.Body).Decode(&req)
				date = strings.TrimSpace(req.Date)
			}
		}

		ctx, cancel := context.WithTimeout(r.Context(), collectorRunTimeout)
		defer cancel()
		_ = ctx
		report, err := runCollectorReconcile("manual", date)
		if err != nil {
			errorResponse(w, "执行对账失败: "+err.Error())
			return
		}
		successResponse(w, report)
	case http.MethodGet:
		date := strings.TrimSpace(r.URL.Query().Get("date"))
		report, err := collectorpkg.ReadReconcileReport(filepath.Join(databaseDir, "collector_reports"), date)
		if err != nil {
			errorResponse(w, "读取对账报告失败: "+err.Error())
			return
		}
		successResponse(w, report)
	default:
		errorResponse(w, "只支持GET和POST请求")
	}
}

// Response 统一响应结构
type Response struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data"`
}

// 返回成功响应
func successResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(Response{
		Code:    0,
		Message: "success",
		Data:    data,
	})
}

// 返回错误响应
func errorResponse(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(Response{
		Code:    -1,
		Message: message,
		Data:    nil,
	})
}

// 获取五档行情
func handleGetQuote(w http.ResponseWriter, r *http.Request) {
	codeParam := r.URL.Query().Get("code")
	if codeParam == "" {
		errorResponse(w, "股票代码不能为空")
		return
	}

	codes := splitCodes(codeParam)
	if len(codes) == 0 {
		errorResponse(w, "股票代码不能为空")
		return
	}

	quotes, err := client.GetQuote(codes...)
	if err != nil {
		errorResponse(w, fmt.Sprintf("获取行情失败: %v", err))
		return
	}

	successResponse(w, quotes)
}

// 获取K线数据（日K线默认使用前复权）
func handleGetKline(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	klineType := r.URL.Query().Get("type") // minute1/minute5/minute15/minute30/hour/day/week/month
	if code == "" {
		errorResponse(w, "股票代码不能为空")
		return
	}

	limit := parsePositiveInt(r.URL.Query().Get("limit"))

	var resp *protocol.KlineResp
	var err error

	switch klineType {
	case "minute1":
		resp, err = client.GetKlineMinuteAll(code)
	case "minute5":
		resp, err = client.GetKline5MinuteAll(code)
	case "minute15":
		resp, err = client.GetKline15MinuteAll(code)
	case "minute30":
		resp, err = client.GetKline30MinuteAll(code)
	case "hour":
		resp, err = client.GetKlineHourAll(code)
	case "week":
		resp, err = getQfqKlineDay(code)
		if err == nil && len(resp.List) > 0 {
			resp = convertToWeekKline(resp)
		}
	case "month":
		resp, err = getQfqKlineDay(code)
		if err == nil && len(resp.List) > 0 {
			resp = convertToMonthKline(resp)
		}
	case "day":
		fallthrough
	default:
		resp, err = getQfqKlineDay(code)
	}

	if err != nil {
		errorResponse(w, fmt.Sprintf("获取K线失败: %v", err))
		return
	}

	if limit > 0 && resp != nil && len(resp.List) > limit {
		resp.List = resp.List[len(resp.List)-limit:]
		resp.Count = uint16(len(resp.List))
	}

	successResponse(w, resp)
}

// getQfqKlineDay 获取前复权日K线数据
func getQfqKlineDay(code string) (*protocol.KlineResp, error) {
	// 使用同花顺API获取前复权数据
	klines, err := extend.GetTHSDayKline(code, extend.THS_QFQ)
	if err != nil {
		return nil, fmt.Errorf("获取前复权数据失败: %w", err)
	}

	if len(klines) == 0 {
		return nil, fmt.Errorf("同花顺前复权数据为空")
	}

	// 转换为 protocol.KlineResp 格式
	resp := &protocol.KlineResp{
		Count: uint16(len(klines)),
		List:  make([]*protocol.Kline, 0, len(klines)),
	}

	for i, k := range klines {
		pk := &protocol.Kline{
			Time:   time.Unix(k.Date, 0),
			Open:   k.Open,
			High:   k.High,
			Low:    k.Low,
			Close:  k.Close,
			Volume: k.Volume,
			Amount: k.Amount,
		}
		// 设置昨收价（使用上一条K线的收盘价）
		if i > 0 {
			pk.Last = klines[i-1].Close
		}
		resp.List = append(resp.List, pk)
	}

	return resp, nil
}

// convertToWeekKline 将日K线转换为周K线（简化版）
func convertToWeekKline(dayKline *protocol.KlineResp) *protocol.KlineResp {
	if len(dayKline.List) == 0 {
		return dayKline
	}

	weekResp := &protocol.KlineResp{
		List: make([]*protocol.Kline, 0),
	}

	var currentWeek *protocol.Kline
	var lastWeekDay time.Time

	for _, k := range dayKline.List {
		year, week := k.Time.ISOWeek()

		// 判断是否是新的一周
		if currentWeek == nil || lastWeekDay.Year() != year || getISOWeek(lastWeekDay) != week {
			// 保存上一周的数据
			if currentWeek != nil {
				weekResp.List = append(weekResp.List, currentWeek)
			}
			// 创建新周
			currentWeek = &protocol.Kline{
				Time:   k.Time,
				Last:   k.Last,
				Open:   k.Open,
				High:   k.High,
				Low:    k.Low,
				Close:  k.Close,
				Volume: k.Volume,
				Amount: k.Amount,
			}
		} else {
			// 累积当周数据
			if k.High > currentWeek.High {
				currentWeek.High = k.High
			}
			if k.Low < currentWeek.Low || currentWeek.Low == 0 {
				currentWeek.Low = k.Low
			}
			currentWeek.Close = k.Close
			currentWeek.Volume += k.Volume
			currentWeek.Amount += k.Amount
			currentWeek.Time = k.Time // 使用最后一天的时间
		}
		lastWeekDay = k.Time
	}

	// 添加最后一周
	if currentWeek != nil {
		weekResp.List = append(weekResp.List, currentWeek)
	}

	weekResp.Count = uint16(len(weekResp.List))
	return weekResp
}

// convertToMonthKline 将日K线转换为月K线
func convertToMonthKline(dayKline *protocol.KlineResp) *protocol.KlineResp {
	if len(dayKline.List) == 0 {
		return dayKline
	}

	monthResp := &protocol.KlineResp{
		List: make([]*protocol.Kline, 0),
	}

	var currentMonth *protocol.Kline
	var lastMonthKey string

	for _, k := range dayKline.List {
		monthKey := k.Time.Format("200601") // YYYYMM

		// 判断是否是新的一月
		if currentMonth == nil || lastMonthKey != monthKey {
			// 保存上一月的数据
			if currentMonth != nil {
				monthResp.List = append(monthResp.List, currentMonth)
			}
			// 创建新月
			currentMonth = &protocol.Kline{
				Time:   k.Time,
				Last:   k.Last,
				Open:   k.Open,
				High:   k.High,
				Low:    k.Low,
				Close:  k.Close,
				Volume: k.Volume,
				Amount: k.Amount,
			}
		} else {
			// 累积当月数据
			if k.High > currentMonth.High {
				currentMonth.High = k.High
			}
			if k.Low < currentMonth.Low || currentMonth.Low == 0 {
				currentMonth.Low = k.Low
			}
			currentMonth.Close = k.Close
			currentMonth.Volume += k.Volume
			currentMonth.Amount += k.Amount
			currentMonth.Time = k.Time // 使用最后一天的时间
		}
		lastMonthKey = monthKey
	}

	// 添加最后一月
	if currentMonth != nil {
		monthResp.List = append(monthResp.List, currentMonth)
	}

	monthResp.Count = uint16(len(monthResp.List))
	return monthResp
}

// getISOWeek 获取ISO周数
func getISOWeek(t time.Time) int {
	_, week := t.ISOWeek()
	return week
}

// 获取分时数据
func handleGetMinute(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	date := r.URL.Query().Get("date")
	if code == "" {
		errorResponse(w, "股票代码不能为空")
		return
	}

	resp, usedDate, err := getMinuteWithFallback(code, date)
	if err != nil {
		errorResponse(w, fmt.Sprintf("获取分时数据失败: %v", err))
		return
	}

	if resp == nil {
		successResponse(w, map[string]interface{}{
			"date":  usedDate,
			"Count": 0,
			"List":  []interface{}{},
		})
		return
	}

	successResponse(w, map[string]interface{}{
		"date":  usedDate,
		"Count": resp.Count,
		"List":  resp.List,
	})
}

// 获取分时成交
func handleGetTrade(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	date := r.URL.Query().Get("date")
	if code == "" {
		errorResponse(w, "股票代码不能为空")
		return
	}

	var resp *protocol.TradeResp
	var err error

	if date == "" {
		// 获取今日分时成交（最近1800条）
		resp, err = client.GetMinuteTrade(code, 0, 1800)
	} else {
		// 获取历史某天的分时成交
		resp, err = client.GetHistoryMinuteTradeDay(date, code)
	}

	if err != nil {
		errorResponse(w, fmt.Sprintf("获取分时成交失败: %v", err))
		return
	}

	successResponse(w, resp)
}

// 搜索股票代码
func handleSearchCode(w http.ResponseWriter, r *http.Request) {
	keyword := r.URL.Query().Get("keyword")
	if keyword == "" {
		errorResponse(w, "搜索关键词不能为空")
		return
	}

	// Accept both "asset_type" and legacy "type" param
	typeFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("asset_type")))
	if typeFilter == "" {
		typeFilter = strings.ToLower(strings.TrimSpace(r.URL.Query().Get("type")))
	}
	if typeFilter == "" {
		typeFilter = "all"
	}

	limit := parsePositiveInt(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}

	keywordUpper := strings.ToUpper(keyword)

	type SearchResult struct {
		Code      string  `json:"code"`
		FullCode  string  `json:"full_code"`
		Name      string  `json:"name"`
		Exchange  string  `json:"exchange"`
		AssetType string  `json:"asset_type"`
		Decimal   int8    `json:"decimal"`
		Multiple  uint16  `json:"multiple"`
		LastPrice float64 `json:"last_price"`
	}

	results := []SearchResult{}
	seen := map[string]struct{}{}

	codeModels, err := getAllCodeModels()
	if err != nil {
		errorResponse(w, "搜索失败: "+err.Error())
		return
	}

	for _, model := range codeModels {
		fullCode := model.FullCode()
		at := classifyAssetType(fullCode)

		if typeFilter != "all" {
			if at != typeFilter {
				continue
			}
		}

		if _, ok := seen[model.Code]; ok {
			continue
		}

		codeUpper := strings.ToUpper(model.Code)
		nameUpper := strings.ToUpper(model.Name)
		if strings.Contains(codeUpper, keywordUpper) || strings.Contains(nameUpper, keywordUpper) {
			results = append(results, SearchResult{
				Code:      model.Code,
				FullCode:  fullCode,
				Name:      model.Name,
				Exchange:  strings.ToLower(model.Exchange),
				AssetType: at,
				Decimal:   model.Decimal,
				Multiple:  model.Multiple,
				LastPrice: model.LastPrice,
			})
			seen[model.Code] = struct{}{}
		}

		if len(results) >= limit {
			break
		}
	}

	successResponse(w, map[string]interface{}{
		"count": len(results),
		"list":  results,
	})
}

// classifyAssetType returns "stock", "etf", "index", or "other" for a full code.
func classifyAssetType(fullCode string) string {
	switch {
	case protocol.IsStock(fullCode):
		return "stock"
	case protocol.IsETF(fullCode):
		return "etf"
	case protocol.IsIndex(fullCode):
		return "index"
	default:
		return "other"
	}
}

// 获取股票基本信息（整合多个接口）
func handleGetStockInfo(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		errorResponse(w, "股票代码不能为空")
		return
	}

	// 整合多个数据源
	result := make(map[string]interface{})

	// 1. 获取五档行情
	quotes, err := client.GetQuote(code)
	if err == nil && len(quotes) > 0 {
		result["quote"] = quotes[0]
	}

	// 2. 获取最近30天的日K线（使用前复权）
	kline, err := getQfqKlineDay(code)
	if err == nil && len(kline.List) > 30 {
		// 只返回最近30条
		kline.List = kline.List[len(kline.List)-30:]
		kline.Count = 30
	}
	if err == nil {
		result["kline_day"] = kline
	}

	// 3. 获取今日分时数据
	minute, minuteDate, err := getMinuteWithFallback(code, "")
	if err == nil && minute != nil {
		result["minute"] = map[string]interface{}{
			"date":  minuteDate,
			"Count": minute.Count,
			"List":  minute.List,
		}
	}

	successResponse(w, result)
}

func handleGetFinance(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		errorResponse(w, "股票代码不能为空")
		return
	}

	resp, err := client.GetFinanceInfo(code)
	if err != nil {
		errorResponse(w, fmt.Sprintf("获取财务数据失败: %v", err))
		return
	}

	successResponse(w, resp)
}

func handleGetF10Categories(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		errorResponse(w, "股票代码不能为空")
		return
	}

	resp, err := client.GetCompanyInfoCategory(code)
	if err != nil {
		errorResponse(w, fmt.Sprintf("获取F10目录失败: %v", err))
		return
	}

	successResponse(w, resp)
}

func handleGetF10Content(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	filename := r.URL.Query().Get("filename")
	startStr := r.URL.Query().Get("start")
	lengthStr := r.URL.Query().Get("length")

	if code == "" {
		errorResponse(w, "股票代码不能为空")
		return
	}
	if filename == "" {
		errorResponse(w, "filename不能为空")
		return
	}
	if startStr == "" || lengthStr == "" {
		errorResponse(w, "start和length不能为空")
		return
	}

	start, err := strconv.ParseUint(startStr, 10, 32)
	if err != nil {
		errorResponse(w, "start参数格式错误")
		return
	}
	length, err := strconv.ParseUint(lengthStr, 10, 32)
	if err != nil {
		errorResponse(w, "length参数格式错误")
		return
	}

	content, err := client.GetCompanyInfoContent(code, filename, uint32(start), uint32(length))
	if err != nil {
		errorResponse(w, fmt.Sprintf("获取F10正文失败: %v", err))
		return
	}

	successResponse(w, map[string]interface{}{
		"code":     protocol.AddPrefix(code),
		"filename": filename,
		"start":    uint32(start),
		"length":   uint32(length),
		"content":  content,
	})
}

func handleCreatePullKlineTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorResponse(w, "只支持POST请求")
		return
	}
	if manager == nil {
		errorResponse(w, "数据管理器未初始化")
		return
	}

	var req struct {
		Codes      []string `json:"codes"`
		IndexCodes []string `json:"index_codes"`
		AssetTypes []string `json:"asset_types"`
		Tables     []string `json:"tables"`
		Dir        string   `json:"dir"`
		Limit      int      `json:"limit"`
		StartDate  string   `json:"start_date"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, "请求参数错误: "+err.Error())
		return
	}

	tables := req.Tables
	if len(tables) == 0 {
		tables = []string{extend.Day}
	} else {
		valid := make([]string, 0, len(tables))
		for _, v := range tables {
			if _, ok := extend.KlineTableMap[v]; ok {
				valid = append(valid, v)
			}
		}
		if len(valid) == 0 {
			errorResponse(w, "tables参数无效")
			return
		}
		tables = valid
	}

	assetTypes := req.AssetTypes
	if len(assetTypes) > 0 {
		valid := make([]string, 0, len(assetTypes))
		for _, v := range assetTypes {
			switch strings.ToLower(strings.TrimSpace(v)) {
			case extend.AssetStock, extend.AssetETF, extend.AssetIndex:
				valid = append(valid, strings.ToLower(strings.TrimSpace(v)))
			}
		}
		if len(valid) == 0 {
			errorResponse(w, "asset_types参数无效")
			return
		}
		assetTypes = valid
	}

	indexCodes := make([]string, 0, len(req.IndexCodes))
	for _, code := range req.IndexCodes {
		code = strings.ToLower(strings.TrimSpace(code))
		if code == "" {
			continue
		}
		if !protocol.IsIndex(code) {
			errorResponse(w, "index_codes 参数无效，必须使用带交易所前缀的指数代码，如 sh000001 或 sz399001")
			return
		}
		indexCodes = append(indexCodes, code)
	}

	dir := req.Dir
	if dir == "" {
		dir = filepath.Join(tdx.DefaultDatabaseDir, "kline")
	}

	startAt := time.Unix(0, 0)
	if req.StartDate != "" {
		var parsed bool
		for _, layout := range []string{"2006-01-02", "20060102"} {
			if t, err := time.ParseInLocation(layout, req.StartDate, time.Local); err == nil {
				startAt = t
				parsed = true
				break
			}
		}
		if !parsed {
			errorResponse(w, "start_date格式错误，应为YYYY-MM-DD或YYYYMMDD")
			return
		}
	}

	cfg := extend.PullKlineConfig{
		Codes:      req.Codes,
		IndexCodes: indexCodes,
		AssetTypes: assetTypes,
		Tables:     tables,
		Dir:        dir,
		Limit:      req.Limit,
		StartAt:    startAt,
	}

	puller := extend.NewPullKline(cfg)

	taskID := taskManager.Run("pull_kline", func(ctx context.Context) error {
		return puller.Run(ctx, manager)
	})

	successResponse(w, map[string]string{
		"task_id": taskID,
	})
}

func handleCreatePullTradeTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorResponse(w, "只支持POST请求")
		return
	}
	if manager == nil {
		errorResponse(w, "数据管理器未初始化")
		return
	}

	var req struct {
		Code       string   `json:"code"`
		Codes      []string `json:"codes"`
		AssetTypes []string `json:"asset_types"`
		Dir        string   `json:"dir"`
		Limit      int      `json:"limit"`
		StartYear  int      `json:"start_year"`
		EndYear    int      `json:"end_year"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, "请求参数错误: "+err.Error())
		return
	}

	codes := make([]string, 0, len(req.Codes)+1)
	for _, code := range req.Codes {
		code = strings.ToLower(strings.TrimSpace(code))
		if code == "" {
			continue
		}
		if protocol.IsIndex(code) {
			errorResponse(w, "pull-trade 暂不支持指数代码，请仅传股票或ETF代码")
			return
		}
		codes = append(codes, code)
	}
	if req.Code != "" {
		code := strings.ToLower(strings.TrimSpace(req.Code))
		if protocol.IsIndex(code) {
			errorResponse(w, "pull-trade 暂不支持指数代码，请仅传股票或ETF代码")
			return
		}
		codes = append(codes, code)
	}

	assetTypes := req.AssetTypes
	if len(assetTypes) > 0 {
		valid := make([]string, 0, len(assetTypes))
		for _, v := range assetTypes {
			switch strings.ToLower(strings.TrimSpace(v)) {
			case extend.AssetStock, extend.AssetETF:
				valid = append(valid, strings.ToLower(strings.TrimSpace(v)))
			case extend.AssetIndex:
				errorResponse(w, "pull-trade 暂不支持 index 类型，请仅使用 stock 或 etf")
				return
			}
		}
		if len(valid) == 0 {
			errorResponse(w, "asset_types参数无效")
			return
		}
		assetTypes = valid
	}

	if len(codes) == 0 && len(assetTypes) == 0 {
		assetTypes = []string{extend.AssetStock}
	}

	dir := req.Dir
	if dir == "" {
		dir = filepath.Join(tdx.DefaultDatabaseDir, "trade")
	}

	puller := extend.NewPullTrade(dir)
	puller.Codes = codes
	puller.AssetTypes = assetTypes
	puller.Limit = req.Limit
	puller.StartYear = req.StartYear
	puller.EndYear = req.EndYear

	taskID := taskManager.Run("pull_trade", func(ctx context.Context) error {
		return puller.Run(ctx, manager)
	})

	successResponse(w, map[string]string{
		"task_id": taskID,
	})
}

func handleListTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errorResponse(w, "只支持GET请求")
		return
	}

	tasks := taskManager.List()
	successResponse(w, tasks)
}

func handleTaskOperations(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
	path = strings.Trim(path, "/")
	if path == "" {
		http.NotFound(w, r)
		return
	}

	parts := strings.Split(path, "/")
	id := parts[0]

	if len(parts) == 2 && parts[1] == "cancel" {
		if r.Method != http.MethodPost {
			errorResponse(w, "取消任务仅支持POST")
			return
		}
		if ok := taskManager.Cancel(id); !ok {
			errorResponse(w, "任务不存在或已结束")
			return
		}
		successResponse(w, map[string]string{
			"task_id": id,
			"status":  string(TaskStatusCancelled),
		})
		return
	}

	if r.Method != http.MethodGet {
		errorResponse(w, "只支持GET请求")
		return
	}

	if task, ok := taskManager.Get(id); ok {
		successResponse(w, task)
		return
	}

	errorResponse(w, "任务不存在")
}

func splitCodes(param string) []string {
	parts := strings.Split(param, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		code := strings.TrimSpace(p)
		if code != "" {
			result = append(result, code)
		}
	}
	return result
}

func getMinuteWithFallback(code, date string) (*protocol.MinuteResp, string, error) {
	target := strings.TrimSpace(date)
	if target == "" {
		target = time.Now().Format("20060102")
		resp, err := client.GetMinute(code)
		return resp, target, err
	}

	resp, err := client.GetHistoryMinute(target, code)
	return resp, target, err
	if date != "" {
		resp, err := client.GetHistoryMinute(date, code)
		return resp, date, err
	}

	today := time.Now()
	const maxLookback = 10

	var lastResp *protocol.MinuteResp
	var lastDate string
	var lastErr error

	for i := 0; i < maxLookback; i++ {
		currentDate := today.AddDate(0, 0, -i).Format("20060102")
		resp, err := client.GetHistoryMinute(currentDate, code)
		if err != nil {
			lastErr = err
			continue
		}
		if resp != nil {
			if len(resp.List) > 0 && resp.Count > 0 {
				return resp, currentDate, nil
			}
			if lastResp == nil {
				lastResp = resp
				lastDate = currentDate
			}
		}
	}

	if lastResp != nil {
		return lastResp, lastDate, nil
	}

	return nil, "", lastErr
}

func main() {
	// 静态文件服务
	http.Handle("/", http.FileServer(http.Dir("./static")))

	// API路由
	http.HandleFunc("/api/quote", handleGetQuote)
	http.HandleFunc("/api/kline", handleGetKline)
	http.HandleFunc("/api/minute", handleGetMinute)
	http.HandleFunc("/api/trade", handleGetTrade)
	http.HandleFunc("/api/search", handleSearchCode)
	http.HandleFunc("/api/profile", handleGetProfile)
	http.HandleFunc("/api/security/status", handleSecurityStatus)
	http.HandleFunc("/api/stock-info", handleGetStockInfo)
	http.HandleFunc("/api/finance", handleGetFinance)
	http.HandleFunc("/api/f10/categories", handleGetF10Categories)
	http.HandleFunc("/api/f10/content", handleGetF10Content)
	http.HandleFunc("/api/codes", handleGetCodes)
	http.HandleFunc("/api/batch-quote", handleBatchQuote)
	http.HandleFunc("/api/kline-history", handleGetKlineHistory)
	http.HandleFunc("/api/index", handleGetIndex)
	http.HandleFunc("/api/index/all", handleGetIndexAll)
	http.HandleFunc("/api/market-stats", handleGetMarketStats)
	http.HandleFunc("/api/market/screen", handleMarketScreen)
	http.HandleFunc("/api/market/signal", handleMarketSignal)
	http.HandleFunc("/api/market-count", handleGetMarketCount)
	http.HandleFunc("/api/stock-codes", handleGetStockCodes)
	http.HandleFunc("/api/etf-codes", handleGetETFCodes)
	http.HandleFunc("/api/index-codes", handleGetIndexCodes)
	http.HandleFunc("/api/server-status", handleGetServerStatus)
	http.HandleFunc("/api/health", handleHealthCheck)
	http.HandleFunc("/api/collector/status", handleCollectorStatus)
	http.HandleFunc("/api/collector/reconcile", handleCollectorReconcile)
	http.HandleFunc("/api/etf", handleGetETFList)
	http.HandleFunc("/api/trade-history", handleGetTradeHistory)
	http.HandleFunc("/api/order-history", handleGetOrderHistory)
	http.HandleFunc("/api/trade-history/full", handleGetTradeHistoryFull)
	http.HandleFunc("/api/minute-trade-all", handleGetMinuteTradeAll)
	http.HandleFunc("/api/kline-all", handleGetKlineAllTDX)
	http.HandleFunc("/api/kline-all/tdx", handleGetKlineAllTDX)
	http.HandleFunc("/api/kline-all/ths", handleGetKlineAllTHS)
	http.HandleFunc("/api/workday", handleGetWorkday)
	http.HandleFunc("/api/workday/range", handleGetWorkdayRange)
	http.HandleFunc("/api/income", handleGetIncome)
	http.HandleFunc("/api/blocks", handleGetBlocks)
	http.HandleFunc("/api/block/members", handleGetBlockMembers)
	http.HandleFunc("/api/stock/blocks", handleGetStockBlocks)
	http.HandleFunc("/api/block/ranking", handleBlockRanking)
	http.HandleFunc("/api/block/stocks", handleBlockStocks)
	http.HandleFunc("/api/ticker/status", handleTickerStatus)
	http.HandleFunc("/api/tasks/pull-kline", handleCreatePullKlineTask)
	http.HandleFunc("/api/tasks/pull-trade", handleCreatePullTradeTask)
	http.HandleFunc("/api/tasks", handleListTasks)
	http.HandleFunc("/api/tasks/", handleTaskOperations)

	port := ":8080"
	log.Printf("服务启动成功，访问 http://localhost%s\n", port)
	log.Fatal(http.ListenAndServe(port, nil))
}
