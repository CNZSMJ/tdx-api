package lifecycle

type ProfessionalFinanceSlimmingPlan struct {
	HotServingTables           []string
	RawSourceArchiveCandidates []TableInventory
	EstimatedReclaimBytes      int64
	EndpointDependencies       map[string][]string
}

type ProfessionalFinanceArchiveChunk struct {
	Table      string
	ChunkIndex int
	MaxRows    int64
}

func PlanProfessionalFinanceSlimming(inventory StorageInventoryReport) ProfessionalFinanceSlimmingPlan {
	deps := ProfessionalFinanceEndpointDependencies()
	servingSet := map[string]bool{}
	for endpoint, tables := range deps {
		if endpoint == "raw/source candidates" {
			continue
		}
		for _, table := range tables {
			servingSet[table] = true
		}
	}
	rawSet := map[string]bool{}
	for _, table := range deps["raw/source candidates"] {
		rawSet[table] = true
	}
	plan := ProfessionalFinanceSlimmingPlan{EndpointDependencies: deps}
	for table := range servingSet {
		plan.HotServingTables = append(plan.HotServingTables, table)
	}
	reclaimDBs := make(map[string]bool)
	for _, row := range inventory.Tables {
		if row.Domain != "professional_finance" || !rawSet[row.Table] {
			continue
		}
		plan.RawSourceArchiveCandidates = append(plan.RawSourceArchiveCandidates, row)
		if row.DBPath == "" {
			plan.EstimatedReclaimBytes += row.FileBytes
			continue
		}
		if reclaimDBs[row.DBPath] {
			continue
		}
		reclaimDBs[row.DBPath] = true
		plan.EstimatedReclaimBytes += row.FileBytes
	}
	return plan
}

func PlanProfessionalFinanceArchiveChunks(row TableInventory, maxRows int64) []ProfessionalFinanceArchiveChunk {
	if maxRows <= 0 {
		maxRows = 500_000
	}
	chunks := make([]ProfessionalFinanceArchiveChunk, 0)
	remaining := row.RowCount
	index := 0
	for remaining > 0 {
		chunkRows := maxRows
		if remaining < maxRows {
			chunkRows = remaining
		}
		chunks = append(chunks, ProfessionalFinanceArchiveChunk{Table: row.Table, ChunkIndex: index, MaxRows: chunkRows})
		remaining -= chunkRows
		index++
	}
	return chunks
}
