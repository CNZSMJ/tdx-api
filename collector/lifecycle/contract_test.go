package lifecycle

import "testing"

func TestExistingAPIContractsCoverAffectedEndpoints(t *testing.T) {
	contracts := ExistingAPIContracts()
	required := []string{
		"/api/trade-history",
		"/api/trade-history/full",
		"/api/order-history",
		"/api/minute-trade-all",
		"/api/kline-history",
		"/api/kline-all",
		"/api/kline-all/tdx",
		"/api/kline-all/ths",
		"/api/finance",
		"/api/f10/categories",
		"/api/f10/content",
		"/api/v1/prof-finance/fields",
		"/api/v1/prof-finance/history",
		"/api/v1/prof-finance/snapshot",
		"/api/v1/prof-finance/coverage",
		"/api/v1/prof-finance/cross-section",
	}

	byPath := make(map[string]APIContract, len(contracts))
	for _, contract := range contracts {
		byPath[contract.Path] = contract
		if contract.Method != "GET" {
			t.Fatalf("%s method = %s, want GET", contract.Path, contract.Method)
		}
		if contract.ResponseEnvelope == "" {
			t.Fatalf("%s missing response envelope", contract.Path)
		}
		if contract.SourceClass == "" {
			t.Fatalf("%s missing current source class", contract.Path)
		}
		if contract.ColdReadAllowed {
			t.Fatalf("%s must not implicitly read cold storage", contract.Path)
		}
	}
	for _, path := range required {
		contract, ok := byPath[path]
		if !ok {
			t.Fatalf("missing contract for %s", path)
		}
		if len(contract.Parameters) == 0 {
			t.Fatalf("%s missing parameter contract", path)
		}
	}
}

func TestExistingAPIContractsPreserveProviderBackedSources(t *testing.T) {
	contracts := ExistingAPIContracts()
	providerBacked := map[string]bool{
		"/api/trade-history":      true,
		"/api/trade-history/full": true,
		"/api/order-history":      true,
		"/api/minute-trade-all":   true,
		"/api/kline-history":      true,
		"/api/kline-all":          true,
		"/api/kline-all/tdx":      true,
		"/api/kline-all/ths":      true,
		"/api/finance":            true,
		"/api/f10/categories":     true,
		"/api/f10/content":        true,
	}
	for _, contract := range contracts {
		if !providerBacked[contract.Path] {
			continue
		}
		if contract.SourceClass != SourceProvider {
			t.Fatalf("%s source = %s, want %s", contract.Path, contract.SourceClass, SourceProvider)
		}
		if contract.OutOfHotRangeBehavior == "" {
			t.Fatalf("%s must document out-of-hot-range behavior", contract.Path)
		}
	}
}
