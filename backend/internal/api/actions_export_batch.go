package api

import (
	"fmt"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/addressnorm"
)

// Both input forms feed the same export pipeline and durable tenant-scoped
// intent. A batch is one combined export, with one entry per address/dataset.
func normalizeActionExportAddresses(chain, single string, addresses []string) ([]string, error) {
	single = strings.TrimSpace(single)
	if single != "" && len(addresses) != 0 {
		return nil, fmt.Errorf("provide address or addresses, not both")
	}
	if len(addresses) > 100 {
		return nil, fmt.Errorf("addresses must contain 1 to 100 items")
	}
	if single != "" {
		addresses = []string{single}
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("address or addresses is required")
	}
	return addressnorm.NormalizeList(chain, addresses)
}
