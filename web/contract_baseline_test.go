package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLifecycleAffectedLegacyEndpointsKeepErrorEnvelope(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		handler http.HandlerFunc
		wantMsg string
	}{
		{name: "trade history", path: "/api/trade-history", handler: handleGetTradeHistory, wantMsg: "code 与 date 均为必填参数"},
		{name: "trade history full", path: "/api/trade-history/full", handler: handleGetTradeHistoryFull, wantMsg: "code 为必填参数"},
		{name: "order history", path: "/api/order-history", handler: handleGetOrderHistory, wantMsg: "code 与 date 均为必填参数"},
		{name: "minute trade all", path: "/api/minute-trade-all", handler: handleGetMinuteTradeAll, wantMsg: "code 为必填参数"},
		{name: "kline history", path: "/api/kline-history", handler: handleGetKlineHistory, wantMsg: "full_code 为必填参数"},
		{name: "kline all", path: "/api/kline-all", handler: handleGetKlineAllTDX, wantMsg: "股票代码不能为空"},
		{name: "kline all tdx", path: "/api/kline-all/tdx", handler: handleGetKlineAllTDX, wantMsg: "股票代码不能为空"},
		{name: "kline all ths", path: "/api/kline-all/ths", handler: handleGetKlineAllTHS, wantMsg: "股票代码不能为空"},
		{name: "finance", path: "/api/finance", handler: handleGetFinance, wantMsg: "股票代码不能为空"},
		{name: "f10 categories", path: "/api/f10/categories", handler: handleGetF10Categories, wantMsg: "股票代码不能为空"},
		{name: "f10 content", path: "/api/f10/content?code=sh600000", handler: handleGetF10Content, wantMsg: "filename不能为空"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			tt.handler(rec, req)

			if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
				t.Fatalf("content-type = %q", ct)
			}
			var payload Response
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("unmarshal response: %v body=%s", err, rec.Body.String())
			}
			if payload.Code != -1 || payload.Data != nil || payload.Message != tt.wantMsg {
				t.Fatalf("unexpected error envelope: %+v", payload)
			}
		})
	}
}
