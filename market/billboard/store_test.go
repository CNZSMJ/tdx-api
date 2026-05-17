package billboard

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreUpsertsEntryReasonAndSeatTradeIdempotently(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "market_billboard.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.Local)
	entry := EntryRecord{
		TradeDate:                "20240515",
		FullCode:                 "sz000504",
		Code:                     "000504",
		Exchange:                 "sz",
		Name:                     "*ST生物",
		AssetType:                "stock",
		BillboardBuyAmountMilli:  50185957000,
		BillboardSellAmountMilli: 69639811660,
		BillboardNetAmountMilli:  -19453854660,
		SourceReportName:         ReportDailyDetails,
		SourceTradeID:            "4967145",
		SourceChangeType:         "137001002002002",
		SourceRowID:              "4967145|sz000504|20240515|137001002002002",
		SourcePayloadHash:        "hash-entry",
		FetchedAt:                now,
	}
	reason := ReasonRecord{
		ReasonText:        "连续三个交易日内，跌幅偏离值累计达到20%的证券",
		ReasonHash:        HashText("连续三个交易日内，跌幅偏离值累计达到20%的证券"),
		SourceChangeType:  "137001002002002",
		SourceExplanation: "连续三个交易日内，跌幅偏离值累计达到20%的证券",
	}

	id1, err := store.UpsertEntry(entry, []ReasonRecord{reason})
	if err != nil {
		t.Fatalf("upsert entry first: %v", err)
	}
	id2, err := store.UpsertEntry(entry, []ReasonRecord{reason})
	if err != nil {
		t.Fatalf("upsert entry second: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("entry id changed across idempotent upsert: %d vs %d", id1, id2)
	}

	seatID, err := store.UpsertSeat(SeatRecord{
		SeatName:       "中国银河证券股份有限公司北京中关村大街证券营业部",
		SeatType:       SeatTypeBrokerage,
		SourceSeatCode: "10115140",
		SourceSeatName: "中国银河证券股份有限公司北京中关村大街证券营业部",
	})
	if err != nil {
		t.Fatalf("upsert seat: %v", err)
	}
	trade := SeatTradeRecord{
		EntryID:           id1,
		SeatID:            seatID,
		TradeDate:         "20240515",
		FullCode:          "sz000504",
		Side:              "buy",
		Rank:              1,
		BuyAmountMilli:    20443206000,
		SellAmountMilli:   311441000,
		NetAmountMilli:    20131765000,
		SourceReportName:  ReportBuyDetails,
		SourceRowID:       "4967145|sz000504|20240515|10115140|buy",
		SourcePayloadHash: "hash-seat",
		FetchedAt:         now,
	}
	if err := store.UpsertSeatTrade(trade); err != nil {
		t.Fatalf("upsert seat trade first: %v", err)
	}
	if err := store.UpsertSeatTrade(trade); err != nil {
		t.Fatalf("upsert seat trade second: %v", err)
	}

	list, err := store.ListEntries(EntryQuery{StartDate: "20240515", EndDate: "20240515", Limit: 10})
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("entry count = %d, want 1", len(list.Items))
	}
	seats, err := store.ListSeatTrades(SeatTradeQuery{FullCode: "sz000504", TradeDate: "20240515", Limit: 10})
	if err != nil {
		t.Fatalf("list seat trades: %v", err)
	}
	if len(seats.Items) != 1 || seats.Items[0].Rank != 1 || seats.Items[0].Side != "buy" {
		t.Fatalf("seat trades = %#v, want single buy rank 1", seats.Items)
	}
}

