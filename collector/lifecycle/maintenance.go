package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const defaultLifecycleDataset = "a-stock-market-tdx"

var errLifecycleRuntimeBudgetExhausted = errors.New("runtime budget exhausted")

func (r MaintenanceRunner) RunWithContext(ctx context.Context) (MaintenanceResult, error) {
	if r.Manifest == nil || r.Storage == nil || strings.TrimSpace(r.DataDir) == "" {
		return r.planningOnlyRun()
	}
	if !r.Enable {
		return MaintenanceResult{Status: "skipped", Reason: "TDX_LIFECYCLE_ENABLE is not enabled"}, nil
	}
	if _, err := RecoverInterruptedPruningSegments(r.Manifest); err != nil {
		return MaintenanceResult{}, err
	}
	if r.HigherPriorityActive {
		return MaintenanceResult{Status: "skipped", Reason: "higher-priority governance job active"}, nil
	}
	if r.RuntimeBudget <= 0 {
		return MaintenanceResult{Status: "skipped", Reason: "runtime budget exhausted or not configured"}, nil
	}
	if r.StartedAt.IsZero() {
		r.StartedAt = time.Now()
	}

	cutoffs, err := r.resolveHotCutoffs()
	if err != nil {
		return MaintenanceResult{}, err
	}
	defaultCutoff := cutoffs.Default
	freeBytes := r.FreeBytes
	if freeBytes <= 0 {
		freeBytes, err = diskFreeBytes(r.DataDir)
		if err != nil {
			return MaintenanceResult{}, err
		}
	}
	gate := SteadyStateGate{
		FreeBytes:                      freeBytes,
		WriteWatermarkBytes:            r.WriteWatermarkBytes,
		HigherPriorityGovernanceActive: r.HigherPriorityActive,
	}.Decide()
	if !gate.Allowed {
		return MaintenanceResult{Status: "skipped", Reason: gate.Reason, HotCutoffDate: defaultCutoff}, nil
	}

	candidates, err := r.resolveCandidates(ctx, cutoffs)
	if err != nil {
		return MaintenanceResult{}, err
	}
	plan, err := SelectLifecycleCandidates(candidates, SchedulerOptions{
		FreeBytes:         freeBytes,
		SafetyMarginBytes: r.SafetyMarginBytes,
		MaxCandidates:     effectiveMaxCandidates(r.MaxCandidates, len(candidates)),
	})
	if err != nil {
		return MaintenanceResult{}, err
	}
	result := MaintenanceResult{
		Status:             "passed",
		SelectedCandidates: len(plan.Selected),
		SkippedCandidates:  len(plan.Skipped),
		HotCutoffDate:      defaultCutoff,
		LifecycleDebt:      LifecycleDebt{Skipped: len(plan.Skipped)},
	}
	if status, err := LifecycleStatusFromManifest(r.Manifest); err == nil {
		result.LifecycleDebt.FailedSegments = status.SegmentCounts[string(SegmentFailed)]
	}

	if err := r.processCandidateGroups(ctx, groupCandidatesByDB(plan.Selected), defaultCutoff, &result); err != nil {
		return result, err
	}
	if result.ProcessedSegments == 0 && len(plan.Selected) == 0 {
		result.Status = "skipped"
		result.Reason = "no lifecycle candidates"
	}
	return result, nil
}

type processedSegment struct {
	Export        SegmentExportResult
	Pruned        bool
	BytesReleased int64
}

type archivedSegment struct {
	SegmentID   string
	BatchID     string
	Candidate   LifecycleCandidate
	Export      SegmentExportResult
	PruneCutoff string
}

type candidateGroupResult struct {
	Segments []processedSegment
	Err      error
}

func (r MaintenanceRunner) processCandidateGroups(ctx context.Context, groups [][]LifecycleCandidate, cutoff string, result *MaintenanceResult) error {
	if r.processWorkers() <= 1 {
		return r.processCandidateGroupsSerial(ctx, groups, cutoff, result)
	}
	return r.processCandidateGroupsParallel(ctx, groups, cutoff, result)
}

