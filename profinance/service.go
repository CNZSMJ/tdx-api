package profinance

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultBaseURL          = "https://data.tdx.com.cn/tdxfin"
	defaultUserAgent        = "Mozilla/5.0 (compatible; nexus-fi-tdx-api/1.0; +https://data.tdx.com.cn)"
	listCacheTTL            = 6 * time.Hour
	defaultPrefetchSchedule = "0 0 9 * * *"
	defaultPrefetchRetry    = 5 * time.Minute

	fieldBookValuePerShare = 4
	fieldTotalShares       = 238
	fieldFloatAShares      = 239
	fieldNetProfitTTM      = 276
	fieldWeightedROE       = 281
	fieldRevenueTTM        = 283

	minTrackedFieldID = fieldRevenueTTM
)

type Config struct {
	BaseURL              string
	UserAgent            string
	HTTPClient           *http.Client
	Now                  func() time.Time
	DisableAutoPrefetch  bool
	AutoPrefetchSchedule string
	AutoPrefetchRetry    time.Duration
}

type ReportFile struct {
	Filename   string
	Hash       string
	Filesize   int64
	ReportDate string
}

type Snapshot struct {
	Code              string
	ReportDate        string
	BookValuePerShare float64
	TotalShares       float64
	FloatAShares      float64
	NetProfitTTM      float64
	RevenueTTMYuan    float64
	WeightedROE       float64
	SourceReportFile  string
}

type Service struct {
	baseURL      string
	userAgent    string
	cacheDir     string
	rootDir      string
	artifactsDir string
	zipDir       string
	dbPath       string
	httpClient   *http.Client
	now          func() time.Time
	registry     *Registry

	mu                   sync.RWMutex
	listFetchedAt        time.Time
	reportFiles          []ReportFile
	reportCache          map[string]map[string]Snapshot
	codeCache            map[string]Snapshot
	syncMu               sync.Mutex
	dbOnce               sync.Once
	db                   *sql.DB
	dbErr                error
	task                 cronStopper
	startupPrefetchWG    sync.WaitGroup
	prefetchRetry        time.Duration
	autoPrefetchSchedule string
	disableAutoPrefetch  bool
}

type SourceWatermarkState struct {
	Found                    bool
	ManifestFetchedAt        string
	LatestReportDateSeen     string
	LatestReportDateIngested string
	WatermarkDate            string
	UpdatedAt                string
}

type cronStopper interface {
	Stop() context.Context
}

