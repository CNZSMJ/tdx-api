package collector

import "github.com/injoyai/tdx/protocol"

// assetTypeFromCode classifies full codes (e.g. sh600000) using TDX protocol rules.
// Lives in *_tdx.go so collector core (ticker.go) stays free of direct protocol imports.
func assetTypeFromCode(code string) AssetType {
	switch {
	case protocol.IsStock(code):
		return AssetTypeStock
	case protocol.IsETF(code):
		return AssetTypeETF
	case protocol.IsIndex(code):
		return AssetTypeIndex
	default:
		return AssetTypeUnknown
	}
}
