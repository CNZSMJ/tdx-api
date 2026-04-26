package lifecycle

import (
	"errors"
	"strings"
)

type ObjectStorageConfig struct {
	Enabled      bool
	Scheme       string
	Bucket       string
	Prefix       string
	EndpointName string
}

type ObjectStorageRemapPlan struct {
	Items []ObjectStorageRemapItem
}

type ObjectStorageRemapItem struct {
	SegmentID       string
	LogicalURI      string
	FutureObjectURI string
}

type ManifestStorageMigrationProtocol struct {
	RequiredSteps []string
}

func DryRunObjectStorageRemap(segments []ColdSegment, cfg ObjectStorageConfig) (ObjectStorageRemapPlan, error) {
	if !cfg.Enabled {
		return ObjectStorageRemapPlan{}, errors.New("object storage config is disabled")
	}
	if cfg.Scheme == "" || cfg.Bucket == "" {
		return ObjectStorageRemapPlan{}, errors.New("object storage scheme and bucket are required")
	}
	plan := ObjectStorageRemapPlan{}
	for _, segment := range segments {
		uri, err := ParseColdURI(segment.ColdURI)
		if err != nil {
			return plan, err
		}
		parts := []string{strings.Trim(cfg.Prefix, "/"), uri.Path}
		prefixPath := strings.Trim(strings.Join(parts, "/"), "/")
		plan.Items = append(plan.Items, ObjectStorageRemapItem{
			SegmentID:       segment.SegmentID,
			LogicalURI:      segment.ColdURI,
			FutureObjectURI: cfg.Scheme + "://" + cfg.Bucket + "/" + prefixPath,
		})
	}
	return plan, nil
}

func BuildManifestStorageMigrationProtocol() ManifestStorageMigrationProtocol {
	return ManifestStorageMigrationProtocol{RequiredSteps: []string{
		"copy",
		"file_checksum",
		"logical_checksum",
		"manifest_update",
		"local_retained_until_verified",
	}}
}
