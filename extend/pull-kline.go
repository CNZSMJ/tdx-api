package extend

import (
	"context"
	_ "github.com/glebarez/go-sqlite"
	"github.com/injoyai/base/chans"
	"github.com/injoyai/logs"
	"github.com/injoyai/tdx"
	"github.com/injoyai/tdx/protocol"
	"os"
	"path/filepath"
	"sort"
	"time"
	"xorm.io/core"
	"xorm.io/xorm"
)

const (
	Minute   = "minute"
	Minute5  = "5minute"
	Minute15 = "15minute"
	Minute30 = "30minute"
	Hour     = "hour"
	Day      = "day"
	Week     = "week"
	Month    = "month"
	Quarter  = "quarter"
	Year     = "year"

	tableMinute   = "MinuteKline"
	table5Minute  = "Minute5Kline"
	table15Minute = "Minute15Kline"
	table30Minute = "Minute30Kline"
	tableHour     = "HourKline"
	tableDay      = "DayKline"
	tableWeek     = "WeekKline"
	tableMonth    = "MonthKline"
	tableQuarter  = "QuarterKline"
	tableYear     = "YearKline"

	AssetStock = "stock"
	AssetETF   = "etf"
	AssetIndex = "index"
)

var (
	AllKlineType  = []string{Minute, Minute5, Minute15, Minute30, Hour, Day, Week, Month, Quarter, Year}
	KlineTableMap = map[string]*KlineTable{
		Minute: NewKlineTable(
			tableMinute,
			func(c *tdx.Client) KlineHandler { return c.GetKlineMinuteUntil },
			func(c *tdx.Client) KlineHandler { return indexKlineHandler(c, protocol.TypeKlineMinute) },
		),
		Minute5: NewKlineTable(
			table5Minute,
			func(c *tdx.Client) KlineHandler { return c.GetKline5MinuteUntil },
			func(c *tdx.Client) KlineHandler { return indexKlineHandler(c, protocol.TypeKline5Minute) },
		),
		Minute15: NewKlineTable(
			table15Minute,
			func(c *tdx.Client) KlineHandler { return c.GetKline15MinuteUntil },
			func(c *tdx.Client) KlineHandler { return indexKlineHandler(c, protocol.TypeKline15Minute) },
		),
		Minute30: NewKlineTable(
			table30Minute,
			func(c *tdx.Client) KlineHandler { return c.GetKline30MinuteUntil },
			func(c *tdx.Client) KlineHandler { return indexKlineHandler(c, protocol.TypeKline30Minute) },
		),
		Hour: NewKlineTable(
			tableHour,
			func(c *tdx.Client) KlineHandler { return c.GetKlineHourUntil },
			func(c *tdx.Client) KlineHandler { return indexKlineHandler(c, protocol.TypeKline60Minute) },
		),
		Day: NewKlineTable(
			tableDay,
			func(c *tdx.Client) KlineHandler { return c.GetKlineDayUntil },
			func(c *tdx.Client) KlineHandler { return indexKlineHandler(c, protocol.TypeKlineDay) },
		),
		Week: NewKlineTable(
			tableWeek,
			func(c *tdx.Client) KlineHandler { return c.GetKlineWeekUntil },
			func(c *tdx.Client) KlineHandler { return indexKlineHandler(c, protocol.TypeKlineWeek) },
		),
		Month: NewKlineTable(
			tableMonth,
			func(c *tdx.Client) KlineHandler { return c.GetKlineMonthUntil },
			func(c *tdx.Client) KlineHandler { return indexKlineHandler(c, protocol.TypeKlineMonth) },
		),
		Quarter: NewKlineTable(
			tableQuarter,
			func(c *tdx.Client) KlineHandler { return c.GetKlineQuarterUntil },
			func(c *tdx.Client) KlineHandler { return indexKlineHandler(c, protocol.TypeKlineQuarter) },
		),
		Year: NewKlineTable(
			tableYear,
			func(c *tdx.Client) KlineHandler { return c.GetKlineYearUntil },
			func(c *tdx.Client) KlineHandler { return indexKlineHandler(c, protocol.TypeKlineYear) },
		),
	}
)

