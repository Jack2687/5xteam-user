package store

import (
	"testing"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccCardSnapshotRollsBackAndPreservesOtherCards(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	old := time.Now().Add(-time.Hour)
	card, err := s.SaveNexovccCard(nexovcc.Card{Serial: "one", Status: "1", Balance: "1.00", BalanceAt: old}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveNexovccCard(nexovcc.Card{Serial: "other", Status: "1", Balance: "20.00", BalanceAt: old}, nil); err != nil {
		t.Fatal(err)
	}
	f := nexovcc.Finance{At: old, Funds: []nexovcc.Fund{{ID: "other-fund", Serial: "other", Kind: 3001, Amount: "-20.00", Currency: "USD", At: old}}}
	if err = s.SaveNexovccFinance(f); err != nil {
		t.Fatal(err)
	}
	card.Balance, card.BalanceAt = "10.00", time.Now()
	fresh := nexovcc.Finance{At: time.Now()}
	// A local failure after saving the new balance must roll everything back.
	if _, err = s.db.Exec(`CREATE TRIGGER reject_finance BEFORE UPDATE ON nexovcc_cache WHEN NEW.kind='finance' BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveNexovccCardSnapshot(card, fresh); err == nil {
		t.Fatal("commit should fail")
	}
	cards, finance, _, _, err := s.NexovccFinancialSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cards {
		if c.Serial == card.Serial && (c.Balance != "1.00" || !c.BalanceAt.Equal(old)) {
			t.Fatal("balance escaped rollback", c)
		}
	}
	if !finance.At.Equal(old) {
		t.Fatal("finance escaped rollback")
	}
	if _, err = s.db.Exec(`DROP TRIGGER reject_finance`); err != nil {
		t.Fatal(err)
	}
	// Local classification changed after the request started; preserve it.
	if _, err = s.SetNexovccCardNormalPending(card.ID, true); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveNexovccCardSnapshot(card, fresh); err != nil {
		t.Fatal(err)
	}
	cards, finance, _, _, err = s.NexovccFinancialSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cards {
		if c.Serial == card.Serial && (c.Balance != "10.00" || !c.NormalPending) {
			t.Fatal("metadata lost", c)
		}
	}
	if !finance.CardSyncedAt("other").Equal(old) || !finance.CardSyncedAt("one").Equal(fresh.At) || len(finance.Funds) != 1 || finance.Funds[0].ID != "other-fund" {
		t.Fatal("another card snapshot changed", finance)
	}
}
