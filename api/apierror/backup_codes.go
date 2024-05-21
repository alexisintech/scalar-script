package apierror

import (
	"net/http"
)

func BackupCodesNotAvailable() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Backup codes not available",
		longMessage:  "In order to use backup codes, you have to enable any other Multi-factor method",
		code:         BackupCodesNotAvailableCode,
	})
}
