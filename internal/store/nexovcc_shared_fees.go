package store

import (
	"strings"

	"chatgpt-space-merge/internal/nexovcc"
)

type NexovccSharedFees struct {
	Count        int    `json:"count"`
	Ready        bool   `json:"ready"`
	Applied      bool   `json:"applied"`
	Opening      string `json:"opening"`
	Monthly      string `json:"monthly"`
	OpeningTotal string `json:"opening_total"`
	MonthlyTotal string `json:"monthly_total"`
}

// Use all successful accounts, including deleted accounts, independently of UI
// filters/pagination. A repeated successful order for the same account receives
// its card share only once, consistently with the card's opened-account count.
func allocateNexovccCardFees(q nexovccCaseQuery, cases []NexovccCase, finance nexovcc.Finance) error {
	serials := map[string]string{}
	rows, err := q.Query(`SELECT id,serial FROM nexovcc_cards`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, serial string
		if err = rows.Scan(&id, &serial); err != nil {
			rows.Close()
			return err
		}
		serials[id] = serial
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	type cardFees struct {
		opening, monthly int64
		invalid          bool
	}
	totals := map[string]cardFees{}
	for _, f := range finance.Funds {
		if !nexovcc.OpeningFee(f.Kind) && f.Kind != 3004 {
			continue
		}
		v := totals[f.Serial]
		n, e := nexovcc.SignedCents(f.Amount)
		if e != nil || f.Currency != "USD" {
			v.invalid = true
		} else if f.Kind == 3004 {
			v.monthly -= n
		} else {
			v.opening -= n
		}
		totals[f.Serial] = v
	}
	accounts := map[string]map[string]bool{}
	for _, c := range cases {
		if c.PaymentStatus == "success" {
			if accounts[c.CardID] == nil {
				accounts[c.CardID] = map[string]bool{}
			}
			accounts[c.CardID][strings.ToLower(c.Email)] = true
		}
	}
	assigned := map[string]bool{}
	for i := len(cases) - 1; i >= 0; i-- {
		c := &cases[i]
		if c.PaymentStatus != "success" {
			continue
		}
		serial := serials[c.CardID]
		v := totals[serial]
		a := &NexovccSharedFees{Count: len(accounts[c.CardID]), Ready: serial != "" && !finance.CardSyncedAt(serial).IsZero() && !v.invalid}
		key := c.CardID + "|" + strings.ToLower(c.Email)
		a.Applied = !assigned[key]
		assigned[key] = true
		if a.Ready && a.Count > 0 {
			a.OpeningTotal, a.MonthlyTotal = nexovcc.SignedMoney(v.opening), nexovcc.SignedMoney(v.monthly)
			a.Opening, a.Monthly = "0.00", "0.00"
			if a.Applied {
				// Display cents truncated as requested (0.50 / 3 = 0.16).
				// Card/global totals still use the exact original ledger, not shares.
				a.Opening = nexovcc.SignedMoney(v.opening / int64(a.Count))
				a.Monthly = nexovcc.SignedMoney(v.monthly / int64(a.Count))
			}
		}
		c.SharedFees = a
	}
	return nil
}
