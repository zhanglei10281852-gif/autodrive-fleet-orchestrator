package common

import (
	"errors"
	"fmt"
)

var (
	ErrNotFound     = errors.New("resource not found")
	ErrConflict     = errors.New("resource conflict")
	ErrInvalid      = errors.New("invalid input")
	ErrUnauthorized = errors.New("authentication required")
	ErrForbidden    = errors.New("operation forbidden")
	ErrExpired      = errors.New("resource expired")
	ErrUnavailable  = errors.New("dependency unavailable")
)

type FieldError struct {
	Field   string
	Problem string
}

func (e FieldError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Problem)
}

func (e FieldError) Unwrap() error { return ErrInvalid }

type ConflictError struct {
	Resource string
	Reason   string
}

func (e ConflictError) Error() string {
	return fmt.Sprintf("%s conflict: %s", e.Resource, e.Reason)
}

func (e ConflictError) Unwrap() error { return ErrConflict }

type TransitionError struct {
	Entity string
	From   string
	To     string
	Reason string
}

func (e TransitionError) Error() string {
	return fmt.Sprintf("cannot transition %s from %s to %s: %s", e.Entity, e.From, e.To, e.Reason)
}

func (e TransitionError) Unwrap() error { return ErrConflict }
