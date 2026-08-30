package apperr

import (
	"encoding/json"
	"errors"
)

type Code string

const (
	InvalidArgument Code = "INVALID_ARGUMENT"
	NotFound        Code = "NOT_FOUND"
	StaleLookup     Code = "STALE_LOOKUP"
	UpstreamError   Code = "UPSTREAM_ERROR"
	Unauthorized    Code = "UNAUTHORIZED"
	InternalError   Code = "INTERNAL_ERROR"
)

type Error struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
	cause   error
}

func New(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

func Wrap(code Code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, cause: cause}
}

func (err *Error) Error() string {
	encoded, marshalErr := json.Marshal(struct {
		Code    Code   `json:"code"`
		Message string `json:"message"`
	}{Code: err.Code, Message: err.Message})
	if marshalErr != nil {
		return `{"code":"INTERNAL_ERROR","message":"failed to encode application error"}`
	}
	return string(encoded)
}

func (err *Error) Unwrap() error {
	return err.cause
}

func From(err error) *Error {
	if err == nil {
		return nil
	}
	var applicationError *Error
	if errors.As(err, &applicationError) {
		return applicationError
	}
	return Wrap(InternalError, "an unexpected internal error occurred", err)
}
