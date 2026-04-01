package collector

import (
	"fmt"
	"time"
)

type AssetType string

const (
	AssetTypeUnknown AssetType = "unknown"
	AssetTypeStock   AssetType = "stock"
	AssetTypeETF     AssetType = "etf"
	AssetTypeIndex   AssetType = "index"
)

type KlinePeriod string

const (
	PeriodMinute   KlinePeriod = "minute"
	Period5Minute  KlinePeriod = "5minute"
	Period15Minute KlinePeriod = "15minute"
	Period30Minute KlinePeriod = "30minute"
	Period60Minute KlinePeriod = "60minute"
	PeriodDay      KlinePeriod = "day"
	PeriodWeek     KlinePeriod = "week"
	PeriodMonth    KlinePeriod = "month"
	PeriodQuarter  KlinePeriod = "quarter"
	PeriodYear     KlinePeriod = "year"
)

// PriceMilli stores prices using the project-wide "厘" unit.
type PriceMilli int64

func (p PriceMilli) Float64() float64 {
	return float64(p) / 1000
}

func (p PriceMilli) String() string {
	return fmt.Sprintf("%.3f", p.Float64())
}

type Instrument struct {
	Code      string
	Name      string
	Exchange  string
	AssetType AssetType
	Multiple  uint16
	Decimal   int8
	LastPrice PriceMilli
	Source    string
}

type TradingDay struct {
	Date string
	Time time.Time
}

type QuoteLevel struct {
	Price  PriceMilli
	Number int
}

type QuoteSnapshot struct {
	Code        string
	Name        string
	Exchange    string
	AssetType   AssetType
	ServerTime  string
	PreClose    PriceMilli
	Open        PriceMilli
	High        PriceMilli
	Low         PriceMilli
	Last        PriceMilli
	VolumeHand  int64
	AmountYuan  float64
	InsideHand  int64
	OutsideHand int64
	BuyLevels   []QuoteLevel
	SellLevels  []QuoteLevel
}

type MinutePoint struct {
	Code   string
	Date   string
	Clock  string
	Price  PriceMilli
	Number int
}

type KlineBar struct {
	Code       string
	AssetType  AssetType
	Period     KlinePeriod
	Time       time.Time
	PrevClose  PriceMilli
	Open       PriceMilli
	High       PriceMilli
	Low        PriceMilli
	Close      PriceMilli
	VolumeHand int64
	Amount     PriceMilli
	UpCount    int
	DownCount  int
}

type TradeTick struct {
	Code       string
	Time       time.Time
	Price      PriceMilli
	VolumeHand int
	Number     int
	StatusCode int
	Side       string
}

type OrderHistoryEntry struct {
	Price        PriceMilli
	BuySellDelta int
	Volume       int
}

type OrderHistorySnapshot struct {
	Code     string
	Date     string
	PreClose PriceMilli
	Items    []OrderHistoryEntry
}

type FinanceSnapshot struct {
	Code               string
	Market             string
	UpdatedDate        string
	IPODate            string
	Liutongguben       float64
	Zongguben          float64
	Guojiagu           float64
	Faqirenfarengu     float64
	Farengu            float64
	Bgu                float64
	Hgu                float64
	Zhigonggu          float64
	Zongzichan         float64
	Liudongzichan      float64
	Gudingzichan       float64
	Wuxingzichan       float64
	Gudongrenshu       float64
	Liudongfuzhai      float64
	Changqifuzhai      float64
	Zibengongjijin     float64
	Jingzichan         float64
	Zhuyingshouru      float64
	Zhuyinglirun       float64
	Yingshouzhangkuan  float64
	Yingyelirun        float64
	Touzishouyu        float64
	Jingyingxianjinliu float64
	Zongxianjinliu     float64
	Cunhuo             float64
	Lirunzonghe        float64
	Shuihoulirun       float64
	Jinglirun          float64
	Weifenpeilirun     float64
	Meigujingzichan    float64
	Baoliu2            float64
}

type F10Category struct {
	Code     string
	Name     string
	Filename string
	Start    uint32
	Length   uint32
}

type F10Content struct {
	Code     string
	Filename string
	Start    uint32
	Length   uint32
	Content  string
}

type InstrumentQuery struct {
	AssetTypes []AssetType
	Limit      int
	Refresh    bool
}

type TradingDayQuery struct {
	Start   time.Time
	End     time.Time
	Refresh bool
}

type KlineQuery struct {
	Code      string
	AssetType AssetType
	Period    KlinePeriod
	Limit     int
	Since     time.Time
}

type MinuteQuery struct {
	Code string
	Date string
}

type TradeHistoryQuery struct {
	Code string
	Date string
}

type OrderHistoryQuery struct {
	Code string
	Date string
}

type F10ContentQuery struct {
	Code     string
	Filename string
	Start    uint32
	Length   uint32
}
