package billboard

import "time"

const (
	DomainMarketBillboard = "market_billboard"

	SeatTypeBrokerage   = "brokerage"
	SeatTypeInstitution = "institution"
	SeatTypeNorthbound  = "northbound"
	SeatTypeUnknown     = "unknown"

	SyncStatusPassed       = "passed"
	SyncStatusEmptySuccess = "empty_success"
	SyncStatusFailed       = "failed"

	CoverageComplete     = "complete"
	CoveragePartial      = "partial"
	CoverageMissing      = "missing"
	CoverageFailed       = "failed"
	CoverageEmptySuccess = "empty_success"
)

type RawRowRecord struct {
	ID                int64     `xorm:"pk autoincr" json:"id"`
	ReportName        string    `xorm:"varchar(96) index notnull" json:"report_name"`
	QueryKey          string    `xorm:"varchar(255) index" json:"query_key,omitempty"`
	RowKey            string    `xorm:"varchar(255) unique notnull" json:"row_key"`
	TradeDate         string    `xorm:"varchar(16) index" json:"trade_date,omitempty"`
	FullCode          string    `xorm:"varchar(16) index" json:"full_code,omitempty"`
	PayloadJSON       string    `xorm:"text notnull" json:"payload_json"`
	PayloadHash       string    `xorm:"varchar(64) index notnull" json:"payload_hash"`
	FetchedAt         time.Time `xorm:"index notnull" json:"fetched_at"`
	SourceReportName  string    `xorm:"varchar(96) index" json:"source_report_name,omitempty"`
	SourceRowID       string    `xorm:"varchar(255) index" json:"source_row_id,omitempty"`
	SourcePayloadHash string    `xorm:"varchar(64) index" json:"source_payload_hash,omitempty"`
	CreatedAt         time.Time `xorm:"created" json:"created_at"`
	UpdatedAt         time.Time `xorm:"updated" json:"updated_at"`
}

func (*RawRowRecord) TableName() string { return "eastmoney_rpt_raw_row" }

type EntryRecord struct {
	ID                       int64     `xorm:"pk autoincr" json:"id"`
	StableKey                string    `xorm:"varchar(255) unique notnull" json:"stable_key"`
	TradeDate                string    `xorm:"varchar(16) index notnull" json:"trade_date"`
	FullCode                 string    `xorm:"varchar(16) index notnull" json:"full_code"`
	Code                     string    `xorm:"varchar(16) index notnull" json:"code"`
	Exchange                 string    `xorm:"varchar(8) index notnull" json:"exchange"`
	Name                     string    `xorm:"varchar(64)" json:"name"`
	AssetType                string    `xorm:"varchar(32) index notnull" json:"asset_type"`
	ClosePriceMilli          int64     `json:"close_price_milli"`
	ChangeRatePct            float64   `json:"change_rate_pct"`
	BillboardNetAmountMilli  int64     `json:"billboard_net_amount_milli"`
	BillboardBuyAmountMilli  int64     `json:"billboard_buy_amount_milli"`
	BillboardSellAmountMilli int64     `json:"billboard_sell_amount_milli"`
	BillboardDealAmountMilli int64     `json:"billboard_deal_amount_milli"`
	AccumAmountMilli         int64     `json:"accum_amount_milli"`
	DealNetRatioPct          float64   `json:"deal_net_ratio_pct"`
	DealAmountRatioPct       float64   `json:"deal_amount_ratio_pct"`
	TurnoverRatePct          float64   `json:"turnover_rate_pct"`
	FreeMarketCapMilli       int64     `json:"free_market_cap_milli"`
	ProviderD1CloseAdjPct    float64   `json:"provider_d1_close_adj_pct"`
	ProviderD2CloseAdjPct    float64   `json:"provider_d2_close_adj_pct"`
	ProviderD3CloseAdjPct    float64   `json:"provider_d3_close_adj_pct"`
	ProviderD5CloseAdjPct    float64   `json:"provider_d5_close_adj_pct"`
	ProviderD10CloseAdjPct   float64   `json:"provider_d10_close_adj_pct"`
	SourceReportName         string    `xorm:"varchar(96) index" json:"source_report_name,omitempty"`
	SourceTradeID            string    `xorm:"varchar(64) index" json:"source_trade_id,omitempty"`
	SourceChangeType         string    `xorm:"varchar(64) index" json:"source_change_type,omitempty"`
	SourceSecurityCode       string    `xorm:"varchar(16)" json:"source_security_code,omitempty"`
	SourceSecuCode           string    `xorm:"varchar(32)" json:"source_secucode,omitempty"`
	SourceRowID              string    `xorm:"varchar(255) index" json:"source_row_id,omitempty"`
	SourcePayloadHash        string    `xorm:"varchar(64) index" json:"source_payload_hash,omitempty"`
	FetchedAt                time.Time `xorm:"index" json:"fetched_at,omitempty"`
	CreatedAt                time.Time `xorm:"created" json:"created_at"`
	UpdatedAt                time.Time `xorm:"updated" json:"updated_at"`
}

