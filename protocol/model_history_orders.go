package protocol

import (
	"encoding/binary"
	"errors"
	"math"

	"github.com/injoyai/conv"
)

type HistoryOrdersCache struct{}

type HistoryOrdersResp struct {
	Count    uint16
	PreClose Price
	List     []*HistoryOrder
}

type HistoryOrder struct {
	Price Price
	// BuySellDelta is an inferred raw field from TDX "history orders".
	// Positive values appear to lean buy-side, negative values lean sell-side.
	BuySellDelta int
	Volume       int
}

type historyOrders struct{}

func (historyOrders) Frame(date, code string) (*Frame, error) {
	exchange, number, err := DecodeCode(code)
	if err != nil {
		return nil, err
	}
	dataBs := Bytes(conv.Uint32(date))
	dataBs = append(dataBs, exchange.Uint8())
	dataBs = append(dataBs, []byte(number)...)
	return &Frame{
		Control: Control01,
		Type:    TypeHistoryMinute,
		Data:    dataBs,
	}, nil
}

func (historyOrders) Decode(bs []byte) (*HistoryOrdersResp, error) {
	if len(bs) < 6 {
		return nil, errors.New("历史委托数据长度不足")
	}

	resp := &HistoryOrdersResp{
		Count: Uint16(bs[:2]),
	}
	preClose := math.Float32frombits(binary.LittleEndian.Uint32(bs[2:6]))
	resp.PreClose = Price(math.Round(float64(preClose) * 1000))
	bs = bs[6:]

	lastPrice := 0
	for i := uint16(0); i < resp.Count; i++ {
		var priceRaw int
		var unknown int
		var volume int

		bs, priceRaw = CutInt(bs)
		bs, unknown = CutInt(bs)
		bs, volume = CutInt(bs)

		lastPrice += priceRaw
		resp.List = append(resp.List, &HistoryOrder{
			Price:        Price(lastPrice * 10),
			BuySellDelta: unknown,
			Volume:       volume,
		})
	}

	return resp, nil
}
