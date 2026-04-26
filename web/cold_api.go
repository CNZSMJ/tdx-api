package main

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/injoyai/tdx/collector/lifecycle"
)

func handleColdSegments(w http.ResponseWriter, r *http.Request) {
	if !allowColdAPI(r) {
		errorResponse(w, "cold api requires local-admin access")
		return
	}
	store, err := openColdAPIManifest()
	if err != nil {
		errorResponse(w, "读取 cold manifest 失败: "+err.Error())
		return
	}
	if store == nil {
		successResponse(w, map[string]any{"count": 0, "segments": []any{}})
		return
	}
	defer store.Close()
	segments, err := store.ListSegments()
	if err != nil {
		errorResponse(w, "读取 cold segments 失败: "+err.Error())
		return
	}
	successResponse(w, map[string]any{"count": len(segments), "segments": segments})
}

func handleColdTradeHistory(w http.ResponseWriter, r *http.Request) {
	if !allowColdAPI(r) {
		errorResponse(w, "cold api requires local-admin access")
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	startDate := normalizeDateString(r.URL.Query().Get("start_date"))
	endDate := normalizeDateString(r.URL.Query().Get("end_date"))
	if code == "" || startDate == "" || endDate == "" {
		errorResponse(w, "code、start_date、end_date 为必填参数")
		return
	}
	limit := parsePositiveInt(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 5000 {
		limit = 5000
	}
	store, err := openColdAPIManifest()
	if err != nil {
		errorResponse(w, "读取 cold manifest 失败: "+err.Error())
		return
	}
	if store == nil {
		successResponse(w, map[string]any{"count": 0, "list": []any{}})
		return
	}
	defer store.Close()
	segments, err := store.ListSegments()
	if err != nil {
		errorResponse(w, "读取 cold segments 失败: "+err.Error())
		return
	}
	storage := lifecycle.NewLocalColdStorage(databaseDir)
	out := make([]lifecycle.ColdTradeHistoryRow, 0)
	for _, segment := range segments {
		if segment.Status != lifecycle.SegmentActive || segment.Domain != "trade" || segment.TableName != "TradeHistory" || segment.Instrument != code {
			continue
		}
		if segment.EndDate < startDate || segment.StartDate > endDate {
			continue
		}
		rows, err := lifecycle.ReadTradeHistoryRows(storage, segment.ColdURI)
		if err != nil {
			errorResponse(w, "读取 cold trade history 失败: "+err.Error())
			return
		}
		for _, row := range rows {
			if row.TradeDate < startDate || row.TradeDate > endDate {
				continue
			}
			out = append(out, row)
			if len(out) >= limit {
				break
			}
		}
		if len(out) >= limit {
			break
		}
	}
	successResponse(w, map[string]any{"count": len(out), "limit": limit, "list": out})
}

func allowColdAPI(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func openColdAPIManifest() (*lifecycle.ManifestStore, error) {
	manifestPath := filepath.Join(databaseDir, "cold_manifest.db")
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		return nil, nil
	}
	return lifecycle.OpenManifestStore(manifestPath)
}