func (r MaintenanceRunner) processCandidateGroupsSerial(ctx context.Context, groups [][]LifecycleCandidate, cutoff string, result *MaintenanceResult) error {
	for _, group := range groups {
		if err := ctx.Err(); err != nil {
			result.Status = "interrupted"
			result.Reason = err.Error()
			return err
		}
		if time.Since(r.StartedAt) > r.RuntimeBudget {
			result.Status = "partial"
			result.Reason = errLifecycleRuntimeBudgetExhausted.Error()
			return nil
		}
		segments, err := r.processCandidateGroup(ctx, group, cutoff)
		r.applyCandidateGroupResult(result, candidateGroupResult{Segments: segments, Err: err})
	}
	return nil
}

func (r MaintenanceRunner) processCandidateGroupsParallel(ctx context.Context, groups [][]LifecycleCandidate, cutoff string, result *MaintenanceResult) error {
	jobs := make(chan []LifecycleCandidate, len(groups))
	results := make(chan candidateGroupResult, len(groups))
	workers := r.processWorkers()
	if workers > len(groups) {
		workers = len(groups)
	}
	for _, group := range groups {
		jobs <- group
	}
	close(jobs)

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for group := range jobs {
				if err := ctx.Err(); err != nil {
					results <- candidateGroupResult{Err: err}
					continue
				}
				if time.Since(r.StartedAt) > r.RuntimeBudget {
					results <- candidateGroupResult{Err: errLifecycleRuntimeBudgetExhausted}
					continue
				}
				segments, err := r.processCandidateGroup(ctx, group, cutoff)
				results <- candidateGroupResult{Segments: segments, Err: err}
			}
		}()
	}
	wg.Wait()
	close(results)

	var interruptErr error
	for groupResult := range results {
		if errors.Is(groupResult.Err, context.Canceled) || errors.Is(groupResult.Err, context.DeadlineExceeded) {
			interruptErr = groupResult.Err
		}
		r.applyCandidateGroupResult(result, groupResult)
	}
	if interruptErr != nil {
		result.Status = "interrupted"
		result.Reason = interruptErr.Error()
		return interruptErr
	}
	return nil
}

func (r MaintenanceRunner) applyCandidateGroupResult(result *MaintenanceResult, groupResult candidateGroupResult) {
	if groupResult.Err != nil {
		if errors.Is(groupResult.Err, errLifecycleRuntimeBudgetExhausted) {
			result.Status = "partial"
			if result.Reason == "" {
				result.Reason = errLifecycleRuntimeBudgetExhausted.Error()
			}
			return
		}
		result.Status = "partial"
		if result.Reason == "" {
			result.Reason = groupResult.Err.Error()
		}
		return
	}
	for _, segment := range groupResult.Segments {
		result.ProcessedSegments++
		result.RowsArchived += segment.Export.RowCount
		if segment.Pruned {
			result.PrunedSegments++
			result.BytesReleased += segment.BytesReleased
		} else if r.AllowPrune {
			result.Status = "partial"
			result.Reason = "candidate exported and restore-tested but not pruned"
		} else {
			result.Status = "partial"
			result.Reason = "hot pruning disabled"
		}
	}
}

func (r MaintenanceRunner) processCandidate(ctx context.Context, candidate LifecycleCandidate, cutoff string) (processedSegment, error) {
	segments, err := r.processCandidateGroup(ctx, []LifecycleCandidate{candidate}, cutoff)
	if err != nil {
		return processedSegment{}, err
	}
	if len(segments) == 0 {
		return processedSegment{}, errors.New("candidate produced no segment")
	}
	return segments[0], nil
}