func indexKlineHandler(c *tdx.Client, kind uint8) KlineHandler {
	return func(code string, f func(k *protocol.Kline) bool) (*protocol.KlineResp, error) {
		return c.GetIndexUntil(kind, code, f)
	}
}

type PullKlineConfig struct {
	Codes      []string  //操作代码
	IndexCodes []string  //临时覆盖的指数代码
	AssetTypes []string  //采集对象类型
	Tables     []string  //数据类型
	Dir        string    //数据位置
	Limit      int       //协程数量
	StartAt    time.Time //数据开始时间
}

func NewPullKline(cfg PullKlineConfig) *PullKline {
	_tables := []*KlineTable(nil)
	for _, v := range cfg.Tables {
		_tables = append(_tables, KlineTableMap[v])
	}
	if cfg.Limit <= 0 {
		cfg.Limit = 1
	}
	if len(cfg.Dir) == 0 {
		cfg.Dir = filepath.Join(tdx.DefaultDatabaseDir, "kline")
	}
	return &PullKline{
		tables: _tables,
		Config: cfg,
	}
}

type PullKline struct {
	tables []*KlineTable
	Config PullKlineConfig
}

func (this *PullKline) Name() string {
	return "拉取k线数据"
}

func (this *PullKline) DayKlines(code string) (Klines, error) {
	//连接数据库
	db, err := xorm.NewEngine("sqlite", filepath.Join(this.Config.Dir, code+".db"))
	if err != nil {
		return nil, err
	}
	db.SetMapper(core.SameMapper{})
	db.DB().SetMaxOpenConns(1)
	defer db.Close()

	data := Klines{}
	err = db.Table(tableDay).Asc("date").Find(&data)
	return data, err
}

func (this *PullKline) Run(ctx context.Context, m *tdx.Manage) error {
	limit := chans.NewWaitLimit(this.Config.Limit)

	//1. 获取采集代码池
	codes := this.resolveCodes(m)
	if len(codes) == 0 {
		return nil
	}

	for _, v := range codes {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		limit.Add()
		go func(code string) {
			defer limit.Done()

			_ = os.MkdirAll(this.Config.Dir, 0777)

			//连接数据库
			db, err := xorm.NewEngine("sqlite", filepath.Join(this.Config.Dir, code+".db"))
			if err != nil {
				logs.Err(err)
				return
			}
			defer db.Close()
			db.SetMapper(core.SameMapper{})
			db.DB().SetMaxOpenConns(1)

			for _, table := range this.tables {
				if table == nil {
					continue
				}

				select {
				case <-ctx.Done():
					return
				default:
				}

				logs.PrintErr(db.Sync2(table))

				//2. 获取最后一条数据
				last := new(Kline)
				if _, err = db.Table(table).Desc("Date").Get(last); err != nil {
					logs.Err(err)
					return
				}

				//3. 从服务器获取数据
				insert := Klines{}
				err = m.Do(func(c *tdx.Client) error {
					insert, err = this.pull(code, last.Date, this.handlerForCode(table, c, code))
					return err
				})
				if err != nil {
					logs.Err(err)
					return
				}

				//4. 插入数据库
				err = tdx.NewSessionFunc(db, func(session *xorm.Session) error {
					for i, v := range insert {
						if i == 0 {
							if _, err := session.Table(table).Where("Date >= ?", v.Date).Delete(); err != nil {
								return err
							}
						}
						if _, err := session.Table(table).Insert(v); err != nil {
							return err
						}
					}
					return nil
				})
				logs.PrintErr(err)

			}

		}(v)
	}
	limit.Wait()
	return nil
}

func (this *PullKline) resolveCodes(m *tdx.Manage) []string {
	if len(this.Config.Codes) > 0 {
		return uniqueCodes(this.Config.Codes)
	}

	assetTypes := this.Config.AssetTypes
	if len(assetTypes) == 0 {
		assetTypes = []string{AssetStock}
	}

	merged := make([]string, 0)
	for _, assetType := range assetTypes {
		switch assetType {
		case AssetStock:
			merged = append(merged, m.Codes.GetStocks()...)
		case AssetETF:
			merged = append(merged, m.Codes.GetETFs()...)
		case AssetIndex:
			if len(this.Config.IndexCodes) > 0 {
				merged = append(merged, this.Config.IndexCodes...)
			} else {
				merged = append(merged, m.Codes.GetIndexes()...)
			}
		}
	}
	return uniqueCodes(merged)
}