func NewService(cacheDir string, cfg Config) *Service {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	userAgent := strings.TrimSpace(cfg.UserAgent)
	if userAgent == "" {
		userAgent = defaultUserAgent
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	if cacheDir == "" {
		cacheDir = filepath.Join(os.TempDir(), "tdx-profinance")
	}
	rootDir := cacheDir

	autoPrefetchSchedule := strings.TrimSpace(cfg.AutoPrefetchSchedule)
	if autoPrefetchSchedule == "" {
		autoPrefetchSchedule = defaultPrefetchSchedule
	}
	autoPrefetchRetry := cfg.AutoPrefetchRetry
	if autoPrefetchRetry <= 0 {
		autoPrefetchRetry = defaultPrefetchRetry
	}

	svc := &Service{
		baseURL:              baseURL,
		userAgent:            userAgent,
		cacheDir:             cacheDir,
		rootDir:              rootDir,
		artifactsDir:         filepath.Join(rootDir, "artifacts"),
		zipDir:               filepath.Join(rootDir, "artifacts", "zips"),
		dbPath:               filepath.Join(rootDir, "prof_finance.db"),
		httpClient:           httpClient,
		now:                  now,
		registry:             DefaultRegistry(),
		reportCache:          make(map[string]map[string]Snapshot),
		codeCache:            make(map[string]Snapshot),
		prefetchRetry:        autoPrefetchRetry,
		autoPrefetchSchedule: autoPrefetchSchedule,
		disableAutoPrefetch:  cfg.DisableAutoPrefetch,
	}
	svc.startAutoPrefetch()
	return svc
}

func (s *Service) latestForCodeFromZIP(ctx context.Context, code string) (*Snapshot, error) {
	normalized := normalizeCode(code)
	if normalized == "" {
		return nil, errors.New("professional finance requires code")
	}

	s.mu.RLock()
	if cached, ok := s.codeCache[normalized]; ok {
		s.mu.RUnlock()
		copy := cached
		return &copy, nil
	}
	s.mu.RUnlock()

	reports, err := s.listReports(ctx, false)
	if err != nil {
		return nil, err
	}

	nowDate := s.now().Format("20060102")
	for _, report := range reports {
		if report.ReportDate == "" || report.ReportDate > nowDate {
			continue
		}
		rows, err := s.loadReport(ctx, report)
		if err != nil {
			continue
		}
		if snapshot, ok := rows[normalized]; ok {
			s.mu.Lock()
			s.codeCache[normalized] = snapshot
			s.mu.Unlock()
			copy := snapshot
			return &copy, nil
		}
	}

	return nil, fmt.Errorf("professional finance snapshot not found for code %s", normalized)
}

func (s *Service) listForCodeFromZIP(ctx context.Context, code string, limit int, startDate, endDate string) ([]Snapshot, error) {
	normalized := normalizeCode(code)
	if normalized == "" {
		return nil, errors.New("professional finance requires code")
	}
	if limit <= 0 {
		limit = 8
	}

	reports, err := s.listReports(ctx, false)
	if err != nil {
		return nil, err
	}

	nowDate := s.now().Format("20060102")
	out := make([]Snapshot, 0, limit)
	for _, report := range reports {
		if report.ReportDate == "" || report.ReportDate > nowDate {
			continue
		}
		if startDate != "" && report.ReportDate < startDate {
			continue
		}
		if endDate != "" && report.ReportDate > endDate {
			continue
		}

		rows, err := s.loadReport(ctx, report)
		if err != nil {
			continue
		}
		snapshot, ok := rows[normalized]
		if !ok {
			continue
		}
		out = append(out, snapshot)
		if len(out) >= limit {
			break
		}
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("professional finance reports not found for code %s", normalized)
	}
	return out, nil
}

func (s *Service) listReports(ctx context.Context, forceRefresh bool) ([]ReportFile, error) {
	s.mu.RLock()
	if !forceRefresh && len(s.reportFiles) > 0 && s.now().Sub(s.listFetchedAt) < listCacheTTL {
		out := append([]ReportFile(nil), s.reportFiles...)
		s.mu.RUnlock()
		return out, nil
	}
	s.mu.RUnlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/gpcw.txt", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", s.userAgent)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch report list status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.artifactsDir, 0o755); err == nil {
		_ = os.WriteFile(filepath.Join(s.artifactsDir, "gpcw.txt"), body, 0o644)
	}
	reports, err := parseReportList(body)
	if err != nil {
		return nil, err
	}
	sort.Slice(reports, func(i, j int) bool {
		return reports[i].ReportDate > reports[j].ReportDate
	})

	s.mu.Lock()
	s.listFetchedAt = s.now()
	s.reportFiles = append([]ReportFile(nil), reports...)
	s.reportCache = make(map[string]map[string]Snapshot)
	s.codeCache = make(map[string]Snapshot)
	s.mu.Unlock()

	return reports, nil
}

func (s *Service) PrefetchAll(ctx context.Context) error {
	if err := s.Sync(ctx); err != nil {
		return err
	}
	log.Printf("profinance: synced professional finance artifacts and serving data")
	return nil
}

func (s *Service) SyncIfNeeded(ctx context.Context) (bool, string, error) {
	needsSync, reason, err := s.NeedsSync(ctx)
	if err != nil {
		return false, "", err
	}
	if !needsSync {
		return false, reason, nil
	}
	if err := s.Sync(ctx); err != nil {
		return false, reason, err
	}
	return true, reason, nil
}

func (s *Service) NeedsSync(ctx context.Context) (bool, string, error) {
	db, err := s.openDB()
	if err != nil {
		return false, "", err
	}
	state, err := s.sourceWatermarkState(ctx, db)
	if err != nil {
		return false, "", err
	}

	reports, err := s.listReports(ctx, true)
	if err != nil {
		return false, "", err
	}
	today := s.now().UTC().Format("20060102")
	latestVisible := latestVisibleReportDate(reports, today)

	switch {
	case !state.Found:
		return true, "source_watermark_missing", nil
	case latestVisible != "" && latestVisible > strings.TrimSpace(state.LatestReportDateIngested):
		return true, "source_report_advanced", nil
	case strings.TrimSpace(state.WatermarkDate) < today:
		return true, "serving_watermark_stale", nil
	default:
		return false, "up_to_date", nil
	}
}

