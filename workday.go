package tdx

import (
	"errors"
	_ "github.com/glebarez/go-sqlite"
	_ "github.com/go-sql-driver/mysql"
	"github.com/injoyai/base/maps"
	"github.com/injoyai/conv"
	"github.com/injoyai/ios/client"
	"github.com/injoyai/logs"
	"github.com/injoyai/tdx/protocol"
	"github.com/robfig/cron/v3"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"xorm.io/core"
	"xorm.io/xorm"
)

func DialWorkday(op ...client.Option) (*Workday, error) {
	c, err := DialDefault(op...)
	if err != nil {
		return nil, err
	}
	return NewWorkdaySqlite(c)
}

func NewWorkdayMysql(c *Client, dsn string) (*Workday, error) {

	//连接数据库
	db, err := xorm.NewEngine("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMapper(core.SameMapper{})

	return NewWorkday(c, db)
}

func NewWorkdaySqlite(c *Client, filenames ...string) (*Workday, error) {

	defaultFilename := filepath.Join(DefaultDatabaseDir, "workday.db")
	filename := conv.Default(defaultFilename, filenames...)

	//如果文件夹不存在就创建
	dir, _ := filepath.Split(filename)
	_ = os.MkdirAll(dir, 0777)

	//连接数据库
	db, err := xorm.NewEngine("sqlite", filename)
	if err != nil {
		return nil, err
	}
	db.SetMapper(core.SameMapper{})
	db.DB().SetMaxOpenConns(1)

	return NewWorkday(c, db)
}

func NewWorkday(c *Client, db *xorm.Engine) (*Workday, error) {
	if err := db.Sync2(new(WorkdayModel)); err != nil {
		return nil, err
	}

	w := &Workday{
		Client: c,
		db:     db,
		cache:  maps.NewBit(),
	}
	cacheCount, err := w.loadCache()
	if err != nil {
		return nil, err
	}
	if err := w.Update(); err != nil {
		if cacheCount == 0 {
			return nil, err
		}
		logs.Err(err)
	}
	return w, nil
}

type Workday struct {
	*Client
	db         *xorm.Engine
	cache      maps.Bit
	latestUnix int64
	task       *cron.Cron
}

func (this *Workday) loadCache() (int, error) {
	all := []*WorkdayModel(nil)
	if err := this.db.Find(&all); err != nil {
		return 0, err
	}
	normalized, changed, err := normalizeWorkdayModels(all)
	if err != nil {
		return 0, err
	}
	if changed {
		if err := rewriteWorkdayTable(this.db, normalized); err != nil {
			return 0, err
		}
	}
	if err := ensureWorkdayDateUniqueIndex(this.db); err != nil {
		return 0, err
	}

	this.cache = maps.NewBit()
	var latest int64
	for _, v := range normalized {
		this.cache.Set(uint64(v.Unix), true)
		if v.Unix > latest {
			latest = v.Unix
		}
	}
	this.latestUnix = latest
	return len(normalized), nil
}

// Update 更新
func (this *Workday) Update() error {

	if this.Client == nil {
		return errors.New("client is nil")
	}

	//获取沪市指数的日K线,用作历史是否节假日的判断依据
	//判断日K线是否拉取过

	if _, err := this.loadCache(); err != nil {
		return err
	}

	now := time.Now()
	if this.latestUnix < canonicalWorkdayTime(now).Unix() {
		resp, err := this.Client.GetIndexDayAll("sh000001")
		if err != nil {
			logs.Err(err)
			return err
		}

		inserts := []any(nil)
		var latest int64
		seenDates := make(map[string]struct{})
		for _, v := range resp.List {
			canonical := canonicalWorkdayTime(v.Time)
			unix := canonical.Unix()
			date := canonical.Format("20060102")
			if _, ok := seenDates[date]; ok {
				continue
			}
			seenDates[date] = struct{}{}
			if unix > this.latestUnix {
				inserts = append(inserts, &WorkdayModel{Unix: unix, Date: date})
				this.cache.Set(uint64(unix), true)
				if unix > latest {
					latest = unix
				}
			}
		}

		if len(inserts) == 0 {
			return nil
		}

		_, err = this.db.Insert(inserts)
		if err == nil && latest > this.latestUnix {
			this.latestUnix = latest
		}
		return err

	}

	return nil
}

func normalizeWorkdayModels(rows []*WorkdayModel) ([]*WorkdayModel, bool, error) {
	byDate := make(map[string]*WorkdayModel, len(rows))
	changed := false

	for _, row := range rows {
		if row == nil {
			continue
		}
		canonical, err := canonicalWorkdayModel(row)
		if err != nil {
			return nil, false, err
		}
		if canonical.Unix != row.Unix || canonical.Date != strings.TrimSpace(row.Date) {
			changed = true
		}
		if _, exists := byDate[canonical.Date]; exists {
			changed = true
		}
		byDate[canonical.Date] = canonical
	}

	out := make([]*WorkdayModel, 0, len(byDate))
	for _, row := range byDate {
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Unix == out[j].Unix {
			return out[i].Date < out[j].Date
		}
		return out[i].Unix < out[j].Unix
	})
	for i := 1; i < len(out); i++ {
		if out[i-1].Unix == out[i].Unix {
			return nil, false, errors.New("duplicate canonical workday unix detected")
		}
	}
	if len(out) != len(rows) {
		changed = true
	}
	return out, changed, nil
}

