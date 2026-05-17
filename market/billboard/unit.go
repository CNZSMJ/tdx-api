package billboard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

const (
	ReportDailyDetails             = "RPT_DAILYBILLBOARD_DETAILSNEW"
	ReportBuyDetails               = "RPT_BILLBOARD_DAILYDETAILSBUY"
	ReportSellDetails              = "RPT_BILLBOARD_DAILYDETAILSSELL"
	ReportTradeAll                 = "RPT_BILLBOARD_TRADEALL"
	ReportOrganizationTradeDetails = "RPT_ORGANIZATION_TRADE_DETAILS"
)

type Unit string

const (
	UnitUnknown       Unit = ""
	UnitCNY           Unit = "CNY"
	UnitCNY100M       Unit = "CNY_100M"
	UnitShare         Unit = "SHARE"
	UnitPercentValue  Unit = "PERCENT_VALUE"
	UnitRatioFraction Unit = "RATIO_FRACTION"
)

type InstrumentIdentity struct {
	FullCode  string
	Code      string
	Exchange  string
	AssetType string
}

var secuCodePattern = regexp.MustCompile(`^([0-9]{6})\.([A-Z]{2})$`)

func NormalizeSecuCode(secucode string) (InstrumentIdentity, bool) {
	match := secuCodePattern.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(secucode)))
	if len(match) != 3 {
		return InstrumentIdentity{}, false
	}
	code := match[1]
	exchange := strings.ToLower(match[2])
	if !isSupportedAStock(exchange, code) {
		return InstrumentIdentity{}, false
	}
	return InstrumentIdentity{
		FullCode:  exchange + code,
		Code:      code,
		Exchange:  exchange,
		AssetType: "stock",
	}, true
}

func isSupportedAStock(exchange, code string) bool {
	if len(code) != 6 {
		return false
	}
	switch exchange {
	case "sh":
		return hasAnyPrefix(code, "600", "601", "603", "605", "688", "689")
	case "sz":
		return hasAnyPrefix(code, "000", "001", "002", "003", "300", "301")
	case "bj":
		return true
	default:
		return false
	}
}

func hasAnyPrefix(value string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func UnitForField(reportName, field string) Unit {
	field = strings.ToUpper(strings.TrimSpace(field))
	switch reportName {
	case ReportDailyDetails:
		switch field {
		case "BILLBOARD_NET_AMT", "BILLBOARD_BUY_AMT", "BILLBOARD_SELL_AMT", "BILLBOARD_DEAL_AMT", "ACCUM_AMOUNT", "FREE_MARKET_CAP", "CLOSE_PRICE":
			return UnitCNY
		case "DEAL_NET_RATIO", "DEAL_AMOUNT_RATIO", "TURNOVERRATE", "CHANGE_RATE", "D1_CLOSE_ADJCHRATE", "D2_CLOSE_ADJCHRATE", "D3_CLOSE_ADJCHRATE", "D5_CLOSE_ADJCHRATE", "D10_CLOSE_ADJCHRATE":
			return UnitPercentValue
		}
	case ReportBuyDetails, ReportSellDetails:
		switch field {
		case "BUY", "SELL", "NET", "ACCUM_AMOUNT", "CLOSE_PRICE":
			return UnitCNY
		case "ACCUM_VOLUME":
			return UnitShare
		case "CHANGE_RATE", "RISE_PROBABILITY_3DAY":
			return UnitPercentValue
		case "TOTAL_BUYRIO", "TOTAL_SELLRIO":
			return UnitRatioFraction
		}
	case ReportOrganizationTradeDetails:
		switch field {
		case "BUY_AMT", "SELL_AMT", "NET_BUY_AMT", "ACCUM_AMOUNT", "CLOSE_PRICE":
			return UnitCNY
		case "FREECAP":
			return UnitCNY100M
		case "CHANGE_RATE", "RATIO", "TURNOVERRATE", "D1_CLOSE_ADJCHRATE", "D2_CLOSE_ADJCHRATE", "D3_CLOSE_ADJCHRATE", "D5_CLOSE_ADJCHRATE", "D10_CLOSE_ADJCHRATE":
			return UnitPercentValue
		}
	case ReportTradeAll:
		switch field {
		case "BILLBOARD_DEAL_AMT", "BILLBOARD_NET_BUY", "ORG_DEAL_AMT", "ORG_NET_BUY", "BILLBOARD_BUY_AMT", "BILLBOARD_SELL_AMT", "ORG_BUY_AMT", "ORG_SELL_AMT":
			return UnitCNY
		case "IPCT1M", "IPCT3M", "IPCT6M", "IPCT1Y", "CHANGE_RATE":
			return UnitPercentValue
		}
	}
	return UnitUnknown
}

func NormalizeMilli(reportName, field string, value any) (int64, error) {
	f, ok := numberValue(value)
	if !ok {
		return 0, fmt.Errorf("%s.%s is not numeric: %v", reportName, field, value)
	}
	switch UnitForField(reportName, field) {
	case UnitCNY:
		return int64(math.Round(f * 1000)), nil
	case UnitCNY100M:
		return int64(math.Round(f * 100000000 * 1000)), nil
	default:
		return 0, fmt.Errorf("%s.%s is not a money field", reportName, field)
	}
}

func OptionalMilli(reportName, field string, row map[string]any) int64 {
	value, ok := row[field]
	if !ok || value == nil {
		return 0
	}
	milli, err := NormalizeMilli(reportName, field, value)
	if err != nil {
		return 0
	}
	return milli
}

func OptionalFloat(row map[string]any, field string) float64 {
	value, ok := row[field]
	if !ok || value == nil {
		return 0
	}
	f, _ := numberValue(value)
	return f
}

func OptionalInt(row map[string]any, field string) int {
	value, ok := row[field]
	if !ok || value == nil {
		return 0
	}
	f, ok := numberValue(value)
	if !ok {
		return 0
	}
	return int(math.Round(f))
}

func StringField(row map[string]any, field string) string {
	value, ok := row[field]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func numberValue(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case string:
		if strings.TrimSpace(v) == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func HashText(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}

func ParseEastmoneyDate(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 10 && value[4] == '-' && value[7] == '-' {
		return value[0:4] + value[5:7] + value[8:10]
	}
	if len(value) >= 8 {
		return value[:8]
	}
	return value
}
