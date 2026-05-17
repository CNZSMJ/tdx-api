package rpt

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientQueryAllPaginatesAndSendsReportParameters(t *testing.T) {
	var seenPages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("reportName") != "RPT_DAILYBILLBOARD_DETAILSNEW" {
			t.Fatalf("reportName = %s", r.URL.Query().Get("reportName"))
		}
		if r.URL.Query().Get("pageSize") != "2" {
			t.Fatalf("pageSize = %s", r.URL.Query().Get("pageSize"))
		}
		if r.URL.Query().Get("filter") != "(TRADE_DATE<='2024-05-15')" {
			t.Fatalf("filter = %s", r.URL.Query().Get("filter"))
		}
		page := r.URL.Query().Get("pageNumber")
		seenPages = append(seenPages, page)
		data := []map[string]any{}
		switch page {
		case "1":
			data = []map[string]any{{"SECURITY_CODE": "600000", "VALUE": 1.23}, {"SECURITY_CODE": "000001"}}
		case "2":
			data = []map[string]any{{"SECURITY_CODE": "920118"}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"result": map[string]any{
				"pages": 2,
				"count": 3,
				"data":  data,
			},
		})
	}))
	defer server.Close()

	client := NewClient(ClientConfig{Endpoint: server.URL, PageSize: 2, RetryCount: 0})
	rows, err := client.QueryAll(t.Context(), Query{
		ReportName: "RPT_DAILYBILLBOARD_DETAILSNEW",
		Columns:    []string{"SECURITY_CODE"},
		Filter:     "(TRADE_DATE<='2024-05-15')",
	})
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	if _, ok := rows[0]["VALUE"].(json.Number); !ok {
		t.Fatalf("VALUE type = %T, want json.Number", rows[0]["VALUE"])
	}
	if len(seenPages) != 2 || seenPages[0] != "1" || seenPages[1] != "2" {
		t.Fatalf("seen pages = %#v, want [1 2]", seenPages)
	}
}