func (*EntryRecord) TableName() string { return "market_billboard_entry" }

type ReasonRecord struct {
	ID                int64     `xorm:"pk autoincr" json:"id"`
	ReasonText        string    `xorm:"text notnull" json:"reason_text"`
	ReasonHash        string    `xorm:"varchar(64) unique notnull" json:"reason_hash"`
	SourceChangeType  string    `xorm:"varchar(64) index" json:"source_change_type,omitempty"`
	SourceExplanation string    `xorm:"text" json:"source_explanation,omitempty"`
	CreatedAt         time.Time `xorm:"created" json:"created_at"`
	UpdatedAt         time.Time `xorm:"updated" json:"updated_at"`
}

func (*ReasonRecord) TableName() string { return "market_billboard_reason" }

type EntryReasonRecord struct {
	ID        int64     `xorm:"pk autoincr" json:"id"`
	EntryID   int64     `xorm:"index notnull" json:"entry_id"`
	ReasonID  int64     `xorm:"index notnull" json:"reason_id"`
	LinkKey   string    `xorm:"varchar(128) unique notnull" json:"link_key"`
	CreatedAt time.Time `xorm:"created" json:"created_at"`
}

func (*EntryReasonRecord) TableName() string { return "market_billboard_entry_reason" }

type SeatRecord struct {
	ID                int64     `xorm:"pk autoincr" json:"id"`
	SeatKey           string    `xorm:"varchar(255) unique notnull" json:"seat_key"`
	SeatName          string    `xorm:"varchar(255) index notnull" json:"seat_name"`
	SeatType          string    `xorm:"varchar(32) index notnull" json:"seat_type"`
	SourceSeatCode    string    `xorm:"varchar(64) index" json:"source_seat_code,omitempty"`
	SourceSeatName    string    `xorm:"varchar(255)" json:"source_seat_name,omitempty"`
	SourceSeatCodeOld string    `xorm:"varchar(64)" json:"source_seat_code_old,omitempty"`
	CreatedAt         time.Time `xorm:"created" json:"created_at"`
	UpdatedAt         time.Time `xorm:"updated" json:"updated_at"`
}

func (*SeatRecord) TableName() string { return "market_billboard_seat" }

type SeatTradeRecord struct {
	ID                int64     `xorm:"pk autoincr" json:"id"`
	StableKey         string    `xorm:"varchar(255) unique notnull" json:"stable_key"`
	EntryID           int64     `xorm:"index notnull" json:"entry_id"`
	SeatID            int64     `xorm:"index notnull" json:"seat_id"`
	TradeDate         string    `xorm:"varchar(16) index notnull" json:"trade_date"`
	FullCode          string    `xorm:"varchar(16) index notnull" json:"full_code"`
	Side              string    `xorm:"varchar(8) index notnull" json:"side"`
	Rank              int       `xorm:"index notnull" json:"rank"`
	BuyAmountMilli    int64     `json:"buy_amount_milli"`
	SellAmountMilli   int64     `json:"sell_amount_milli"`
	NetAmountMilli    int64     `json:"net_amount_milli"`
	AccumAmountMilli  int64     `json:"accum_amount_milli"`
	AccumVolumeShare  int64     `json:"accum_volume_share"`
	ChangeRatePct     float64   `json:"change_rate_pct"`
	ClosePriceMilli   int64     `json:"close_price_milli"`
	BuyRatio          float64   `json:"buy_ratio"`
	SellRatio         float64   `json:"sell_ratio"`
	SourceReportName  string    `xorm:"varchar(96) index" json:"source_report_name,omitempty"`
	SourceRowID       string    `xorm:"varchar(255) index" json:"source_row_id,omitempty"`
	SourcePayloadHash string    `xorm:"varchar(64) index" json:"source_payload_hash,omitempty"`
	FetchedAt         time.Time `xorm:"index" json:"fetched_at,omitempty"`
	CreatedAt         time.Time `xorm:"created" json:"created_at"`
	UpdatedAt         time.Time `xorm:"updated" json:"updated_at"`
}

