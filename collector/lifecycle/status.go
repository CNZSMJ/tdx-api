package lifecycle

import (
	"time"
)

type LifecycleStatus struct {
	GeneratedAt    time.Time        `json:"generated_at"`
	SegmentCounts  map[string]int   `json:"segment_counts"`
	RowsArchived   int64            `json:"rows_archived"`
	BytesArchived  int64            `json:"bytes_archived"`
	FailedSegments []ColdSegment    `json:"failed_segments"`
	Alerts         []LifecycleAlert `json:"alerts"`
}

type LifecycleAlert struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

func LifecycleStatusFromManifest(store *ManifestStore) (LifecycleStatus, error) {
	segments, err := store.ListSegments()
	if err != nil {
		return LifecycleStatus{}, err
	}
	status := LifecycleStatus{
		GeneratedAt:   time.Now(),
		SegmentCounts: make(map[string]int),
	}
	for _, segment := range segments {
		status.SegmentCounts[string(segment.Status)]++
		status.RowsArchived += segment.RowCount
		status.BytesArchived += segment.ByteSize
		if segment.Status == SegmentFailed {
			status.FailedSegments = append(status.FailedSegments, segment)
			if time.Since(segment.UpdatedAt) > 24*time.Hour {
				status.Alerts = append(status.Alerts, LifecycleAlert{
					Severity: "warning",
					Code:     "failed_segment_older_than_24h",
					Message:  "failed cold segment requires operator review or retry",
				})
			}
		}
	}
	return status, nil
}