func (s *Service) sourceWatermarkState(ctx context.Context, db *sql.DB) (*SourceWatermarkState, error) {
	state := &SourceWatermarkState{}
	err := db.QueryRowContext(ctx, `
SELECT manifest_fetched_at, latest_report_date_seen, latest_report_date_ingested, watermark_date, updated_at
FROM prof_finance_source_watermark
WHERE source_name = ?`, "gpcw").Scan(
		&state.ManifestFetchedAt,
		&state.LatestReportDateSeen,
		&state.LatestReportDateIngested,
		&state.WatermarkDate,
		&state.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	state.Found = true
	return state, nil
}

func latestVisibleReportDate(reports []ReportFile, nowDate string) string {
	latest := ""
	for _, report := range reports {
		if report.ReportDate == "" || report.ReportDate > nowDate {
			continue
		}
		if report.ReportDate > latest {
			latest = report.ReportDate
		}
	}
	return latest
}

func (s *Service) startAutoPrefetch() {
	if s == nil || s.disableAutoPrefetch {
		return
	}

	s.startupPrefetchWG.Add(1)
	go func() {
		defer s.startupPrefetchWG.Done()
		if err := s.PrefetchAll(context.Background()); err != nil {
			log.Printf("profinance: startup prefetch failed: %v", err)
		}
	}()
}

func (s *Service) Close() {
	if s == nil {
		return
	}
	if s.task != nil {
		ctx := s.task.Stop()
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
		s.task = nil
	}
	s.startupPrefetchWG.Wait()
	if s.db != nil {
		_ = s.db.Close()
		s.db = nil
	}
}

func (s *Service) loadReport(ctx context.Context, report ReportFile) (map[string]Snapshot, error) {
	s.mu.RLock()
	if cached, ok := s.reportCache[report.Filename]; ok {
		s.mu.RUnlock()
		return cached, nil
	}
	s.mu.RUnlock()

	bs, err := s.ensureReportZip(ctx, report)
	if err != nil {
		return nil, err
	}
	rows, err := parseZipReport(bs, report)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.reportCache[report.Filename] = rows
	s.mu.Unlock()
	return rows, nil
}

func (s *Service) ensureReportZip(ctx context.Context, report ReportFile) ([]byte, error) {
	if err := os.MkdirAll(s.zipDir, 0o755); err != nil {
		return nil, err
	}

	filename := filepath.Join(s.zipDir, report.Filename)
	hashFilename := filename + ".hash"
	if bs, err := os.ReadFile(filename); err == nil {
		hashMatches := true
		if strings.TrimSpace(report.Hash) != "" {
			hashMatches = false
			if hashValue, hashErr := os.ReadFile(hashFilename); hashErr == nil && strings.TrimSpace(string(hashValue)) == strings.TrimSpace(report.Hash) {
				hashMatches = true
			}
		}
		if hashMatches && validateZip(bs) == nil {
			return bs, nil
		}
		_ = os.Remove(filename)
		_ = os.Remove(hashFilename)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/"+report.Filename, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", s.userAgent)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch report %s status %d", report.Filename, resp.StatusCode)
	}
	bs, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := validateZip(bs); err != nil {
		bs, err = s.downloadReportWithWget(ctx, report)
		if err != nil {
			return nil, fmt.Errorf("invalid report zip %s via http and wget fallback failed: %w", report.Filename, err)
		}
	}

	tmp := filename + ".tmp"
	if err := os.WriteFile(tmp, bs, 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, filename); err != nil {
		return nil, err
	}
	if strings.TrimSpace(report.Hash) != "" {
		_ = os.WriteFile(hashFilename, []byte(strings.TrimSpace(report.Hash)), 0o644)
	}
	return bs, nil
}

func (s *Service) downloadReportWithWget(ctx context.Context, report ReportFile) ([]byte, error) {
	cmd := exec.CommandContext(
		ctx,
		"wget",
		"-qO-",
		"--user-agent="+s.userAgent,
		s.baseURL+"/"+report.Filename,
	)
	bs, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	if err := validateZip(bs); err != nil {
		return nil, err
	}
	return bs, nil
}

