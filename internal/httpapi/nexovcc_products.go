package httpapi

import (
	"context"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

func (s *Server) syncNexovccProductsLocked(ctx context.Context) ([]nexovcc.Product, error) {
	c, _, err := s.nexovccClientLocked()
	if err != nil {
		return nil, err
	}
	rows, err := c.Pages(ctx, "/api/product/findUserProductList")
	if err != nil {
		return nil, err
	}
	products := []nexovcc.Product{}
	for _, row := range rows {
		if !nexovcc.ActiveProduct(row) {
			continue
		}
		product, err := nexovcc.ProductFrom(row)
		if err != nil {
			return nil, err
		}
		products = append(products, product)
	}
	if err = s.store.NexovccCache("products", products); err != nil {
		return nil, err
	}
	return products, s.store.NexovccCache("products_at", time.Now())
}

func (s *Server) syncNexovccProductsIfDueLocked(ctx context.Context) error {
	var at time.Time
	if err := s.store.NexovccCached("products_at", &at); err != nil {
		return err
	}
	if !at.IsZero() && time.Since(at) < 30*time.Minute {
		return nil
	}
	_, err := s.syncNexovccProductsLocked(ctx)
	return err
}