func (r MaintenanceRunner) processCandidateGroup(ctx context.Context, candidates []LifecycleCandidate, cutoff string) ([]processedSegment, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dbPath := strings.TrimSpace(candidates[0].DBPath)
	if dbPath == "" {
		return nil, errors.New("candidate db path is required")
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.DBPath) != dbPath {
			return nil, errors.New("candidate group must target the same db path")
		}
	}
	batchID := r.BatchID
	if batchID == "" {
		batchID = fmt.Sprintf("lifecycle-%d", time.Now().UnixNano())
	}
	lease, err := AcquireFileLease(dbPath)
	if err != nil {
		return nil, err
	}
	defer lease.Release()

	archived := make([]archivedSegment, 0, len(candidates))
	processed := make([]processedSegment, 0, len(candidates))
	for _, candidate := range candidates {
		candidateCutoff := candidateHotCutoff(candidate, cutoff)
		segment, err := r.archiveCandidateLocked(ctx, candidate, candidateCutoff, batchID)
		if err != nil {
			return processed, err
		}
		archived = append(archived, segment)
		processed = append(processed, processedSegment{Export: segment.Export})
	}
	if !r.AllowPrune {
		return processed, nil
	}
	eligible, err := r.Manifest.CountSegmentsByStatus(SegmentRestoreTested, SegmentPruning, SegmentHotPruned, SegmentActive)
	if err != nil {
		return processed, err
	}
	if eligible < r.minVerifiedSegments() {
		return processed, nil
	}
	var released int64
	if len(archived) == 1 {
		segment := archived[0]
		released, err = r.pruneCandidate(segment.SegmentID, segment.BatchID, segment.Candidate, segment.PruneCutoff)
	} else {
		released, err = r.pruneCandidateGroup(batchID, archived)
	}
	if err != nil {
		for _, segment := range archived {
			_ = r.Manifest.UpdateSegmentStatus(segment.SegmentID, SegmentFailed, err.Error())
		}
		return processed, err
	}
	for i := range processed {
		processed[i].Pruned = true
	}
	if len(processed) > 0 {
		processed[0].BytesReleased = released
	}
	return processed, nil
}

