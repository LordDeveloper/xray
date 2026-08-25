package apiserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// APIErrorResponse is the standard JSON error body for HTTP API clients.
type APIErrorResponse struct {
	Error          string   `json:"error"`
	Code           string   `json:"code,omitempty"`
	Details        []string `json:"details,omitempty"`
	RuntimeApplied *bool    `json:"runtime_applied,omitempty"`
}

func writeAPIErrorMsg(w http.ResponseWriter, status int, msg string) {
	writeAPIErrorStatus(w, status, errors.New(msg))
}

func withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				writeAPIErrorMsg(w, http.StatusInternalServerError, fmt.Sprintf("internal server error: %v", rec))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func writeAPIError(w http.ResponseWriter, err error) {
	writeAPIErrorStatus(w, httpStatusForError(err), err)
}

func writeAPIErrorStatus(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, formatAPIError(status, err, nil))
}

func writeDecodeError(w http.ResponseWriter, err error) {
	writeAPIErrorStatus(w, http.StatusBadRequest, err)
}

func writeValidationError(w http.ResponseWriter, err error) {
	writeAPIErrorStatus(w, http.StatusBadRequest, err)
}

func writeMutationSaveError(w http.ResponseWriter, err error) {
	applied := true
	status := http.StatusInternalServerError
	if isClientInputError(err) {
		status = http.StatusBadRequest
	}
	body := formatAPIError(status, err, &applied)
	body.Code = "config_save_failed"
	writeJSON(w, status, body)
}

func formatAPIError(status int, err error, runtimeApplied *bool) APIErrorResponse {
	if err == nil {
		return APIErrorResponse{
			Error:          http.StatusText(status),
			Code:           errorCode(status, nil),
			RuntimeApplied: runtimeApplied,
		}
	}
	if msg, code, ok := formatJSONError(err); ok {
		return APIErrorResponse{
			Error:          msg,
			Code:           code,
			RuntimeApplied: runtimeApplied,
		}
	}
	primary, details := splitErrorChain(err)
	return APIErrorResponse{
		Error:          primary,
		Code:           errorCode(status, err),
		Details:        details,
		RuntimeApplied: runtimeApplied,
	}
}

func formatJSONError(err error) (message, code string, ok bool) {
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return fmt.Sprintf("invalid JSON syntax near byte %d", syntax.Offset), "invalid_json", true
	}
	var unmarshal *json.UnmarshalTypeError
	if errors.As(err, &unmarshal) {
		if unmarshal.Field != "" {
			return fmt.Sprintf("invalid type for field %q: expected %s", unmarshal.Field, unmarshal.Type.String()), "invalid_json", true
		}
		return fmt.Sprintf("invalid JSON type at offset %d: expected %s", unmarshal.Offset, unmarshal.Type.String()), "invalid_json", true
	}
	if errors.Is(err, io.EOF) || strings.Contains(strings.ToLower(err.Error()), "unexpected end of json") {
		return "request body is empty or incomplete JSON", "invalid_json", true
	}
	return "", "", false
}

func splitErrorChain(err error) (primary string, details []string) {
	if err == nil {
		return "unknown error", nil
	}
	parts := strings.Split(err.Error(), " > ")
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	if len(out) == 0 {
		return err.Error(), nil
	}
	if len(out) == 1 {
		return out[0], nil
	}
	return out[0], out[1:]
}

func httpStatusForError(err error) int {
	if err == nil {
		return http.StatusInternalServerError
	}
	var syntax *json.SyntaxError
	var unmarshal *json.UnmarshalTypeError
	if errors.As(err, &syntax) || errors.As(err, &unmarshal) || errors.Is(err, io.EOF) {
		return http.StatusBadRequest
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "not found"):
		return http.StatusNotFound
	case strings.Contains(msg, "unauthorized"):
		return http.StatusUnauthorized
	case isClientInputError(err):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func isClientInputError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	keywords := []string{
		"is required",
		"invalid ",
		"validation failed",
		"build failed",
		"duplicate ",
		"not found in config",
		"must not be empty",
		"must be an object",
		"proxy is not a usermanager",
		"rule tag not found",
		"out of range",
		"unknown inbound",
		"no routing rules",
		"routing section not found",
		"config path not set",
		"pattern looks like a regex",
		"outbound and source_ips",
		"tag required",
		"method not allowed",
	}
	for _, k := range keywords {
		if strings.Contains(msg, k) {
			return true
		}
	}
	return false
}

func errorCode(status int, err error) string {
	if err != nil {
		if _, code, ok := formatJSONError(err); ok {
			return code
		}
		msg := strings.ToLower(err.Error())
		switch {
		case strings.Contains(msg, "validation failed"):
			return "validation_failed"
		case strings.Contains(msg, "build failed"):
			return "build_failed"
		case strings.Contains(msg, "duplicate "):
			return "duplicate"
		case strings.Contains(msg, "not found"):
			return "not_found"
		case strings.Contains(msg, "is required") || strings.Contains(msg, "must not be empty"):
			return "missing_field"
		case strings.Contains(msg, "invalid "):
			return "invalid_input"
		}
	}
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	default:
		return "internal_error"
	}
}
