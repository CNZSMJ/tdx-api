package collector

import "testing"

func TestBlockServiceKeepsSourceScopedProviderKeys(t *testing.T) {
	service := &BlockService{}
	groups := []BlockGroupRecord{
		{
			Name:       "机器人",
			BlockType:  string(BlockTypeConcept),
			Source:     "block_gn.dat",
			StockCount: 1,
		},
		{
			Name:       "机器人",
			BlockType:  string(BlockTypeConcept),
			Source:     "block_fg.dat",
			StockCount: 1,
		},
	}
	members := []BlockMemberRecord{
		{
			Source:    "block_gn.dat",
			BlockName: "机器人",
			BlockType: string(BlockTypeConcept),
			Code:      "sz300024",
		},
		{
			Source:    "block_fg.dat",
			BlockName: "机器人",
			BlockType: string(BlockTypeConcept),
			Code:      "sh600000",
		},
	}

	service.rebuildCache(groups, members)

	gnMembers := service.GetBlockMembers("block_gn.dat", string(BlockTypeConcept), "机器人")
	if len(gnMembers) != 1 || gnMembers[0] != "sz300024" {
		t.Fatalf("block_gn.dat members = %#v, want [sz300024]", gnMembers)
	}

	fgMembers := service.GetBlockMembers("block_fg.dat", string(BlockTypeConcept), "机器人")
	if len(fgMembers) != 1 || fgMembers[0] != "sh600000" {
		t.Fatalf("block_fg.dat members = %#v, want [sh600000]", fgMembers)
	}

	stockBlocks := service.GetStockBlocks("sz300024")
	if len(stockBlocks) != 1 {
		t.Fatalf("stock block count = %d, want 1", len(stockBlocks))
	}
	if stockBlocks[0].Source != "block_gn.dat" {
		t.Fatalf("stock block source = %s, want block_gn.dat", stockBlocks[0].Source)
	}
}
