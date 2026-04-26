package main

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/injoyai/tdx/collector/lifecycle"
)

func handleCollectorLifecycleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errorResponse(w, "只支持GET请求")
		return
	}
	manifestPath := filepath.Join(databaseDir, "cold_manifest.db")
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		successResponse(w, lifecycle.LifecycleStatus{
			SegmentCounts: map[string]int{},
		})
		return
	}
	store, err := lifecycle.OpenManifestStore(manifestPath)
	if err != nil {
		errorResponse(w, "读取 lifecycle manifest 失败: "+err.Error())
		return
	}
	defer store.Close()
	status, err := lifecycle.LifecycleStatusFromManifest(store)
	if err != nil {
		errorResponse(w, "读取 lifecycle 状态失败: "+err.Error())
		return
	}
	successResponse(w, status)
}
