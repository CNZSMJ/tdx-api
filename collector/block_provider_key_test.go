package collector

import "testing"

func TestBlockServiceKeepsSourceScopedProviderKeys(t *testing.T) {
	service := &BlockService{}
	groups := []BlockGroupRecord{
		{
			Name:       "白酒概念",
			BlockType:  string(BlockTypeConcept),
			Source:     "block.dat",
			StockCount: 1,
		},
		{
			Name:       "白酒概念",
			BlockType:  string(BlockTypeConcept),
			Source:     "block_gn.dat",
			StockCount: 1,
		},
	}
	members := []BlockMemberRecord{
		{
			Source:    "block.dat",
			BlockName: "白酒概念",
			BlockType: string(BlockTypeConcept),
			Code:      "sz300024",
		},
		{
			Source:    "block_gn.dat",
			BlockName: "白酒概念",
			BlockType: string(BlockTypeConcept),
			Code:      "sh600000",
		},
	}

	service.rebuildCache(groups, members)

	blockMembers := service.GetBlockMembers("block.dat", string(BlockTypeConcept), "白酒概念")
	if len(blockMembers) != 1 || blockMembers[0] != "sz300024" {
		t.Fatalf("block.dat members = %#v, want [sz300024]", blockMembers)
	}

	gnMembers := service.GetBlockMembers("block_gn.dat", string(BlockTypeConcept), "白酒概念")
	if len(gnMembers) != 1 || gnMembers[0] != "sh600000" {
		t.Fatalf("block_gn.dat members = %#v, want [sh600000]", gnMembers)
	}

	stockBlocks := service.GetStockBlocks("sz300024")
	if len(stockBlocks) != 1 {
		t.Fatalf("stock block count = %d, want 1", len(stockBlocks))
	}
	if stockBlocks[0].Source != "block.dat" {
		t.Fatalf("stock block source = %s, want block.dat", stockBlocks[0].Source)
	}
}

func TestBlockServiceNormalizesMemberCodesToFullCode(t *testing.T) {
	service := &BlockService{}
	service.rebuildCache(
		[]BlockGroupRecord{
			{Name: "沪深300", BlockType: string(BlockTypeIndexBlock), Source: "block.dat", StockCount: 1},
		},
		[]BlockMemberRecord{
			{Source: "block.dat", BlockName: "沪深300", BlockType: string(BlockTypeIndexBlock), Code: "000009"},
		},
	)

	members := service.GetBlockMembers("block.dat", string(BlockTypeIndexBlock), "沪深300")
	if len(members) != 1 || members[0] != "sz000009" {
		t.Fatalf("members = %#v, want [sz000009]", members)
	}

	blocks := service.GetStockBlocks("sz000009")
	if len(blocks) != 1 || blocks[0].Name != "沪深300" {
		t.Fatalf("stock blocks = %#v, want 沪深300", blocks)
	}
}

func TestClassifyBlockTypeUsesSourceAndNameHeuristics(t *testing.T) {
	tests := []struct {
		source string
		name   string
		want   BlockType
	}{
		{source: "block_gn.dat", name: "机器人", want: BlockTypeConcept},
		{source: "block_fg.dat", name: "券商金股", want: BlockTypeStyle},
		{source: "block_fg.dat", name: "中小银行", want: BlockTypeIndustry},
		{source: "block.dat", name: "融资融券", want: BlockTypeStyle},
		{source: "block.dat", name: "白酒概念", want: BlockTypeConcept},
		{source: "block.dat", name: "沪深300", want: BlockTypeIndexBlock},
		{source: "block_zs.dat", name: "科创50", want: BlockTypeIndexBlock},
	}

	for _, tc := range tests {
		if got := classifyBlockType(tc.source, tc.name); got != tc.want {
			t.Fatalf("classifyBlockType(%q, %q) = %q, want %q", tc.source, tc.name, got, tc.want)
		}
	}
}

func TestBlockServiceReclassifiesLegacyStoredBlockTypes(t *testing.T) {
	service := &BlockService{}
	service.rebuildCache(
		[]BlockGroupRecord{
			{Name: "沪深300", BlockType: string(BlockTypeIndexBlock), Source: "block.dat", StockCount: 1},
			{Name: "白酒概念", BlockType: string(BlockTypeIndexBlock), Source: "block_gn.dat", StockCount: 1},
			{Name: "融资融券", BlockType: string(BlockTypeIndexBlock), Source: "block_fg.dat", StockCount: 1},
		},
		[]BlockMemberRecord{
			{Source: "block.dat", BlockName: "沪深300", BlockType: string(BlockTypeIndexBlock), Code: "600519"},
			{Source: "block_gn.dat", BlockName: "白酒概念", BlockType: string(BlockTypeIndexBlock), Code: "600519"},
			{Source: "block_fg.dat", BlockName: "融资融券", BlockType: string(BlockTypeIndexBlock), Code: "600519"},
		},
	)

	blocks := service.GetStockBlocks("sh600519")
	if len(blocks) != 3 {
		t.Fatalf("stock block count = %d, want 3", len(blocks))
	}

	gotTypes := map[string]string{}
	for _, block := range blocks {
		gotTypes[block.Name] = block.BlockType
	}

	if gotTypes["沪深300"] != string(BlockTypeIndexBlock) {
		t.Fatalf("沪深300 block_type = %q, want %q", gotTypes["沪深300"], BlockTypeIndexBlock)
	}
	if gotTypes["白酒概念"] != string(BlockTypeConcept) {
		t.Fatalf("白酒概念 block_type = %q, want %q", gotTypes["白酒概念"], BlockTypeConcept)
	}
	if gotTypes["融资融券"] != string(BlockTypeStyle) {
		t.Fatalf("融资融券 block_type = %q, want %q", gotTypes["融资融券"], BlockTypeStyle)
	}
}
