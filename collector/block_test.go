package collector

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestBlockServiceSyncBlocksPersistsAndQueriesFromCache(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	provider := &blockStubProvider{
		blockFiles: map[string][]BlockInfo{
			"block_gn.dat": {
				{
					Name:      "白酒概念",
					BlockType: BlockTypeConcept,
					Source:    "block_gn.dat",
					Codes:     []string{"600519", "000568"},
				},
			},
			"block_fg.dat": {
				{
					Name:      "白酒",
					BlockType: BlockTypeIndustry,
					Source:    "block_fg.dat",
					Codes:     []string{"600519"},
				},
			},
		},
	}

	service, err := NewBlockService(store, provider, BlockConfig{
		BaseDir:            filepath.Join(tmp, "block"),
		DisableAutoRefresh: true,
		Now: func() time.Time {
			return time.Date(2026, 4, 17, 9, 30, 0, 0, time.Local)
		},
	})
	if err != nil {
		t.Fatalf("new block service: %v", err)
	}
	defer service.Close()

	if err := service.SyncBlocks(context.Background()); err != nil {
		t.Fatalf("sync blocks: %v", err)
	}

	if got := service.SearchBlocks("白酒"); len(got) != 2 {
		t.Fatalf("search blocks len = %d, want 2", len(got))
	}
	if got := service.GetBlockMembers("block_gn.dat", "concept", "白酒概念"); len(got) != 2 {
		t.Fatalf("concept members len = %d, want 2", len(got))
	}
	if got := service.GetStockBlocks("sh600519"); len(got) != 2 {
		t.Fatalf("stock blocks len = %d, want 2", len(got))
	}
}

func TestBlockServiceSyncBlocksPreservesExistingDataOnEmptyRefresh(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	baseDir := filepath.Join(tmp, "block")
	service, err := NewBlockService(store, &blockStubProvider{
		blockFiles: map[string][]BlockInfo{
			"block_gn.dat": {
				{
					Name:      "白酒概念",
					BlockType: BlockTypeConcept,
					Source:    "block_gn.dat",
					Codes:     []string{"600519", "000568"},
				},
			},
			"block_fg.dat": {
				{
					Name:      "白酒",
					BlockType: BlockTypeIndustry,
					Source:    "block_fg.dat",
					Codes:     []string{"600519"},
				},
			},
		},
	}, BlockConfig{
		BaseDir:            baseDir,
		DisableAutoRefresh: true,
		Now:                func() time.Time { return time.Date(2026, 4, 17, 9, 30, 0, 0, time.Local) },
	})
	if err != nil {
		t.Fatalf("new block service: %v", err)
	}
	defer service.Close()

	if err := service.SyncBlocks(context.Background()); err != nil {
		t.Fatalf("seed sync: %v", err)
	}

	if groups, members := service.Stats(); groups != 2 || members != 3 {
		t.Fatalf("seed stats = (%d, %d), want (2, 3)", groups, members)
	}

	service.provider = &blockStubProvider{
		blockFiles: map[string][]BlockInfo{},
	}

	if err := service.SyncBlocks(context.Background()); err == nil {
		t.Fatal("expected empty refresh to fail")
	}

	if groups, members := service.Stats(); groups != 2 || members != 3 {
		t.Fatalf("stats after empty refresh = (%d, %d), want (2, 3)", groups, members)
	}

	reloaded, err := NewBlockService(store, &blockStubProvider{blockFiles: map[string][]BlockInfo{}}, BlockConfig{
		BaseDir:            baseDir,
		DisableAutoRefresh: true,
	})
	if err != nil {
		t.Fatalf("reload block service: %v", err)
	}
	defer reloaded.Close()

	if groups, members := reloaded.Stats(); groups != 2 || members != 3 {
		t.Fatalf("reloaded stats after empty refresh = (%d, %d), want (2, 3)", groups, members)
	}
}

type blockStubProvider struct {
	stubProvider
	blockFiles map[string][]BlockInfo
}

func (p *blockStubProvider) BlockGroups(ctx context.Context, filename string) ([]BlockInfo, error) {
	return p.blockFiles[filename], nil
}
