package collector

import "strings"

var blockStyleMarkers = []string{
	"msci", "qfii", "amc", "etf",
	"融资", "融券", "重仓", "金股", "成份", "成分", "宽基",
	"专精特新", "专项贷款", "新进", "增仓", "减仓",
	"新股", "次新", "近端次新", "科创次新",
	"大盘", "中盘", "小盘", "微盘", "百元股", "低价股",
	"高分红", "高股息", "高市净率", "高市盈率", "高商誉", "高应收款", "高融资盘", "高贝塔值", "高负债率", "高质押股",
	"低市净率", "低市盈率", "低安全分",
	"预高送转", "高送转", "送转", "配股", "定增", "发可转债", "含可转债",
	"回购", "重组", "收购", "举牌", "减持", "增持", "员工持股", "股权", "分拆上市", "整体上市",
	"解禁", "复牌", "摘帽", "st板块", "风险提示",
	"业绩预", "预计", "扭亏", "转亏", "亏损", "微利",
	"通达信", "活跃", "昨日", "近期", "最近", "昨", "首板", "连板", "多板", "断板", "涨停", "跌停", "异动", "情绪",
	"密集调研", "调研", "自由现金", "持续增长", "板块趋势", "轮动趋势",
	"基金", "券商", "保险", "信托", "社保", "私募", "证金汇金", "陆股通", "北上", "沪港通", "深港通", "台资", "外资", "国开持股", "大基金",
	"高校背景", "外资背景", "台资背景", "中字头", "中特估", "国企",
	"周期股", "非周期股",
	"个人持股", "持股", "分红", "历史新", "参股", "减值", "壳资源",
	"户数", "承诺不减", "控制变更", "机构吸筹", "海外业务",
	"破净", "破发行", "破增发", "成长层", "绩优股", "行业龙头", "主营变更",
}

var blockIndexMarkers = []string{
	"指数", "成指", "沪深300", "上证50", "上证180", "上证治理", "上证混改", "上证超大",
	"深证50", "深证成指", "创业板指", "科创50", "北证50",
	"中证", "国证", "mscia50", "央视50", "银河99", "腾讯济安", "中华a80",
}

func classifyBlockType(source, name string) BlockType {
	source = strings.ToLower(strings.TrimSpace(source))
	name = strings.TrimSpace(name)
	if name == "" {
		return BlockTypeIndexBlock
	}

	switch source {
	case "block_gn.dat":
		return BlockTypeConcept
	case "block_zs.dat":
		return BlockTypeIndexBlock
	case "block_fg.dat":
		if isStyleBlockName(name) {
			return BlockTypeStyle
		}
		return BlockTypeIndustry
	case "block.dat":
		switch {
		case isIndexBlockName(name):
			return BlockTypeIndexBlock
		case isStyleBlockName(name):
			return BlockTypeStyle
		default:
			return BlockTypeConcept
		}
	default:
		switch {
		case isIndexBlockName(name):
			return BlockTypeIndexBlock
		case isStyleBlockName(name):
			return BlockTypeStyle
		case strings.Contains(name, "概念"):
			return BlockTypeConcept
		default:
			return BlockTypeIndustry
		}
	}
}

func normalizeBlockType(source, name, current string) string {
	classified := string(classifyBlockType(source, name))
	if classified != "" {
		return classified
	}
	return current
}

func isStyleBlockName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	for _, marker := range blockStyleMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func isIndexBlockName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	for _, marker := range blockIndexMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