func TestStoreCoverageDistinguishesMissingFailedAndEmptySuccess(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "market_billboard.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if got, err := store.Coverage("20240515", "20240515", []string{ReportDailyDetails}); err != nil {
		t.Fatalf("coverage missing: %v", err)
	} else if got.Coverage != CoverageMissing {
		t.Fatalf("coverage = %s, want missing", got.Coverage)
	}

	if err := store.UpsertSyncStatus(SyncStatusRecord{
		TradeDate:   "20240515",
		ReportName:  ReportDailyDetails,
		Status:      SyncStatusEmptySuccess,
		StartedAt:   time.Now(),
		CompletedAt: time.Now(),
	}); err != nil {
		t.Fatalf("upsert sync status: %v", err)
	}
	if got, err := store.Coverage("20240515", "20240515", []string{ReportDailyDetails}); err != nil {
		t.Fatalf("coverage empty_success: %v", err)
	} else if got.Coverage != CoverageEmptySuccess || got.Status != "fresh" {
		t.Fatalf("coverage = %#v, want empty_success fresh", got)
	}

	if err := store.UpsertSyncStatus(SyncStatusRecord{
		TradeDate:   "20240516",
		ReportName:  ReportDailyDetails,
		Status:      SyncStatusFailed,
		LastError:   "upstream unavailable",
		StartedAt:   time.Now(),
		CompletedAt: time.Now(),
	}); err != nil {
		t.Fatalf("upsert failed status: %v", err)
	}
	if got, err := store.Coverage("20240516", "20240516", []string{ReportDailyDetails}); err != nil {
		t.Fatalf("coverage failed: %v", err)
	} else if got.Coverage != CoverageFailed || got.ErrorSummary == "" {
		t.Fatalf("coverage = %#v, want failed with error summary", got)
	}
}

func TestStoreSeatTradeCursorDoesNotRepeatRows(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "market_billboard.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	seatID, err := store.UpsertSeat(SeatRecord{
		SeatName: "机构专用",
		SeatType: SeatTypeInstitution,
	})
	if err != nil {
		t.Fatalf("upsert seat: %v", err)
	}
	for _, sourceRowID := range []string{"row-1", "row-2"} {
		if err := store.UpsertSeatTrade(SeatTradeRecord{
			SeatID:            seatID,
			TradeDate:         "20240515",
			FullCode:          "sz000001",
			Side:              "buy",
			Rank:              1,
			SourceReportName:  ReportBuyDetails,
			SourceRowID:       sourceRowID,
			SourcePayloadHash: HashText(sourceRowID),
		}); err != nil {
			t.Fatalf("upsert seat trade %s: %v", sourceRowID, err)
		}
	}

	first, err := store.ListSeatTrades(SeatTradeQuery{TradeDate: "20240515", Limit: 1})
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	if len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("first page = %#v, want one row and next cursor", first)
	}
	second, err := store.ListSeatTrades(SeatTradeQuery{TradeDate: "20240515", Limit: 1, Cursor: first.NextCursor})
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	if len(second.Items) != 1 {
		t.Fatalf("second page count = %d, want 1", len(second.Items))
	}
	if first.Items[0].ID == second.Items[0].ID {
		t.Fatalf("cursor repeated row id %d", first.Items[0].ID)
	}
}

func TestStoreSeatTradeKeywordFiltersBeforePagination(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "market_billboard.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	matchingSeatID, err := store.UpsertSeat(SeatRecord{
		SeatName: "机构专用",
		SeatType: SeatTypeInstitution,
	})
	if err != nil {
		t.Fatalf("upsert matching seat: %v", err)
	}
	otherSeatID, err := store.UpsertSeat(SeatRecord{
		SeatName: "申万宏源证券有限公司上海天钥桥路营业部",
		SeatType: SeatTypeBrokerage,
	})
	if err != nil {
		t.Fatalf("upsert other seat: %v", err)
	}
	if err := store.UpsertSeatTrade(SeatTradeRecord{
		SeatID:            matchingSeatID,
		TradeDate:         "20260515",
		FullCode:          "bj920580",
		Side:              "buy",
		Rank:              1,
		SourceReportName:  ReportBuyDetails,
		SourceRowID:       "matching-row",
		SourcePayloadHash: "matching-hash",
	}); err != nil {
		t.Fatalf("upsert matching trade: %v", err)
	}
	if err := store.UpsertSeatTrade(SeatTradeRecord{
		SeatID:            otherSeatID,
		TradeDate:         "20260515",
		FullCode:          "bj920580",
		Side:              "buy",
		Rank:              2,
		SourceReportName:  ReportBuyDetails,
		SourceRowID:       "other-row",
		SourcePayloadHash: "other-hash",
	}); err != nil {
		t.Fatalf("upsert other trade: %v", err)
	}

	list, err := store.ListSeatTrades(SeatTradeQuery{Keyword: "机构专用", Limit: 1})
	if err != nil {
		t.Fatalf("list seat trades: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].SeatName != "机构专用" {
		t.Fatalf("keyword list = %#v, want matching seat before pagination", list.Items)
	}
}
