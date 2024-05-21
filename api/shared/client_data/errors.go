package client_data

import (
	"errors"
	"fmt"
)

// ErrNoRecords is returned by find operations when there are no results.
var ErrNoRecords = errors.New("client_data.Service: no records in result set")

// ErrConflict is returned by create / update operations when there is a
// conflict with an existing resource in the database.
func NewErrConflict(err error) ErrConflict {
	return ErrConflict{
		Err: err,
	}
}

type ErrConflict struct {
	Err error
}

func (e ErrConflict) Error() string {
	return "client_data.Service: Conflict with existing resource"
}

func (e ErrConflict) Unwrap() error { return e.Err }

// ErrBadRequest is returned by all operations when there is an improperly
// formatted request to our Edge Client Service.
func NewErrBadRequest(err error, paramIssues []string) ErrBadRequest {
	return ErrBadRequest{
		Err:         err,
		paramIssues: paramIssues,
	}
}

type ErrBadRequest struct {
	Err         error
	paramIssues []string
}

func (e ErrBadRequest) Error() string {
	return fmt.Sprintf("client_data.Service: Bad request: %s [%v]", e.Err.Error(), e.paramIssues)
}

func (e ErrBadRequest) Unwrap() error { return e.Err }
