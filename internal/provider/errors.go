package provider

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/arthur-sommer-etc/cliproxyapi-copilot-plugin/internal/translate"
)

func requestTranslationError(err error) error {
	code := "translation_error"
	if errors.Is(err, translate.ErrAttachmentConversion) {
		code = "unsupported_attachment"
	} else if errors.Is(err, translate.ErrProviderStateConversion) {
		code = "unsupported_provider_state"
	}
	return statusError(code, err.Error(), http.StatusUnprocessableEntity)
}

type StatusError struct {
	Code       string
	Message    string
	HTTPStatus int
	Retryable  bool
}

func (e *StatusError) Error() string {
	var message string
	if e != nil {
		message = e.Message
	}
	return message
}

func statusError(code, message string, status int) error {
	if status == 0 {
		status = http.StatusInternalServerError
	}
	return &StatusError{Code: code, Message: message, HTTPStatus: status}
}

func upstreamStatusError(status int, detail string) error {
	message := fmt.Sprintf("Copilot upstream returned HTTP %d", status)
	if detail != "" {
		message += ": " + detail
	}
	return &StatusError{
		Code:       "upstream_error",
		Message:    message,
		HTTPStatus: status,
		Retryable:  status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500,
	}
}
