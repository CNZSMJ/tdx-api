package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
)

type FinanceInfo struct {
	Market             Exchange
	Code               string
	Liutongguben       float64
	Province           uint16
	Industry           uint16
	UpdatedDate        uint32
	IpoDate            uint32
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

type financeInfo struct{}

func (financeInfo) Frame(code string) (*Frame, error) {
	exchange, number, err := DecodeCode(code)
	if err != nil {
		return nil, err
	}
	data := []byte{0x01, 0x00, exchange.Uint8()}
	data = append(data, []byte(number)...)
	return &Frame{
		Control: Control01,
		Type:    TypeFinanceInfo,
		Data:    data,
	}, nil
}

func (financeInfo) Decode(bs []byte) (*FinanceInfo, error) {
	if len(bs) < 9 {
		return nil, errors.New("财务数据长度不足")
	}

	reader := bytes.NewReader(bs)

	var count uint16
	if err := binary.Read(reader, binary.LittleEndian, &count); err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, errors.New("未返回财务数据")
	}

	resp := &FinanceInfo{}

	var market uint8
	if err := binary.Read(reader, binary.LittleEndian, &market); err != nil {
		return nil, err
	}
	resp.Market = Exchange(market)

	codeBytes := make([]byte, 6)
	if _, err := reader.Read(codeBytes); err != nil {
		return nil, err
	}
	resp.Code = string(codeBytes)

	var firstFloat float32
	if err := binary.Read(reader, binary.LittleEndian, &firstFloat); err != nil {
		return nil, err
	}
	resp.Liutongguben = float64(firstFloat) * 10000

	if err := binary.Read(reader, binary.LittleEndian, &resp.Province); err != nil {
		return nil, err
	}
	if err := binary.Read(reader, binary.LittleEndian, &resp.Industry); err != nil {
		return nil, err
	}
	if err := binary.Read(reader, binary.LittleEndian, &resp.UpdatedDate); err != nil {
		return nil, err
	}
	if err := binary.Read(reader, binary.LittleEndian, &resp.IpoDate); err != nil {
		return nil, err
	}

	values := make([]float32, 30)
	for i := range values {
		if err := binary.Read(reader, binary.LittleEndian, &values[i]); err != nil {
			return nil, err
		}
	}

	scale := func(v float32) float64 {
		return float64(v) * 10000
	}

	resp.Zongguben = scale(values[0])
	resp.Guojiagu = scale(values[1])
	resp.Faqirenfarengu = scale(values[2])
	resp.Farengu = scale(values[3])
	resp.Bgu = scale(values[4])
	resp.Hgu = scale(values[5])
	resp.Zhigonggu = scale(values[6])
	resp.Zongzichan = scale(values[7])
	resp.Liudongzichan = scale(values[8])
	resp.Gudingzichan = scale(values[9])
	resp.Wuxingzichan = scale(values[10])
	resp.Gudongrenshu = float64(values[11])
	resp.Liudongfuzhai = scale(values[12])
	resp.Changqifuzhai = scale(values[13])
	resp.Zibengongjijin = scale(values[14])
	resp.Jingzichan = scale(values[15])
	resp.Zhuyingshouru = scale(values[16])
	resp.Zhuyinglirun = scale(values[17])
	resp.Yingshouzhangkuan = scale(values[18])
	resp.Yingyelirun = scale(values[19])
	resp.Touzishouyu = scale(values[20])
	resp.Jingyingxianjinliu = scale(values[21])
	resp.Zongxianjinliu = scale(values[22])
	resp.Cunhuo = scale(values[23])
	resp.Lirunzonghe = scale(values[24])
	resp.Shuihoulirun = scale(values[25])
	resp.Jinglirun = scale(values[26])
	resp.Weifenpeilirun = scale(values[27])
	resp.Meigujingzichan = float64(values[28])
	resp.Baoliu2 = float64(values[29])

	return resp, nil
}
