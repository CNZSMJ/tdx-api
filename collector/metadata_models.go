package collector

type MetadataUpdateRecord struct {
	Key  string `xorm:"pk"`
	Time int64  `xorm:"notnull"`
}

func (*MetadataUpdateRecord) TableName() string {
	return "update"
}

type MetadataCodeRecord struct {
	ID        int64   `xorm:"pk autoincr"`
	Name      string  `xorm:"varchar(255) notnull"`
	Code      string  `xorm:"varchar(16) index notnull"`
	Exchange  string  `xorm:"varchar(16) index notnull"`
	Multiple  uint16  `xorm:"notnull"`
	Decimal   int8    `xorm:"notnull"`
	LastPrice float64 `xorm:"notnull"`
	EditDate  int64   `xorm:"updated"`
	InDate    int64   `xorm:"created"`
}

func (*MetadataCodeRecord) TableName() string {
	return "codes"
}

type MetadataCodeStagingRecord struct {
	ID        int64   `xorm:"pk autoincr"`
	Name      string  `xorm:"varchar(255) notnull"`
	Code      string  `xorm:"varchar(16) index notnull"`
	Exchange  string  `xorm:"varchar(16) index notnull"`
	Multiple  uint16  `xorm:"notnull"`
	Decimal   int8    `xorm:"notnull"`
	LastPrice float64 `xorm:"notnull"`
	EditDate  int64   `xorm:"updated"`
	InDate    int64   `xorm:"created"`
}

func (*MetadataCodeStagingRecord) TableName() string {
	return "collector_codes_staging"
}

type MetadataWorkdayRecord struct {
	ID   int64  `xorm:"pk autoincr"`
	Unix int64  `xorm:"unique notnull"`
	Date string `xorm:"varchar(16) unique notnull"`
}

func (*MetadataWorkdayRecord) TableName() string {
	return "workday"
}

type MetadataWorkdayStagingRecord struct {
	ID   int64  `xorm:"pk autoincr"`
	Unix int64  `xorm:"unique notnull"`
	Date string `xorm:"varchar(16) unique notnull"`
}

func (*MetadataWorkdayStagingRecord) TableName() string {
	return "collector_workday_staging"
}
