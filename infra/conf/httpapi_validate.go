package conf

import (
	"encoding/json"
	"strings"

	"github.com/xtls/xray-core/common/errors"
)

// ValidateConfigBytes checks that data is a complete, buildable Xray config.
func ValidateConfigBytes(data []byte) error {
	if len(data) == 0 {
		return errors.New("config body is empty")
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return errors.New("invalid config json").Base(err)
	}
	if err := PostProcessConfigureFile(&cfg); err != nil {
		return errors.New("config validation failed").Base(err)
	}
	if _, err := cfg.Build(); err != nil {
		return errors.New("config build failed").Base(err)
	}
	return nil
}

// ValidateInboundBatch validates all inbounds and rejects duplicate tags in one request.
func ValidateInboundBatch(raws []json.RawMessage) error {
	if len(raws) == 0 {
		return errors.New("inbounds array is required")
	}
	seen := make(map[string]struct{}, len(raws))
	for _, raw := range raws {
		if err := validateInboundRaw(raw); err != nil {
			return err
		}
		var c InboundDetourConfig
		_ = json.Unmarshal(raw, &c)
		if c.Tag == "" {
			return errors.New("inbound tag is required")
		}
		if _, dup := seen[c.Tag]; dup {
			return errors.New("duplicate inbound tag in request: ", c.Tag)
		}
		seen[c.Tag] = struct{}{}
	}
	return nil
}

// ValidateInboundBatchMeta validates inbound patches for preserve_clients edits.
// Clients/users in the request are ignored; Build is checked with empty clients.
func ValidateInboundBatchMeta(raws []json.RawMessage) error {
	if len(raws) == 0 {
		return errors.New("inbounds array is required")
	}
	seen := make(map[string]struct{}, len(raws))
	for _, raw := range raws {
		var m map[string]interface{}
		if err := json.Unmarshal(raw, &m); err != nil {
			return errors.New("invalid inbound json").Base(err)
		}
		tag, _ := m["tag"].(string)
		if tag == "" {
			return errors.New("inbound tag is required")
		}
		if _, dup := seen[tag]; dup {
			return errors.New("duplicate inbound tag in request: ", tag)
		}
		seen[tag] = struct{}{}
		if err := validateInboundMapMeta(m); err != nil {
			return errors.New("inbound validation failed for ", tag).Base(err)
		}
	}
	return nil
}

// CountSettingsClients counts clients (or users) inside inbound settings JSON.
func CountSettingsClients(settings *json.RawMessage) (int, error) {
	if settings == nil || len(*settings) == 0 {
		return 0, nil
	}
	var sm map[string]interface{}
	if err := json.Unmarshal(*settings, &sm); err != nil {
		return 0, errors.New("invalid settings json").Base(err)
	}
	if clients, ok := sm["clients"].([]interface{}); ok {
		return len(clients), nil
	}
	if users, ok := sm["users"].([]interface{}); ok {
		return len(users), nil
	}
	return 0, nil
}

// ValidateOutboundBatch validates all outbounds and rejects duplicate tags in one request.
func ValidateOutboundBatch(raws []json.RawMessage) error {
	if len(raws) == 0 {
		return errors.New("outbounds array is required")
	}
	seen := make(map[string]struct{}, len(raws))
	for _, raw := range raws {
		if err := validateOutboundRaw(raw); err != nil {
			return err
		}
		var c OutboundDetourConfig
		_ = json.Unmarshal(raw, &c)
		if c.Tag != "" {
			if _, dup := seen[c.Tag]; dup {
				return errors.New("duplicate outbound tag in request: ", c.Tag)
			}
			seen[c.Tag] = struct{}{}
		}
	}
	return nil
}

// ValidateRoutingRules validates a routing block (must contain rules).
func ValidateRoutingRules(routing json.RawMessage) error {
	if len(routing) == 0 {
		return errors.New("routing is required")
	}
	var incoming RouterConfig
	if err := json.Unmarshal(routing, &incoming); err != nil {
		return errors.New("invalid routing json").Base(err)
	}
	if len(incoming.RuleList) == 0 {
		return errors.New("routing.rules is required")
	}
	if _, err := incoming.Build(); err != nil {
		return errors.New("routing validation failed").Base(err)
	}
	return nil
}

// ValidateInboundUserSettings validates settings.clients for an existing inbound protocol.
func ValidateInboundUserSettings(protocol string, settings *json.RawMessage) error {
	if protocol == "" {
		return errors.New("protocol is required")
	}
	if settings == nil || len(*settings) == 0 {
		return errors.New("settings is required")
	}
	var sm map[string]interface{}
	if err := json.Unmarshal(*settings, &sm); err != nil {
		return errors.New("invalid settings json").Base(err)
	}
	clients, ok := sm["clients"].([]interface{})
	if !ok || len(clients) == 0 {
		return errors.New("settings.clients is required and must be non-empty")
	}
	emails := make(map[string]struct{}, len(clients))
	for i, cl := range clients {
		cm, ok := cl.(map[string]interface{})
		if !ok {
			return errors.New("settings.clients[", i, "] must be an object")
		}
		email, _ := cm["email"].(string)
		email = strings.TrimSpace(email)
		if email == "" {
			return errors.New("settings.clients[", i, "].email is required")
		}
		if _, dup := emails[email]; dup {
			return errors.New("duplicate client email in request: ", email)
		}
		emails[email] = struct{}{}
	}
	inb := &InboundDetourConfig{Protocol: protocol, Settings: settings}
	if _, err := inb.BuildProxySettingsOnly(); err != nil {
		return errors.New("invalid inbound user settings for protocol ", protocol).Base(err)
	}
	return nil
}

// ValidateNonEmptyTags ensures at least one non-empty tag string.
func ValidateNonEmptyTags(field string, tags []string) error {
	if len(tags) == 0 {
		return errors.New(field, " is required")
	}
	for i, tag := range tags {
		if strings.TrimSpace(tag) == "" {
			return errors.New(field, "[", i, "] must not be empty")
		}
	}
	return nil
}

// ValidateNonEmptyEmails ensures at least one non-empty email.
func ValidateNonEmptyEmails(emails []string) error {
	if len(emails) == 0 {
		return errors.New("emails is required")
	}
	for i, email := range emails {
		if strings.TrimSpace(email) == "" {
			return errors.New("emails[", i, "] must not be empty")
		}
	}
	return nil
}

func validateConfigRoot(root map[string]interface{}) error {
	data, err := json.Marshal(root)
	if err != nil {
		return errors.New("marshal config").Base(err)
	}
	return ValidateConfigBytes(data)
}

func inboundProtocolFromRoot(root map[string]interface{}, tag string) (string, error) {
	arr, ok := root["inbounds"].([]interface{})
	if !ok {
		return "", errors.New("inbound ", tag, " not found in config")
	}
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		itemTag, _ := m["tag"].(string)
		if itemTag != tag {
			continue
		}
		protocol, _ := m["protocol"].(string)
		if protocol == "" {
			return "", errors.New("inbound ", tag, " has no protocol in config")
		}
		return protocol, nil
	}
	return "", errors.New("inbound ", tag, " not found in config")
}