func (*SeatTradeRecord) TableName() string { return "market_billboard_seat_trade" }

type InstitutionTradeRecord struct {
	ID                     int64     `xorm:"pk autoincr" json:"id"`
	StableKey              string    `xorm:"varchar(255) unique notnull" json:"stable_key"`
	TradeDate              string    `xorm:"varchar(16) index notnull" json:"trade_date"`
	FullCode               string    `xorm:"varchar(16) index notnull" json:"full_code"`
	Code                   string    `xorm:"varchar(16) index notnull" json:"code"`
	Exchange               string    `xorm:"varchar(8) index notnull" json:"exchange"`
	Name                   string    `xorm:"varchar(64)" json:"name"`
	BuyTimes               int       `json:"buy_times"`
	SellTimes              int       `json:"sell_times"`
	BuyCount               int       `json:"buy_count"`
	SellCount              int       `json:"sell_count"`
	BuyAmountMilli         int64     `json:"buy_amount_milli"`
	SellAmountMilli        int64     `json:"sell_amount_milli"`
	NetBuyAmountMilli      int64     `json:"net_buy_amount_milli"`
	AccumAmountMilli       int64     `json:"accum_amount_milli"`
	RatioPct               float64   `json:"ratio_pct"`
	TurnoverRatePct        float64   `json:"turnover_rate_pct"`
	FreeMarketCapMilli     int64     `json:"free_market_cap_milli"`
	ProviderD1CloseAdjPct  float64   `json:"provider_d1_close_adj_pct"`
	ProviderD2CloseAdjPct  float64   `json:"provider_d2_close_adj_pct"`
	ProviderD3CloseAdjPct  float64   `json:"provider_d3_close_adj_pct"`
	ProviderD5CloseAdjPct  float64   `json:"provider_d5_close_adj_pct"`
	ProviderD10CloseAdjPct float64   `json:"provider_d10_close_adj_pct"`
	SourceReportName       string    `xorm:"varchar(96) index" json:"source_report_name,omitempty"`
	SourceRowID            string    `xorm:"varchar(255) index" json:"source_row_id,omitempty"`
	SourcePayloadHash      string    `xorm:"varchar(64) index" json:"source_payload_hash,omitempty"`
	FetchedAt              time.Time `xorm:"index" json:"fetched_at,omitempty"`
	CreatedAt              time.Time `xorm:"created" json:"created_at"`
	UpdatedAt              time.Time `xorm:"updated" json:"updated_at"`
}

func (*InstitutionTradeRecord) TableName() string { return "market_billboard_institution_trade" }