func (r MaintenanceRunner) archiveCandidateLocked(ctx context.Context, candidate LifecycleCandidate, cutoff, batchID string) (archivedSegment, error) {
	if err := ctx.Err(); err != nil {
		return archivedSegment{}, err
	}
	if strings.TrimSpace(candidate.DBPath) == "" {
		return archivedSegment{}, errors.New("candidate db path is required")
	}
	startDate := strings.TrimSpace(candidate.MinDate)
	if startDate == "" {
		startDate = "00000101"
	}
	endDate := previousCalendarDate(cutoff)
	if candidate.MaxDate != "" && candidate.MaxDate < endDate {
		endDate = candidate.MaxDate
	}
	endDate = r.capArchiveEndDate(startDate, endDate)
	if startDate > endDate {
		return archivedSegment{}, fmt.Errorf("candidate has no cold rows before cutoff: %s %s cutoff=%s", candidate.DBPath, candidate.TableName, cutoff)
	}
	pruneCutoff := nextCalendarDate(endDate)

	segmentID := lifecycleSegmentID(batchID, candidate, startDate, endDate)
	uri := coldURIForCandidate(r.dataset(), candidate, startDate, endDate, batchID)
	if err := r.Manifest.CreateSegment(ColdSegment{
		SegmentID:            segmentID,
		DatasetID:            r.dataset(),
		Domain:               candidate.Domain,
		TableName:            candidate.TableName,
		Instrument:           candidate.Instrument,
		PartitionKey:         coldPartitionKeyForCandidate(candidate, startDate),
		StartDate:            startDate,
		EndDate:              endDate,
		ColdURI:              uri,
		ArchiveBatchID:       batchID,
		Status:               SegmentPlanned,
		SchemaVersion:        1,
		StorageScheme:        "local-v1",
		FinalizationStrategy: "atomic_rename_same_device",
		SourceDBPath:         candidate.DBPath,
		SourceDBSize:         candidate.SourceDBBytes,
		SourceSelectionHash:  sourceSelectionHash(candidate, startDate, endDate),
	}); err != nil {
		return archivedSegment{}, err
	}

	if err := r.Manifest.UpdateSegmentStatus(segmentID, SegmentExporting, "export started"); err != nil {
		return archivedSegment{}, err
	}
	export, err := ExportSegment(ExportRequest{
		SourceDBPath: candidate.DBPath,
		TableName:    candidate.TableName,
		Domain:       candidate.Domain,
		Instrument:   candidate.Instrument,
		StartDate:    startDate,
		EndDate:      endDate,
		Storage:      r.Storage,
		ColdURI:      uri,
	})
	if err != nil {
		_ = r.Manifest.UpdateSegmentStatus(segmentID, SegmentFailed, err.Error())
		return archivedSegment{}, err
	}
	if export.RowCount == 0 {
		_ = r.Manifest.UpdateSegmentStatus(segmentID, SegmentFailed, "export produced zero rows")
		return archivedSegment{}, errors.New("export produced zero rows")
	}
	if err := r.Manifest.UpdateSegmentArchiveMetadata(segmentID, export); err != nil {
		return archivedSegment{}, err
	}
	if err := r.Manifest.UpdateSegmentStatus(segmentID, SegmentExported, "export completed"); err != nil {
		return archivedSegment{}, err
	}
	if err := r.Manifest.UpdateSegmentStatus(segmentID, SegmentVerifying, "verify started"); err != nil {
		return archivedSegment{}, err
	}
	if err := VerifySegment(export); err != nil {
		_ = r.Manifest.UpdateSegmentStatus(segmentID, SegmentFailed, err.Error())
		return archivedSegment{}, err
	}
	if err := r.Manifest.UpdateSegmentStatus(segmentID, SegmentVerified, "verify completed"); err != nil {
		return archivedSegment{}, err
	}
	if err := r.Manifest.UpdateSegmentStatus(segmentID, SegmentRestoreTesting, "restore test started"); err != nil {
		return archivedSegment{}, err
	}
	restorePath := r.restorePath(segmentID, candidate.TableName)
	restore, err := RestoreSegment(export, restorePath)
	if err != nil {
		_ = r.Manifest.UpdateSegmentStatus(segmentID, SegmentFailed, err.Error())
		return archivedSegment{}, err
	}
	if restore.RowCount != export.RowCount {
		err := fmt.Errorf("restore row count mismatch: got %d want %d", restore.RowCount, export.RowCount)
		_ = r.Manifest.UpdateSegmentStatus(segmentID, SegmentFailed, err.Error())
		return archivedSegment{}, err
	}
	if err := r.Manifest.CreateRestore(LifecycleRestore{
		RestoreID:   segmentID + ":restore-test",
		SegmentID:   segmentID,
		RestoreMode: "temporary_query_restore",
		Status:      "passed",
		TargetPath:  restorePath,
		ExpiresAt:   time.Now().Add(24 * time.Hour),
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}); err != nil {
		return archivedSegment{}, err
	}
	if err := r.Manifest.UpdateSegmentStatus(segmentID, SegmentRestoreTested, "restore test completed"); err != nil {
		return archivedSegment{}, err
	}
	if !r.KeepRestoreTestDB {
		if err := os.Remove(restorePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = r.Manifest.UpdateSegmentStatus(segmentID, SegmentFailed, err.Error())
			return archivedSegment{}, err
		}
	}
	return archivedSegment{SegmentID: segmentID, BatchID: batchID, Candidate: candidate, Export: export, PruneCutoff: pruneCutoff}, nil
}

