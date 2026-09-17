package application

import "fmt"

type Event struct {
	Name string
	Data any
}

type EventSink func(Event) error

type FaultCode string

const (
	FaultInvalid       FaultCode = "invalid"
	FaultTooLarge      FaultCode = "too_large"
	FaultNotFound      FaultCode = "not_found"
	FaultConflict      FaultCode = "conflict"
	FaultUnprocessable FaultCode = "unprocessable"
	FaultBadGateway    FaultCode = "bad_gateway"
	FaultInternal      FaultCode = "internal"
)

type Fault struct {
	Code   FaultCode
	Public string
	Cause  error
}

func (f *Fault) Error() string {
	if f == nil {
		return ""
	}
	if f.Cause != nil {
		return fmt.Sprintf("%s: %v", f.Public, f.Cause)
	}
	return f.Public
}

func (f *Fault) Unwrap() error {
	if f == nil {
		return nil
	}
	return f.Cause
}
