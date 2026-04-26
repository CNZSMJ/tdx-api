package lifecycle

const (
	SourceProvider          = "provider"
	SourceProfessionalDB    = "professional_finance_service"
	LegacyResponseEnvelope  = "Response{code,message,data}"
	ProfFinanceEnvelope     = "profFinanceEnvelope{code,request_id,data,error}"
	BehaviorProviderCurrent = "preserve current provider/client behavior; no cold fallback"
)

type APIParameter struct {
	Name     string
	Required bool
	Notes    string
}

type APIContract struct {
	Method                string
	Path                  string
	Parameters            []APIParameter
	ResponseEnvelope      string
	ErrorEnvelope         string
	SourceClass           string
	OutOfHotRangeBehavior string
	ColdReadAllowed       bool
}

func ExistingAPIContracts() []APIContract {
	legacy := func(path string, params ...APIParameter) APIContract {
		return APIContract{
			Method:                "GET",
			Path:                  path,
			Parameters:            params,
			ResponseEnvelope:      LegacyResponseEnvelope,
			ErrorEnvelope:         LegacyResponseEnvelope,
			SourceClass:           SourceProvider,
			OutOfHotRangeBehavior: BehaviorProviderCurrent,
			ColdReadAllowed:       false,
		}
	}
	prof := func(path string, params ...APIParameter) APIContract {
		return APIContract{
			Method:                "GET",
			Path:                  path,
			Parameters:            params,
			ResponseEnvelope:      ProfFinanceEnvelope,
			ErrorEnvelope:         ProfFinanceEnvelope,
			SourceClass:           SourceProfessionalDB,
			OutOfHotRangeBehavior: "preserve current professional-finance service query behavior; no cold fallback",
			ColdReadAllowed:       false,
		}
	}

	return []APIContract{
		legacy("/api/trade-history",
			APIParameter{Name: "code", Required: true},
			APIParameter{Name: "date", Required: true},
			APIParameter{Name: "start", Notes: "optional page offset"},
			APIParameter{Name: "count", Notes: "optional page size capped by existing handler"},
		),
		legacy("/api/trade-history/full",
			APIParameter{Name: "code", Required: true},
			APIParameter{Name: "start_date"},
			APIParameter{Name: "end_date"},
			APIParameter{Name: "before"},
			APIParameter{Name: "include_today"},
			APIParameter{Name: "limit"},
		),
		legacy("/api/order-history",
			APIParameter{Name: "code", Required: true},
			APIParameter{Name: "date", Required: true},
		),
		legacy("/api/minute-trade-all",
			APIParameter{Name: "code", Required: true},
			APIParameter{Name: "date"},
		),
		legacy("/api/kline-history",
			APIParameter{Name: "code", Required: true},
			APIParameter{Name: "frequency"},
			APIParameter{Name: "adjust"},
			APIParameter{Name: "start_date"},
			APIParameter{Name: "end_date"},
			APIParameter{Name: "count"},
		),
		legacy("/api/kline-all",
			APIParameter{Name: "code", Required: true},
			APIParameter{Name: "type"},
			APIParameter{Name: "limit"},
		),
		legacy("/api/kline-all/tdx",
			APIParameter{Name: "code", Required: true},
			APIParameter{Name: "type"},
			APIParameter{Name: "limit"},
		),
		legacy("/api/kline-all/ths",
			APIParameter{Name: "code", Required: true},
			APIParameter{Name: "type"},
			APIParameter{Name: "limit"},
		),
		legacy("/api/finance", APIParameter{Name: "code", Required: true}),
		legacy("/api/f10/categories", APIParameter{Name: "code", Required: true}),
		legacy("/api/f10/content",
			APIParameter{Name: "code", Required: true},
			APIParameter{Name: "filename", Required: true},
			APIParameter{Name: "start", Required: true},
			APIParameter{Name: "length", Required: true},
		),
		prof("/api/v1/prof-finance/fields",
			APIParameter{Name: "category"},
			APIParameter{Name: "query"},
		),
		prof("/api/v1/prof-finance/history",
			APIParameter{Name: "full_code", Required: true},
			APIParameter{Name: "field_codes", Required: true},
			APIParameter{Name: "as_of_date"},
			APIParameter{Name: "period"},
			APIParameter{Name: "start_report_date"},
			APIParameter{Name: "end_report_date"},
			APIParameter{Name: "limit"},
		),
		prof("/api/v1/prof-finance/snapshot",
			APIParameter{Name: "full_code", Required: true},
			APIParameter{Name: "field_codes", Required: true},
			APIParameter{Name: "report_date"},
			APIParameter{Name: "period_mode"},
			APIParameter{Name: "as_of_date"},
		),
		prof("/api/v1/prof-finance/coverage",
			APIParameter{Name: "full_code", Required: true},
			APIParameter{Name: "field_codes", Required: true},
			APIParameter{Name: "report_date"},
			APIParameter{Name: "as_of_date"},
		),
		prof("/api/v1/prof-finance/cross-section",
			APIParameter{Name: "full_codes", Required: true},
			APIParameter{Name: "field_codes", Required: true},
			APIParameter{Name: "report_date"},
			APIParameter{Name: "period_mode"},
			APIParameter{Name: "as_of_date"},
			APIParameter{Name: "limit"},
			APIParameter{Name: "cursor"},
		),
	}
}