func (r MaintenanceRunner) pruneCandidate(segmentID, batchID string, candidate LifecycleCandidate, cutoff string) (int64, error) {
	if err := r.Manifest.UpdateSegmentStatus(segmentID, SegmentPruning, "hot pruning started"); err != nil {
		return 0, err
	}
	replacement := candidate.DBPath + ".replacement." + batchID + ".tmp"
	rebuild, err := BuildReplacementHotDB(RebuildRequest{
		SourceDBPath:      candidate.DBPath,
		ReplacementDBPath: replacement,
		TableName:         candidate.TableName,
		HotCutoffDate:     cutoff,
	})
	if err != nil {
		return 0, err
	}
	if rebuild.PrunedRows <= 0 {
		return 0, fmt.Errorf("replacement would prune no rows for %s %s", candidate.DBPath, candidate.TableName)
	}
	if err := verifyHotRetainedRowsForTables(candidate.DBPath, replacement, map[string]string{candidate.TableName: cutoff}, false); err != nil {
		return 0, err
	}
	replace, err := AtomicReplaceVerifiedHotDB(candidate.DBPath, replacement, batchID)
	if err != nil {
		return 0, err
	}
	if err := verifyHotRetainedRowsForTables(replace.BackupPath, candidate.DBPath, map[string]string{candidate.TableName: cutoff}, false); err != nil {
		_ = RollbackHotDBReplacement(candidate.DBPath, replace.BackupPath)
		return 0, err
	}
	if err := r.Manifest.UpdateSegmentStatus(segmentID, SegmentHotPruned, "hot db replacement verified"); err != nil {
		return 0, err
	}
	var released int64
	if !r.KeepBackup {
		backupStat, statErr := os.Stat(replace.BackupPath)
		if statErr != nil {
			return 0, statErr
		}
		newStat, statErr := os.Stat(candidate.DBPath)
		if statErr != nil {
			return 0, statErr
		}
		if backupStat.Size() > newStat.Size() {
			released = backupStat.Size() - newStat.Size()
		}
		if err := os.Remove(replace.BackupPath); err != nil {
			return 0, err
		}
	}
	if err := r.Manifest.UpdateSegmentStatus(segmentID, SegmentActive, "cold segment active and hot backup cleanup complete"); err != nil {
		return 0, err
	}
	return released, nil
}

func (r MaintenanceRunner) pruneCandidateGroup(batchID string, segments []archivedSegment) (int64, error) {
	if len(segments) == 0 {
		return 0, nil
	}
	dbPath := segments[0].Candidate.DBPath
	tableCutoffs := make(map[string]string, len(segments))
	for _, segment := range segments {
		if segment.Candidate.DBPath != dbPath {
			return 0, errors.New("prune group must target one db path")
		}
		tableCutoffs[segment.Candidate.TableName] = segment.PruneCutoff
		if err := r.Manifest.UpdateSegmentStatus(segment.SegmentID, SegmentPruning, "hot pruning started"); err != nil {
			return 0, err
		}
	}
	replacement := dbPath + ".replacement." + batchID + ".tmp"
	rebuild, err := BuildReplacementHotDB(RebuildRequest{
		SourceDBPath:      dbPath,
		ReplacementDBPath: replacement,
		TableCutoffs:      tableCutoffs,
	})
	if err != nil {
		return 0, err
	}
	if rebuild.PrunedRows <= 0 {
		return 0, fmt.Errorf("replacement would prune no rows for %s tables=%s", dbPath, strings.Join(sortedTableNames(tableCutoffs), ","))
	}
	if err := verifyHotRetainedRowsForTables(dbPath, replacement, tableCutoffs, false); err != nil {
		return 0, err
	}
	replace, err := AtomicReplaceVerifiedHotDB(dbPath, replacement, batchID)
	if err != nil {
		return 0, err
	}
	if err := verifyHotRetainedRowsForTables(replace.BackupPath, dbPath, tableCutoffs, false); err != nil {
		_ = RollbackHotDBReplacement(dbPath, replace.BackupPath)
		return 0, err
	}
	for _, segment := range segments {
		if err := r.Manifest.UpdateSegmentStatus(segment.SegmentID, SegmentHotPruned, "hot db replacement verified"); err != nil {
			return 0, err
		}
	}
	var released int64
	if !r.KeepBackup {
		backupStat, statErr := os.Stat(replace.BackupPath)
		if statErr != nil {
			return 0, statErr
		}
		newStat, statErr := os.Stat(dbPath)
		if statErr != nil {
			return 0, statErr
		}
		if backupStat.Size() > newStat.Size() {
			released = backupStat.Size() - newStat.Size()
		}
		if err := os.Remove(replace.BackupPath); err != nil {
			return 0, err
		}
	}
	for _, segment := range segments {
		if err := r.Manifest.UpdateSegmentStatus(segment.SegmentID, SegmentActive, "cold segment active and hot backup cleanup complete"); err != nil {
			return 0, err
		}
	}
	return released, nil
}