func (this *PullKline) handlerForCode(table *KlineTable, c *tdx.Client, code string) KlineHandler {
	if protocol.IsIndex(code) && table.IndexHandler != nil {
		return table.IndexHandler(c)
	}
	return table.Handler(c)
}

func uniqueCodes(codes []string) []string {
	result := make([]string, 0, len(codes))
	seen := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		if code == "" {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		result = append(result, code)
	}
	return result
}

func (this *PullKline) pull(code string, lastDate int64, f func(code string, f func(k *protocol.Kline) bool) (*protocol.KlineResp, error)) (Klines, error) {

	if lastDate == 0 {
		lastDate = protocol.ExchangeEstablish.Unix()
	}

	resp, err := f(code, func(k *protocol.Kline) bool {
		return k.Time.Unix() <= lastDate || k.Time.Unix() <= this.Config.StartAt.Unix()
	})
	if err != nil {
		return nil, err
	}

	ks := Klines{}
	for _, v := range resp.List {
		ks = append(ks, &Kline{
			Code:   code,
			Date:   v.Time.Unix(),
			Open:   v.Open,
			High:   v.High,
			Low:    v.Low,
			Close:  v.Close,
			Volume: v.Volume,
			Amount: v.Amount,
		})
	}

	return ks, nil
}

type Kline struct {
	Code   string         `json:"code"`                  //代码
	Date   int64          `json:"date"`                  //时间节点 2006-01-02 15:00
	Open   protocol.Price `json:"open"`                  //开盘价
	High   protocol.Price `json:"high"`                  //最高价
	Low    protocol.Price `json:"low"`                   //最低价
	Close  protocol.Price `json:"close"`                 //收盘价
	Volume int64          `json:"volume"`                //成交量
	Amount protocol.Price `json:"amount"`                //成交额
	InDate int64          `json:"inDate" xorm:"created"` //创建时间
}

type Klines []*Kline

func (this Klines) Less(i, j int) bool { return this[i].Code > this[j].Code }

func (this Klines) Swap(i, j int) { this[i], this[j] = this[j], this[i] }

func (this Klines) Len() int { return len(this) }

func (this Klines) Sort() { sort.Sort(this) }

// Kline 计算多个K线,成一个K线
func (this Klines) Kline() *Kline {
	if this == nil {
		return new(Kline)
	}
	k := new(Kline)
	for i, v := range this {
		switch i {
		case 0:
			k.Open = v.Open
			k.High = v.High
			k.Low = v.Low
			k.Close = v.Close
		case len(this) - 1:
			k.Close = v.Close
			k.Date = v.Date
		}
		if v.High > k.High {
			k.High = v.High
		}
		if v.Low < k.Low {
			k.Low = v.Low
		}
		k.Volume += v.Volume
		k.Amount += v.Amount
	}

	return k
}

// Merge 合并K线
func (this Klines) Merge(n int) Klines {
	if this == nil {
		return nil
	}
	ks := []*Kline(nil)
	for i := 0; i < len(this); i += n {
		if i+n > len(this) {
			ks = append(ks, this[i:].Kline())
		} else {
			ks = append(ks, this[i:i+n].Kline())
		}
	}
	return ks
}

type KlineHandler func(code string, f func(k *protocol.Kline) bool) (*protocol.KlineResp, error)

func NewKlineTable(tableName string, handler, indexHandler func(c *tdx.Client) KlineHandler) *KlineTable {
	return &KlineTable{
		tableName:    tableName,
		Handler:      handler,
		IndexHandler: indexHandler,
	}
}

type KlineTable struct {
	Kline        `xorm:"extends"`
	tableName    string
	Handler      func(c *tdx.Client) KlineHandler `xorm:"-"`
	IndexHandler func(c *tdx.Client) KlineHandler `xorm:"-"`
}

func (this *KlineTable) TableName() string {
	return this.tableName
}
