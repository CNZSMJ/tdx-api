package lifecycle

type SteadyStatePlan struct {
	Candidates []LifecycleCandidate
}

func PlanSteadyStateRetention(report StorageInventoryReport, hotCutoffDate string) SteadyStatePlan {
	plan := SteadyStatePlan{}
	for _, row := range report.Tables {
		if row.RowCount == 0 || row.MinDate == "" || row.MinDate >= hotCutoffDate {
			continue
		}
		plan.Candidates = append(plan.Candidates, LifecycleCandidate{
			DBPath:                    row.DBPath,
			Domain:                    row.Domain,
			TableName:                 row.Table,
			Instrument:                row.Instrument,
			MinDate:                   row.MinDate,
			MaxDate:                   row.MaxDate,
			SourceDBBytes:             row.FileBytes,
			ColdRowShare:              estimateColdShare(row.MinDate, row.MaxDate, hotCutoffDate),
			EstimatedParquetRatio:     0.4,
			EstimatedReplacementRatio: 0.1,
		})
	}
	return plan
}

func estimateColdShare(minDate, maxDate, cutoff string) float64 {
	if maxDate != "" && maxDate < cutoff {
		return 1.0
	}
	if minDate < cutoff {
		return 0.5
	}
	return 0
}

type DebtDay struct {
	Date string
	Debt int
}

type LifecycleDebtTracker struct {
	Days []DebtDay
}

func (t *LifecycleDebtTracker) RecordDay(date string, debt int) {
	t.Days = append(t.Days, DebtDay{Date: date, Debt: debt})
}

func (t LifecycleDebtTracker) ConsecutiveDebtDays() int {
	count := 0
	for i := len(t.Days) - 1; i >= 0; i-- {
		if t.Days[i].Debt <= 0 {
			break
		}
		count++
	}
	return count
}

type SteadyStateGate struct {
	FreeBytes                      int64
	WriteWatermarkBytes            int64
	HigherPriorityGovernanceActive bool
}

type GateDecision struct {
	Allowed bool
	Reason  string
}

func (g SteadyStateGate) Decide() GateDecision {
	if g.WriteWatermarkBytes > 0 && g.FreeBytes < g.WriteWatermarkBytes {
		return GateDecision{Reason: "free disk below lifecycle write watermark"}
	}
	if g.HigherPriorityGovernanceActive {
		return GateDecision{Reason: "higher-priority governance job active"}
	}
	return GateDecision{Allowed: true}
}
