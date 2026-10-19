package nexovcc

import "testing"

func TestTemplatePriorityCompatibilityAndValidation(t *testing.T) {
	s := Defaults()
	s.Templates = []Template{{ID: "t", Name: "Test", ProductID: "267", Amount: "10.00"}}
	s, err := Normalize(s)
	if err != nil || len(s.Templates[0].ProductIDs) != 1 || s.Templates[0].ProductIDs[0] != "267" || s.BatchIntervalSeconds != 3 || s.AutoWithdrawEnabled {
		t.Fatal(s, err)
	}
	s.Templates[0].ProductIDs = []string{"999", "268", "267"}
	s, err = Normalize(s)
	if err != nil || s.Templates[0].ProductID != "999" {
		t.Fatal(s, err)
	}
	products := []Product{{ID: "267", Minimum: "1.00"}, {ID: "268", Minimum: "5.00"}}
	p, err := s.Templates[0].SelectProduct(products)
	if err != nil || p.ID != "268" {
		t.Fatal("priority must follow saved order, skipping delisted", p, err)
	}
	products[1].Minimum = "20.00"
	p, err = s.Templates[0].SelectProduct(products)
	if err != nil || p.ID != "267" {
		t.Fatal("skip unaffordable minimum", p, err)
	}
	if _, err = s.Templates[0].SelectProduct(nil); err == nil {
		t.Fatal("all products offline accepted")
	}
	for _, ids := range [][]string{{}, {"267", "0267"}, {"invalid"}} {
		s.Templates[0].ProductIDs = ids
		if _, err = Normalize(s); err == nil {
			t.Fatal("invalid selection accepted", ids)
		}
	}
	s.Templates = nil
	for _, gap := range []int{-1, 301} {
		s.BatchIntervalSeconds = gap
		if _, err = Normalize(s); err == nil {
			t.Fatal("invalid gap accepted", gap)
		}
	}
}
