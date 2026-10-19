package httpapi

import (
	"context"

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
	return products, s.store.NexovccCache("products", products)
}
