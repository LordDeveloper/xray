package apiserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/userconn"
)

const maxBulkUsersPerRequest = 200

type userOpError struct {
	Email   string `json:"email"`
	Message string `json:"message"`
}

type bulkUserResult struct {
	Succeeded int           `json:"succeeded"`
	Failed    int           `json:"failed"`
	Errors    []userOpError `json:"errors,omitempty"`
	// Backward-compatible aliases
	AddedUsers   int `json:"added_users,omitempty"`
	UpdatedUsers int `json:"updated_users,omitempty"`
	RemovedUsers int `json:"removed_users,omitempty"`
	DroppedConns int `json:"dropped_connections,omitempty"`
}

func parseOptionalBool(v *bool, defaultVal bool) bool {
	if v == nil {
		return defaultVal
	}
	return *v
}

func logMutation(ctx context.Context, op, tag string, clientsCount int, preserve *bool, duration time.Duration, succeeded, failed int) {
	preserveStr := "n/a"
	if preserve != nil {
		if *preserve {
			preserveStr = "true"
		} else {
			preserveStr = "false"
		}
	}
	errors.LogInfo(ctx, "httpapi mutation op=", op,
		" tag=", tag,
		" clients_count=", clientsCount,
		" preserve_clients=", preserveStr,
		" duration_ms=", duration.Milliseconds(),
		" succeeded=", succeeded,
		" failed=", failed)
}

func countRequestClients(inbounds []struct {
	Tag      string           `json:"tag"`
	Settings *json.RawMessage `json:"settings"`
}) (int, error) {
	total := 0
	for _, inb := range inbounds {
		var n int
		var err error
		if ConfigBridge.CountSettingsClients != nil {
			n, err = ConfigBridge.CountSettingsClients(inb.Settings)
		} else if inb.Settings != nil {
			var sm map[string]interface{}
			if err = json.Unmarshal(*inb.Settings, &sm); err == nil {
				if clients, ok := sm["clients"].([]interface{}); ok {
					n = len(clients)
				}
			}
		}
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

func writeBulkTooLarge(w http.ResponseWriter, count int) {
	writeJSON(w, http.StatusRequestEntityTooLarge, APIErrorResponse{
		Error:   "too many clients in one request",
		Code:    "payload_too_large",
		Details: []string{"max " + strconv.Itoa(maxBulkUsersPerRequest) + " clients per request, got " + strconv.Itoa(count)},
	})
}

func isAlreadyExistsUserErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "already exists")
}

func (s *Server) applyUserBatch(ctx context.Context, tag, protocol string, settings *json.RawMessage, mode string, atomic bool) bulkUserResult {
	res := bulkUserResult{}
	built, err := ConfigBridge.BuildInboundProxyOnly(tag, protocol, settings)
	if err != nil {
		res.Failed = 1
		res.Errors = append(res.Errors, userOpError{Email: "", Message: "build settings: " + err.Error()})
		return res
	}
	users := extractInboundUsers(built)
	appliedEmails := make([]string, 0, len(users))
	for _, user := range users {
		if user == nil || user.Email == "" {
			res.Failed++
			res.Errors = append(res.Errors, userOpError{Email: "", Message: "user email is required"})
			if atomic {
				s.rollbackUserBatch(ctx, tag, mode, appliedEmails)
				return res
			}
			continue
		}
		var opErr error
		switch mode {
		case "add":
			opErr = s.protoAlterAddUser(ctx, tag, user)
		case "edit":
			opErr = s.protoReplaceUser(ctx, tag, user)
		case "upsert":
			opErr = s.protoAlterAddUser(ctx, tag, user)
			if isAlreadyExistsUserErr(opErr) {
				opErr = s.protoReplaceUser(ctx, tag, user)
			}
		default:
			opErr = errors.New("unknown user batch mode")
		}
		if opErr != nil {
			res.Failed++
			res.Errors = append(res.Errors, userOpError{Email: user.Email, Message: opErr.Error()})
			if atomic {
				s.rollbackUserBatch(ctx, tag, mode, appliedEmails)
				return res
			}
			continue
		}
		res.Succeeded++
		appliedEmails = append(appliedEmails, user.Email)
	}
	switch mode {
	case "add":
		res.AddedUsers = res.Succeeded
	case "edit":
		res.UpdatedUsers = res.Succeeded
	case "upsert":
		res.AddedUsers = res.Succeeded
	}
	return res
}

func (s *Server) rollbackUserBatch(ctx context.Context, tag, mode string, emails []string) {
	if mode != "add" && mode != "upsert" {
		return
	}
	for _, email := range emails {
		_ = s.protoAlterRemoveUser(ctx, tag, email)
	}
}

func (s *Server) removeUsersPartial(ctx context.Context, tag string, emails []string, atomic bool) bulkUserResult {
	res := bulkUserResult{}
	for _, email := range emails {
		if email == "" {
			res.Failed++
			res.Errors = append(res.Errors, userOpError{Email: "", Message: "email is required"})
			if atomic {
				return res
			}
			continue
		}
		dropped := userconn.Kick(tag, email)
		if err := s.protoAlterRemoveUser(ctx, tag, email); err != nil {
			res.Failed++
			res.Errors = append(res.Errors, userOpError{Email: email, Message: err.Error()})
			if atomic {
				return res
			}
			continue
		}
		res.Succeeded++
		res.DroppedConns += dropped
	}
	res.RemovedUsers = res.Succeeded
	return res
}