type InstrumentStatRecord struct {
	ID                         int64     `xorm:"pk autoincr" json:"id"`
	StableKey                  string    `xorm:"varchar(255) unique notnull" json:"stable_key"`
	FullCode                   string    `xorm:"varchar(16) index notnull" json:"full_code"`
	Code                       string    `xorm:"varchar(16) index notnull" json:"code"`
	Exchange                   string    `xorm:"varchar(8) index notnull" json:"exchange"`
	Name                       string    `xorm:"varchar(64)" json:"name"`
	StatisticsCycle            string    `xorm:"varchar(16) index" json:"statistics_cycle"`
	PeriodLabel                string    `xorm:"varchar(64)" json:"period_label"`
	LatestTradeDate            string    `xorm:"varchar(16) index" json:"latest_trade_date"`
	BillboardTimes             int       `json:"billboard_times"`
	BillboardDealAmountMilli   int64     `json:"billboard_deal_amount_milli"`
	BillboardBuyAmountMilli    int64     `json:"billboard_buy_amount_milli"`
	BillboardSellAmountMilli   int64     `json:"billboard_sell_amount_milli"`
	BillboardNetBuyAmountMilli int64     `json:"billboard_net_buy_amount_milli"`
	OrgTimes                   int       `json:"org_times"`
	OrgDealAmountMilli         int64     `json:"org_deal_amount_milli"`
	OrgBuyAmountMilli          int64     `json:"org_buy_amount_milli"`
	OrgSellAmountMilli         int64     `json:"org_sell_amount_milli"`
	OrgNetBuyAmountMilli       int64     `json:"org_net_buy_amount_milli"`
	OrgBuyTimes                int       `json:"org_buy_times"`
	OrgSellTimes               int       `json:"org_sell_times"`
	InstrumentPct1M            float64   `json:"instrument_pct_1m"`
	InstrumentPct3M            float64   `json:"instrument_pct_3m"`
	InstrumentPct6M            float64   `json:"instrument_pct_6m"`
	InstrumentPct1Y            float64   `json:"instrument_pct_1y"`
	SourceReportName           string    `xorm:"varchar(96) index" json:"source_report_name,omitempty"`
	SourceRowID                string    `xorm:"varchar(255) index" json:"source_row_id,omitempty"`
	SourcePayloadHash          string    `xorm:"varchar(64) index" json:"source_payload_hash,omitempty"`
	FetchedAt                  time.Time `xorm:"index" json:"fetched_at,omitempty"`
	CreatedAt                  time.Time `xorm:"created" json:"created_at"`
	UpdatedAt                  time.Time `xorm:"updated" json:"updated_at"`
}

func (*InstrumentStatRecord) TableName() string { return "market_billboard_instrument_stat" }

type SyncStatusRecord struct {
	ID            int64     `xorm:"pk autoincr" json:"id"`
	StatusKey     string    `xorm:"varchar(160) unique notnull" json:"status_key"`
	TradeDate     string    `xorm:"varchar(16) index notnull" json:"trade_date"`
	ReportName    string    `xorm:"varchar(96) index notnull" json:"report_name"`
	Status        string    `xorm:"varchar(32) index notnull" json:"status"`
	RowCount      int       `json:"row_count"`
	FilteredCount int       `json:"filtered_count"`
	LastError     string    `xorm:"text" json:"last_error,omitempty"`
	StartedAt     time.Time `json:"started_at,omitempty"`
	CompletedAt   time.Time `json:"completed_at,omitempty"`
	UpdatedAt     time.Time `xorm:"updated" json:"updated_at"`
}

func (*SyncStatusRecord) TableName() string { return "market_billboard_sync_status" }

type EntryQuery struct {
	StartDate string
	EndDate   string
	FullCode  string
	Limit     int
	Cursor    string
}

type SeatTradeQuery struct {
	TradeDate string
	FullCode  string
	Keyword   string
	Side      string
	Limit     int
	Cursor    string
}

type InstitutionQuery struct {
	StartDate string
	EndDate   string
	FullCode  string
	Limit     int
	Cursor    string
}

type InstrumentStatQuery struct {
	FullCode string
	Cycle    string
	Limit    int
	Cursor   string
}

type EntryList struct {
	Items      []EntryRecord
	NextCursor string
}

type SeatTradeList struct {
	Items      []SeatTradeView
	NextCursor string
}

type SeatTradeView struct {
	SeatTradeRecord `xorm:"extends"`
	SeatName        string `json:"seat_name"`
	SeatType        string `json:"seat_type"`
	SourceSeatCode  string `json:"source_seat_code,omitempty"`
}

type InstitutionList struct {
	Items      []InstitutionTradeRecord
	NextCursor string
}

type InstrumentStatList struct {
	Items      []InstrumentStatRecord
	NextCursor string
}

type Freshness struct {
	Domain         string   `json:"domain"`
	Watermark      string   `json:"watermark,omitempty"`
	QueryStartDate string   `json:"query_start_date,omitempty"`
	QueryEndDate   string   `json:"query_end_date,omitempty"`
	Coverage       string   `json:"coverage"`
	Status         string   `json:"status"`
	ErrorSummary   string   `json:"error_summary,omitempty"`
	Reports        []string `json:"reports,omitempty"`
}