func canonicalWorkdayModel(row *WorkdayModel) (*WorkdayModel, error) {
	date := strings.TrimSpace(row.Date)
	if date != "" {
		t, err := time.ParseInLocation("20060102", date, time.Local)
		if err != nil {
			return nil, err
		}
		canonical := canonicalWorkdayTime(t)
		return &WorkdayModel{
			Unix: canonical.Unix(),
			Date: canonical.Format("20060102"),
		}, nil
	}
	canonical := canonicalWorkdayTime(time.Unix(row.Unix, 0).In(time.Local))
	return &WorkdayModel{
		Unix: canonical.Unix(),
		Date: canonical.Format("20060102"),
	}, nil
}

func canonicalWorkdayTime(t time.Time) time.Time {
	year, month, day := t.In(time.Local).Date()
	return time.Date(year, month, day, 15, 0, 0, 0, time.Local)
}

func rewriteWorkdayTable(db *xorm.Engine, rows []*WorkdayModel) error {
	_, err := db.Transaction(func(session *xorm.Session) (interface{}, error) {
		if _, err := session.Exec("DELETE FROM workday"); err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return nil, nil
		}
		inserts := make([]any, 0, len(rows))
		for _, row := range rows {
			inserts = append(inserts, &WorkdayModel{
				Unix: row.Unix,
				Date: row.Date,
			})
		}
		_, err := session.Insert(inserts...)
		return nil, err
	})
	return err
}

func ensureWorkdayDateUniqueIndex(db *xorm.Engine) error {
	_, err := db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS UQE_workday_Date ON workday (Date)")
	return err
}

// Is 是否是工作日
func (this *Workday) Is(t time.Time) bool {
	return this.cache.Get(uint64(IntegerDay(t).Add(time.Hour * 15).Unix()))
}

// TodayIs 今天是否是工作日
func (this *Workday) TodayIs() bool {
	return this.Is(time.Now())
}

// Latest returns the latest cached trading day known to the local workday store.
func (this *Workday) Latest() time.Time {
	if this == nil || this.latestUnix == 0 {
		return time.Time{}
	}
	return time.Unix(this.latestUnix, 0).In(time.Local)
}

// RangeYear 遍历一年的所有工作日
func (this *Workday) RangeYear(year int, f func(t time.Time) bool) {
	this.Range(
		time.Date(year, 1, 1, 0, 0, 0, 0, time.Local),
		time.Date(year, 12, 31, 0, 0, 0, 0, time.Local),
		f,
	)
}

// Range 遍历指定范围的工作日,推荐start带上时间15:00,这样当天小于15点不会触发
func (this *Workday) Range(start, end time.Time, f func(t time.Time) bool) {
	start = conv.Select(start.Before(protocol.ExchangeEstablish), protocol.ExchangeEstablish, start)
	//now := IntegerDay(time.Now())
	//end = conv.Select(end.After(now), now, end).Add(1)
	for ; start.Before(end); start = start.Add(time.Hour * 24) {
		if this.Is(start) {
			if !f(start) {
				return
			}
		}
	}
}

// RangeDesc 倒序遍历工作日,从今天-1990年12月19日(上海交易所成立时间)
func (this *Workday) RangeDesc(f func(t time.Time) bool) {
	t := IntegerDay(time.Now())
	for ; t.After(time.Date(1990, 12, 18, 0, 0, 0, 0, time.Local)); t = t.Add(-time.Hour * 24) {
		if this.Is(t) {
			if !f(t) {
				return
			}
		}
	}
}

// WorkdayModel 工作日
type WorkdayModel struct {
	ID   int64  `json:"id"`   //主键
	Unix int64  `json:"unix"` //时间戳
	Date string `json:"date"` //日期
}

func (this *WorkdayModel) TableName() string {
	return "workday"
}

func IntegerDay(t time.Time) time.Time {
	year, month, day := t.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, t.Location())
}