func groupCandidatesByDB(candidates []LifecycleCandidate) [][]LifecycleCandidate {
	groups := make([][]LifecycleCandidate, 0)
	indexByDB := make(map[string]int)
	for _, candidate := range candidates {
		dbPath := candidate.DBPath
		if idx, ok := indexByDB[dbPath]; ok {
			groups[idx] = append(groups[idx], candidate)
			continue
		}
		indexByDB[dbPath] = len(groups)
		groups = append(groups, []LifecycleCandidate{candidate})
	}
	return groups
}

func (r MaintenanceRunner) resolveCandidates(ctx context.Context, cutoffs HotCutoffDates) ([]LifecycleCandidate, error) {
	if len(r.Candidates) > 0 {
		return assignCandidateHotCutoffs(filterSupportedCandidates(r.Candidates), cutoffs), nil
	}
	if r.MaxCandidates > 0 || r.MaxInventoryFiles > 0 {
		return DiscoverLifecycleCandidatesWithCutoffs(ctx, r.DataDir, CandidateDiscoveryOptions{
			MaxInventoryFiles: r.MaxInventoryFiles,
			MaxCandidates:     effectiveMaxCandidates(r.MaxCandidates, r.MaxCandidates),
			Sort:              r.CandidateSort,
			Domains:           r.CandidateDomains,
		}, func(row TableInventory) string {
			return cutoffs.ForTable(row.Domain, row.Table)
		})
	}
	report, err := InventoryStorageForDomains(r.DataDir, r.CandidateDomains)
	if err != nil {
		return nil, err
	}
	return filterSupportedCandidates(PlanSteadyStateRetentionWithCutoffs(report, func(row TableInventory) string {
		return cutoffs.ForTable(row.Domain, row.Table)
	}).Candidates), nil
}

func filterSupportedCandidates(candidates []LifecycleCandidate) []LifecycleCandidate {
	out := make([]LifecycleCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if !stageOneTableSupported(candidate.TableName) {
			continue
		}
		out = append(out, candidate)
	}
	return out
}

func stageOneTableSupported(table string) bool {
	switch table {
	case "TradeHistory",
		"TradeMinute1Bar",
		"TradeMinute5Bar",
		"TradeMinute15Bar",
		"TradeMinute30Bar",
		"TradeMinute60Bar",
		"TradeLive",
		"MinuteLive",
		"QuoteSnapshot",
		"OrderHistory",
		"AuctionSnapshot":
		return true
	default:
		return false
	}
}

func (r MaintenanceRunner) resolveHotCutoffs() (HotCutoffDates, error) {
	return ResolveHotCutoffDates(r.WorkdayDBPath, r.StartedAt, r.HotCutoffDate)
}

func assignCandidateHotCutoffs(candidates []LifecycleCandidate, cutoffs HotCutoffDates) []LifecycleCandidate {
	out := make([]LifecycleCandidate, len(candidates))
	for i, candidate := range candidates {
		out[i] = candidate
		if strings.TrimSpace(out[i].HotCutoffDate) == "" {
			out[i].HotCutoffDate = cutoffs.ForTable(out[i].Domain, out[i].TableName)
		}
	}
	return out
}

func candidateHotCutoff(candidate LifecycleCandidate, fallback string) string {
	if cutoff := strings.TrimSpace(candidate.HotCutoffDate); cutoff != "" {
		return cutoff
	}
	return strings.TrimSpace(fallback)
}

