package lifecycle

import "testing"

func TestObjectStorageDryRunRemapPreservesSegmentIdentity(t *testing.T) {
	segments := []ColdSegment{{
		SegmentID: "seg-1",
		ColdURI:   "tdx-cold://a-stock-market-tdx/cold/domain=trade/table=TradeHistory/instrument=sh600000/year=2024/part-abcd-000.parquet",
	}}
	plan, err := DryRunObjectStorageRemap(segments, ObjectStorageConfig{
		Enabled:      true,
		Scheme:       "s3",
		Bucket:       "tdx-cold",
		Prefix:       "a-stock-market-tdx",
		EndpointName: "future-object-store",
	})
	if err != nil {
		t.Fatalf("dry-run remap: %v", err)
	}
	if len(plan.Items) != 1 {
		t.Fatalf("items = %+v", plan.Items)
	}
	item := plan.Items[0]
	if item.SegmentID != "seg-1" || item.LogicalURI != segments[0].ColdURI {
		t.Fatalf("identity changed: %+v", item)
	}
	if item.FutureObjectURI != "s3://tdx-cold/a-stock-market-tdx/cold/domain=trade/table=TradeHistory/instrument=sh600000/year=2024/part-abcd-000.parquet" {
		t.Fatalf("future uri = %s", item.FutureObjectURI)
	}
}

func TestManifestMigrationProtocolRequiresCopyVerificationBeforeUpdate(t *testing.T) {
	protocol := BuildManifestStorageMigrationProtocol()
	required := []string{"copy", "file_checksum", "logical_checksum", "manifest_update", "local_retained_until_verified"}
	for _, step := range required {
		if !containsString(protocol.RequiredSteps, step) {
			t.Fatalf("protocol missing %s: %+v", step, protocol)
		}
	}
}
