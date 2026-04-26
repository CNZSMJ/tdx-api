package lifecycle

import (
	"fmt"
	"reflect"

	"github.com/injoyai/tdx/collector"
)

type PriceMilliContractReport struct {
	Fields map[string]string
}

func ValidatePriceMilliContract() (PriceMilliContractReport, error) {
	report := PriceMilliContractReport{Fields: make(map[string]string)}
	priceType := reflect.TypeOf(collector.PriceMilli(0))
	checks := []struct {
		row    any
		fields []string
	}{
		{collector.TradeHistoryRow{}, []string{"Price"}},
		{collector.TradeBarRow{}, []string{"Open", "High", "Low", "Close", "Amount"}},
		{collector.TradeLiveRow{}, []string{"Price"}},
		{collector.MinuteLiveRow{}, []string{"Price"}},
		{collector.QuoteSnapshotRow{}, []string{"Last", "PreClose", "Open", "High", "Low"}},
		{collector.OrderHistoryRow{}, []string{"Price"}},
	}
	for _, check := range checks {
		typ := reflect.TypeOf(check.row)
		for _, fieldName := range check.fields {
			field, ok := typ.FieldByName(fieldName)
			key := typ.Name() + "." + fieldName
			if !ok {
				return report, fmt.Errorf("%s missing", key)
			}
			if field.Type != priceType {
				return report, fmt.Errorf("%s type = %s, want PriceMilli", key, field.Type)
			}
			report.Fields[key] = "PriceMilli"
		}
	}
	return report, nil
}