func (r MaintenanceRunner) dataset() string {
	if strings.TrimSpace(r.Dataset) != "" {
		return strings.TrimSpace(r.Dataset)
	}
	return defaultLifecycleDataset
}

func (r MaintenanceRunner) minVerifiedSegments() int {
	if r.MinVerifiedSegments > 0 {
		return r.MinVerifiedSegments
	}
	return 100
}

func (r MaintenanceRunner) processWorkers() int {
	if r.ProcessWorkers > 0 {
		return r.ProcessWorkers
	}
	return 1
}

func (r MaintenanceRunner) restorePath(segmentID, table string) string {
	root := r.RestoreDir
	if root == "" {
		root = filepath.Join(r.DataDir, "cold_restore")
	}
	return filepath.Join(root, segmentID+"-"+table+".db")
}

func lifecycleSegmentID(batchID string, candidate LifecycleCandidate, startDate, endDate string) string {
	return strings.Join([]string{
		batchID,
		sanitizeSegmentPart(candidate.Domain),
		sanitizeSegmentPart(candidate.TableName),
		sanitizeSegmentPart(candidate.Instrument),
		startDate,
		endDate,
	}, "-")
}

func coldURIForCandidate(dataset string, candidate LifecycleCandidate, startDate, endDate, batchID string) string {
	instrument := candidate.Instrument
	if instrument == "" {
		instrument = "shared"
	}
	year := startDate
	if len(year) >= 4 {
		year = year[:4]
	}
	return MustFormatColdURI(ColdURI{
		Dataset: dataset,
		Path: strings.Join([]string{
			"cold",
			"domain=" + sanitizeSegmentPart(candidate.Domain),
			"table=" + sanitizeSegmentPart(candidate.TableName),
			"instrument=" + sanitizeSegmentPart(instrument),
			"year=" + sanitizeSegmentPart(year),
			fmt.Sprintf("part-%s-%s-%s.parquet", sanitizeSegmentPart(batchID), startDate, endDate),
		}, "/"),
	})
}

func coldPartitionKeyForCandidate(candidate LifecycleCandidate, startDate string) string {
	instrument := candidate.Instrument
	if instrument == "" {
		instrument = "shared"
	}
	year := startDate
	if len(year) >= 4 {
		year = year[:4]
	}
	return strings.Join([]string{
		"domain=" + sanitizeSegmentPart(candidate.Domain),
		"table=" + sanitizeSegmentPart(candidate.TableName),
		"instrument=" + sanitizeSegmentPart(instrument),
		"year=" + sanitizeSegmentPart(year),
	}, "/")
}

func sourceSelectionHash(candidate LifecycleCandidate, startDate, endDate string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		candidate.DBPath,
		candidate.Domain,
		candidate.TableName,
		candidate.Instrument,
		startDate,
		endDate,
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

func sanitizeSegmentPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	return replacer.Replace(value)
}

func previousCalendarDate(cutoff string) string {
	loc, err := shanghaiLocation()
	if err != nil {
		return cutoff
	}
	day, err := time.ParseInLocation("20060102", cutoff, loc)
	if err != nil {
		return cutoff
	}
	return day.AddDate(0, 0, -1).Format("20060102")
}

func nextCalendarDate(date string) string {
	loc, err := shanghaiLocation()
	if err != nil {
		return date
	}
	day, err := time.ParseInLocation("20060102", date, loc)
	if err != nil {
		return date
	}
	return day.AddDate(0, 0, 1).Format("20060102")
}

func (r MaintenanceRunner) capArchiveEndDate(startDate, endDate string) string {
	if r.MaxArchiveDays <= 0 || startDate == "" || endDate == "" {
		return endDate
	}
	loc, err := shanghaiLocation()
	if err != nil {
		return endDate
	}
	start, err := time.ParseInLocation("20060102", startDate, loc)
	if err != nil {
		return endDate
	}
	capped := start.AddDate(0, 0, r.MaxArchiveDays-1).Format("20060102")
	if capped < endDate {
		return capped
	}
	return endDate
}
