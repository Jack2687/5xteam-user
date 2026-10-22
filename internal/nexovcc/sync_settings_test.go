package nexovcc

import "testing"

func TestSplitSyncSettingsMigrationAndValidation(t *testing.T) {
	cfg := Defaults()
	cfg.PageRefreshSeconds = 300
	got, err := Normalize(cfg)
	if err != nil || got.CardSyncSeconds != 300 || got.BalanceSyncSeconds != 300 || got.TransactionSyncSeconds != 300 || got.AccountSyncSeconds != 1800 || got.ProductSyncSeconds != 1800 {
		t.Fatal(got, err)
	}
	for _, v := range []int{-1, 59, 60, 86400, 86401} {
		for _, field := range []string{"balance", "cards", "transactions", "account", "products"} {
			c := Defaults()
			switch field {
			case "balance":
				c.BalanceSyncSeconds = v
			case "cards":
				c.CardSyncSeconds = v
			case "transactions":
				c.TransactionSyncSeconds = v
			case "account":
				c.AccountSyncSeconds = v
			case "products":
				c.ProductSyncSeconds = v
			}
			if _, err := Normalize(c); (err == nil) != (v >= 60 && v <= 86400) {
				t.Fatal(field, v, err)
			}
		}
	}
}
