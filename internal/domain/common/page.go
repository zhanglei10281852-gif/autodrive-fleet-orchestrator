package common

import "fmt"

type PageRequest struct {
	Limit  int
	Offset int
}

func (p PageRequest) Normalize() PageRequest {
	if p.Limit <= 0 {
		p.Limit = 50
	}
	if p.Limit > 200 {
		p.Limit = 200
	}
	if p.Offset < 0 {
		p.Offset = 0
	}
	return p
}

func (p PageRequest) Validate() error {
	if p.Limit < 0 || p.Offset < 0 {
		return fmt.Errorf("negative pagination: %w", ErrInvalid)
	}
	return nil
}

type Page[T any] struct {
	Items  []T `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

func NewPage[T any](items []T, total int, request PageRequest) Page[T] {
	normalized := request.Normalize()
	if items == nil {
		items = make([]T, 0)
	}
	return Page[T]{Items: items, Total: total, Limit: normalized.Limit, Offset: normalized.Offset}
}
