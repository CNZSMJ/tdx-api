package profinance

type QueryError struct {
	ErrorCode  string
	HTTPStatus int
	Retryable  bool
	Message    string
	Details    map[string]any
}

func (e *QueryError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

type CoverageInfo struct {
	Available             bool   `json:"available"`
	AnnounceDateSource    string `json:"announce_date_source,omitempty"`
	EffectiveAnnounceDate string `json:"effective_announce_date,omitempty"`
	SourceReportFile      string `json:"source_report_file,omitempty"`
}

type SnapshotQuery struct {
	FullCode   string
	ReportDate string
	AsOfDate   string
	FieldCodes []string
	PeriodMode string
}

type SnapshotResult struct {
	FullCode        string                 `json:"full_code"`
	Name            string                 `json:"name,omitempty"`
	ReportDate      string                 `json:"report_date"`
	AnnounceDate    string                 `json:"announce_date"`
	AsOfDate        string                 `json:"as_of_date,omitempty"`
	KnowledgeCutoff string                 `json:"knowledge_cutoff"`
	Source          string                 `json:"source"`
	FieldValues     map[string]interface{} `json:"field_values"`
	MissingFields   []string               `json:"missing_fields"`
	Coverage        CoverageInfo           `json:"coverage"`
}

type HistoryQuery struct {
	FullCode        string
	FieldCodes      []string
	AsOfDate        string
	StartReportDate string
	EndReportDate   string
	Limit           int
	Period          string
}

type HistoryItem struct {
	ReportDate       string                 `json:"report_date"`
	AnnounceDate     string                 `json:"announce_date"`
	FieldValues      map[string]interface{} `json:"field_values"`
	MissingFields    []string               `json:"missing_fields"`
	SourceReportFile string                 `json:"source_report_file"`
}

type HistoryResult struct {
	FullCode        string        `json:"full_code"`
	Name            string        `json:"name,omitempty"`
	AsOfDate        string        `json:"as_of_date,omitempty"`
	KnowledgeCutoff string        `json:"knowledge_cutoff"`
	FieldCodes      []string      `json:"field_codes"`
	List            []HistoryItem `json:"list"`
}

type CoverageQuery struct {
	FullCode   string
	ReportDate string
	AsOfDate   string
	FieldCodes []string
}

type CoverageResult struct {
	FullCode            string   `json:"full_code"`
	Name                string   `json:"name,omitempty"`
	LatestReportDate    string   `json:"latest_report_date,omitempty"`
	RequestedFieldCodes []string `json:"requested_field_codes"`
	EvaluatedFieldCodes []string `json:"evaluated_field_codes"`
	KnowledgeCutoff     string   `json:"knowledge_cutoff"`
	AvailableReports    []string `json:"available_reports"`
	AvailableFieldCodes []string `json:"available_field_codes"`
	MissingFields       []string `json:"missing_fields"`
	Status              string   `json:"status"`
	StatusReason        string   `json:"status_reason"`
}

type CrossSectionQuery struct {
	FullCodes  []string
	ReportDate string
	AsOfDate   string
	FieldCodes []string
	PeriodMode string
	Limit      int
	Cursor     string
}

type CrossSectionItem struct {
	FullCode      string                 `json:"full_code"`
	Name          string                 `json:"name,omitempty"`
	ReportDate    string                 `json:"report_date"`
	AnnounceDate  string                 `json:"announce_date"`
	FieldValues   map[string]interface{} `json:"field_values"`
	MissingFields []string               `json:"missing_fields"`
	Coverage      CoverageInfo           `json:"coverage"`
}

type CrossSectionResult struct {
	ReportDate      string             `json:"report_date,omitempty"`
	AsOfDate        string             `json:"as_of_date,omitempty"`
	KnowledgeCutoff string             `json:"knowledge_cutoff"`
	FieldCodes      []string           `json:"field_codes"`
	NextCursor      *string            `json:"next_cursor"`
	Items           []CrossSectionItem `json:"items"`
}
