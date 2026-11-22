package hermes

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// APIError is a non-2xx REST response. Hermes answers with FastAPI's
// {"detail": …}, the auth gate's {"error","detail","reason"}, or the
// mutation fence's {"code"/"reason"} (docs §2.2, §5.4).
type APIError struct {
	Status int
	Code   string // "session_expired", "revision_conflict", …
	Detail string
}

func (e *APIError) Error() string {
	msg := e.Detail
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	if e.Code != "" {
		return fmt.Sprintf("hermes: %d %s: %s", e.Status, e.Code, msg)
	}
	return fmt.Sprintf("hermes: %d: %s", e.Status, msg)
}

// ErrUnauthorized matches a 401 from REST or a 4401 close of the socket.
var ErrUnauthorized = errors.New("hermes: unauthorized")

// ErrForbidden matches a 403, or a 4403 close of the socket (a Host or
// Origin mismatch, or chat disabled).
var ErrForbidden = errors.New("hermes: forbidden")

// ErrNotFound matches a 404.
var ErrNotFound = errors.New("hermes: not found")

func (e *APIError) Is(target error) bool {
	switch target {
	case ErrUnauthorized:
		return e.Status == http.StatusUnauthorized
	case ErrForbidden:
		return e.Status == http.StatusForbidden
	case ErrNotFound:
		return e.Status == http.StatusNotFound
	}
	return false
}

// Retryable reports whether the request may succeed if sent again: a
// fence refusal that needs a new snapshot, a rate limit, or the server
// draining.
func (e *APIError) Retryable() bool {
	switch e.Code {
	case "revision_conflict", "stale_generation", "session_busy", "runtime_draining", "storage_unavailable":
		return true
	}
	return e.Status == http.StatusTooManyRequests || e.Status == http.StatusServiceUnavailable
}

func parseAPIError(status int, body []byte) *APIError {
	e := &APIError{Status: status}
	var v struct {
		Error  json.RawMessage `json:"error"`
		Code   string          `json:"code"`
		Reason string          `json:"reason"`
		Detail json.RawMessage `json:"detail"`
	}
	if json.Unmarshal(body, &v) != nil {
		if len(body) > 0 && len(body) < 300 {
			e.Detail = string(body)
		}
		return e
	}
	// "error" is a code string on the dashboard, an object on the API server.
	var code string
	if json.Unmarshal(v.Error, &code) == nil {
		e.Code = code
	} else {
		var obj struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		}
		if json.Unmarshal(v.Error, &obj) == nil {
			e.Code, e.Detail = obj.Code, obj.Message
		}
	}
	if e.Code == "" {
		e.Code = v.Code
	}
	if e.Code == "" && v.Reason != "" && status != http.StatusUnauthorized {
		e.Code = v.Reason
	}
	if len(v.Detail) > 0 {
		var s string
		if json.Unmarshal(v.Detail, &s) == nil {
			e.Detail = s
			// The mutation fence sends its reason as the detail itself:
			// {"detail":"revision_conflict"} (web_server_sessions.py).
			if e.Code == "" && isCodeLike(s) {
				e.Code = s
			}
		} else {
			// Structured detail: {"code": …, "message": …} or {"reason": …}.
			var d struct {
				Code    string `json:"code"`
				Reason  string `json:"reason"`
				Message string `json:"message"`
			}
			if json.Unmarshal(v.Detail, &d) == nil {
				if e.Code == "" {
					e.Code = d.Code
				}
				if e.Code == "" {
					e.Code = d.Reason
				}
				e.Detail = d.Message
			}
		}
	}
	return e
}

// isCodeLike reports whether s looks like a machine code: lower_snake_case.
func isCodeLike(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r == '_' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