func parseReportList(body []byte) ([]ReportFile, error) {
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	out := make([]ReportFile, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) != 3 {
			return nil, fmt.Errorf("invalid report list row: %s", line)
		}
		size, err := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid report filesize %q: %w", parts[2], err)
		}
		filename := strings.TrimSpace(parts[0])
		out = append(out, ReportFile{
			Filename:   filename,
			Hash:       strings.TrimSpace(parts[1]),
			Filesize:   size,
			ReportDate: deriveReportDate(filename),
		})
	}
	return out, nil
}

func validateZip(bs []byte) error {
	if len(bs) == 0 {
		return errors.New("empty body")
	}
	_, err := zip.NewReader(bytes.NewReader(bs), int64(len(bs)))
	return err
}

func parseZipReport(bs []byte, report ReportFile) (map[string]Snapshot, error) {
	reader, err := zip.NewReader(bytes.NewReader(bs), int64(len(bs)))
	if err != nil {
		return nil, err
	}

	for _, file := range reader.File {
		if !strings.HasSuffix(strings.ToLower(file.Name), ".dat") {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return nil, err
		}
		raw, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return nil, err
		}
		return parseDATReport(raw, report)
	}

	return nil, fmt.Errorf("zip %s has no dat file", report.Filename)
}

func parseDATReport(data []byte, report ReportFile) (map[string]Snapshot, error) {
	if len(data) < 20 {
		return nil, errors.New("dat report too short")
	}

	var header struct {
		_          int16
		ReportDate uint32
		Count      uint16
		_          uint32
		ReportSize uint32
		_          uint32
	}
	if err := binary.Read(bytes.NewReader(data[:20]), binary.LittleEndian, &header); err != nil {
		return nil, err
	}
	if header.ReportSize == 0 {
		return map[string]Snapshot{}, nil
	}
	if header.ReportSize/4 < minTrackedFieldID {
		return nil, fmt.Errorf("report fields too short: %d", header.ReportSize/4)
	}

	const headerSize = 20
	const stockItemSize = 11

	out := make(map[string]Snapshot, header.Count)
	for i := 0; i < int(header.Count); i++ {
		offset := headerSize + i*stockItemSize
		if offset+stockItemSize > len(data) {
			return nil, fmt.Errorf("stock header out of range at index %d", i)
		}
		code := strings.TrimSpace(string(data[offset : offset+6]))
		if code == "" {
			continue
		}
		foa := int(binary.LittleEndian.Uint32(data[offset+7 : offset+11]))
		if foa <= 0 || foa+int(header.ReportSize) > len(data) {
			continue
		}

		snapshot := Snapshot{
			Code:              code,
			ReportDate:        deriveReportDate(report.Filename),
			BookValuePerShare: readFieldFloat(data, foa, fieldBookValuePerShare),
			TotalShares:       readFieldFloat(data, foa, fieldTotalShares),
			FloatAShares:      readFieldFloat(data, foa, fieldFloatAShares),
			NetProfitTTM:      readFieldFloat(data, foa, fieldNetProfitTTM),
			RevenueTTMYuan:    readFieldFloat(data, foa, fieldRevenueTTM) * 10000,
			WeightedROE:       readFieldFloat(data, foa, fieldWeightedROE),
			SourceReportFile:  report.Filename,
		}
		if snapshot.ReportDate == "" && header.ReportDate != 0 {
			snapshot.ReportDate = fmt.Sprintf("%08d", header.ReportDate)
		}
		out[code] = snapshot
	}
	return out, nil
}

func readFieldFloat(data []byte, rowOffset int, fieldID int) float64 {
	if fieldID <= 0 {
		return 0
	}
	fieldOffset := rowOffset + (fieldID-1)*4
	if fieldOffset+4 > len(data) {
		return 0
	}
	bits := binary.LittleEndian.Uint32(data[fieldOffset : fieldOffset+4])
	return float64(math.Float32frombits(bits))
}

func deriveReportDate(filename string) string {
	name := strings.TrimSpace(strings.ToLower(filename))
	if !strings.HasPrefix(name, "gpcw") || len(name) < len("gpcw")+8 {
		return ""
	}
	date := filename[4:12]
	if len(date) != 8 {
		return ""
	}
	return date
}

func normalizeCode(code string) string {
	clean := strings.TrimSpace(strings.ToLower(code))
	for _, prefix := range []string{"sh", "sz", "bj"} {
		if strings.HasPrefix(clean, prefix) && len(clean) > 2 {
			return clean[2:]
		}
	}
	return clean
}
