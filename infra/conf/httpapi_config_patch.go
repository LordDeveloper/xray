package conf

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/xtls/xray-core/app/httpapi/apiserver"
	"github.com/xtls/xray-core/common/errors"
)

func init() {
	apiserver.ConfigBridge.PatchConfigInbounds = PatchConfigInbounds
	apiserver.ConfigBridge.PatchConfigInboundsPreserveClients = PatchConfigInboundsPreserveClients
	apiserver.ConfigBridge.MergeInboundPreserveClients = MergeInboundPreserveClients
	apiserver.ConfigBridge.PatchConfigOutbounds = PatchConfigOutbounds
	apiserver.ConfigBridge.PatchConfigInboundUsers = PatchConfigInboundUsers
	apiserver.ConfigBridge.PatchConfigInboundUsersRemove = PatchConfigInboundUsersRemove
	apiserver.ConfigBridge.ListConfigInbounds = ListConfigInbounds
	apiserver.ConfigBridge.ListConfigOutbounds = ListConfigOutbounds
	apiserver.ConfigBridge.ListConfigRules = ListConfigRules
	apiserver.ConfigBridge.ConfigClientsFromMemoryUsers = ConfigClientsFromMemoryUsers
	apiserver.ConfigBridge.OverlayInboundClients = OverlayInboundClients
	apiserver.ConfigBridge.PatchConfigRulesAdd = PatchConfigRulesAdd
	apiserver.ConfigBridge.PatchConfigRulesRemove = PatchConfigRulesRemove
	apiserver.ConfigBridge.PatchConfigRulesEdit = PatchConfigRulesEdit
	apiserver.ConfigBridge.PatchConfigRulesReplace = PatchConfigRulesReplace
	apiserver.ConfigBridge.ValidateConfigBytes = ValidateConfigBytes
	apiserver.ConfigBridge.ValidateInboundBatch = ValidateInboundBatch
	apiserver.ConfigBridge.ValidateInboundBatchMeta = ValidateInboundBatchMeta
	apiserver.ConfigBridge.ValidateOutboundBatch = ValidateOutboundBatch
	apiserver.ConfigBridge.ValidateRoutingRules = ValidateRoutingRules
	apiserver.ConfigBridge.ValidateInboundUserSettings = ValidateInboundUserSettings
	apiserver.ConfigBridge.ValidateNonEmptyTags = ValidateNonEmptyTags
	apiserver.ConfigBridge.ValidateNonEmptyEmails = ValidateNonEmptyEmails
	apiserver.ConfigBridge.CountSettingsClients = CountSettingsClients
}

func readConfigRoot(configPath string) (map[string]interface{}, error) {
	if configPath == "" {
		return nil, errors.New("config path not set")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, errors.New("read config file").Base(err)
	}
	root := map[string]interface{}{}
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, errors.New("parse config file").Base(err)
	}
	return root, nil
}

func validateInboundRaw(raw json.RawMessage) error {
	var c InboundDetourConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		return errors.New("invalid inbound json").Base(err)
	}
	if c.Tag == "" {
		return errors.New("inbound tag is required")
	}
	if err := validateInboundDetourFields(&c); err != nil {
		return errors.New("inbound validation failed for ", c.Tag).Base(err)
	}
	if _, err := c.Build(); err != nil {
		return errors.New("inbound validation failed for ", c.Tag).Base(err)
	}
	return nil
}

func validateInboundDetourFields(c *InboundDetourConfig) error {
	if c.Protocol == "" {
		return errors.New("protocol is required")
	}
	if c.ListenOn != nil && c.ListenOn.Family().IsDomain() && c.ListenOn.Domain() == "" {
		return errors.New("listen address must not be empty")
	}
	if c.ListenOn == nil && c.PortList == nil {
		return errors.New("port is required when listen is omitted")
	}
	if c.ListenOn != nil {
		isIP := c.ListenOn.Family().IsIP() || (c.ListenOn.Family().IsDomain() && c.ListenOn.Domain() == "localhost")
		if isIP && c.PortList == nil {
			return errors.New("port is required for listen ", c.ListenOn.Domain())
		}
	}
	return nil
}

func validateOutboundRaw(raw json.RawMessage) error {
	var c OutboundDetourConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		return errors.New("invalid outbound json").Base(err)
	}
	if _, err := c.Build(); err != nil {
		tag := c.Tag
		if tag == "" {
			tag = "(no tag)"
		}
		return errors.New("outbound validation failed for ", tag).Base(err)
	}
	return nil
}

// PatchConfigInbounds upserts validated inbound JSON objects into the config file.
func PatchConfigInbounds(configPath string, upsert []json.RawMessage, removeTags []string) error {
	for _, raw := range upsert {
		if err := validateInboundRaw(raw); err != nil {
			return err
		}
	}
	root, err := readConfigRoot(configPath)
	if err != nil {
		return err
	}
	if err := patchInboundsInRoot(root, upsert, removeTags); err != nil {
		return err
	}
	return validateAndWriteConfig(configPath, root)
}

// MergeInboundPreserveClients merges a patch onto the existing inbound in config.json,
// keeping settings.clients / settings.users untouched on disk.
// The returned JSON has clients stripped for a cheap Build; runtime injects live users.
func MergeInboundPreserveClients(configPath string, patch json.RawMessage) (json.RawMessage, int, error) {
	var patchMap map[string]interface{}
	if err := json.Unmarshal(patch, &patchMap); err != nil {
		return nil, 0, errors.New("invalid inbound json").Base(err)
	}
	tag, _ := patchMap["tag"].(string)
	if tag == "" {
		return nil, 0, errors.New("inbound tag is required")
	}
	root, err := readConfigRoot(configPath)
	if err != nil {
		return nil, 0, err
	}
	existing, clientsCount, err := findInboundMapInRoot(root, tag)
	if err != nil {
		return nil, 0, err
	}
	merged := mergeInboundMapsPreserveClients(existing, patchMap)
	if err := validateInboundMapMeta(merged); err != nil {
		return nil, 0, err
	}
	buildRaw, err := stripInboundClientsForBuildMap(merged)
	if err != nil {
		return nil, 0, err
	}
	return buildRaw, clientsCount, nil
}

// PatchConfigInboundsPreserveClients updates inbound meta fields in config.json without rewriting clients.
// Returns per-inbound clients_count after patch (same order as patches).
func PatchConfigInboundsPreserveClients(configPath string, patches []json.RawMessage) ([]int, error) {
	if len(patches) == 0 {
		return nil, errors.New("inbounds array is required")
	}
	root, err := readConfigRoot(configPath)
	if err != nil {
		return nil, err
	}
	counts := make([]int, 0, len(patches))
	for _, raw := range patches {
		var patchMap map[string]interface{}
		if err := json.Unmarshal(raw, &patchMap); err != nil {
			return nil, errors.New("invalid inbound json").Base(err)
		}
		tag, _ := patchMap["tag"].(string)
		if tag == "" {
			return nil, errors.New("inbound tag is required")
		}
		arr, ok := root["inbounds"].([]interface{})
		if !ok {
			return nil, errors.New("inbound ", tag, " not found in config")
		}
		found := false
		for i, item := range arr {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			itemTag, _ := m["tag"].(string)
			if itemTag != tag {
				continue
			}
			merged := mergeInboundMapsPreserveClients(m, patchMap)
			if err := validateInboundMapMeta(merged); err != nil {
				return nil, err
			}
			arr[i] = merged
			counts = append(counts, countClientsInInboundMap(merged))
			found = true
			break
		}
		if !found {
			return nil, errors.New("inbound ", tag, " not found in config")
		}
		root["inbounds"] = arr
	}
	if err := validateAndWriteConfig(configPath, root); err != nil {
		return nil, err
	}
	return counts, nil
}

func findInboundMapInRoot(root map[string]interface{}, tag string) (map[string]interface{}, int, error) {
	arr, ok := root["inbounds"].([]interface{})
	if !ok {
		return nil, 0, errors.New("inbound ", tag, " not found in config")
	}
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		itemTag, _ := m["tag"].(string)
		if itemTag == tag {
			return m, countClientsInInboundMap(m), nil
		}
	}
	return nil, 0, errors.New("inbound ", tag, " not found in config")
}

func countClientsInInboundMap(m map[string]interface{}) int {
	settings, _ := m["settings"].(map[string]interface{})
	if settings == nil {
		return 0
	}
	if clients, ok := settings["clients"].([]interface{}); ok {
		return len(clients)
	}
	if users, ok := settings["users"].([]interface{}); ok {
		return len(users)
	}
	return 0
}

func mergeInboundMapsPreserveClients(existing, patch map[string]interface{}) map[string]interface{} {
	out := shallowCopyMap(existing)
	for _, key := range []string{"listen", "port", "protocol", "streamSettings", "sniffing", "allocate"} {
		if v, ok := patch[key]; ok {
			out[key] = v
		}
	}
	if tag, ok := patch["tag"].(string); ok && tag != "" {
		out["tag"] = tag
	}
	if patchSettings, ok := patch["settings"].(map[string]interface{}); ok {
		baseSettings, _ := out["settings"].(map[string]interface{})
		if baseSettings == nil {
			baseSettings = map[string]interface{}{}
		} else {
			baseSettings = shallowCopyMap(baseSettings)
		}
		for k, v := range patchSettings {
			if k == "clients" || k == "users" {
				continue
			}
			baseSettings[k] = v
		}
		out["settings"] = baseSettings
	}
	return out
}

func shallowCopyMap(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func validateInboundMapMeta(m map[string]interface{}) error {
	buildRaw, err := stripInboundClientsForBuildMap(m)
	if err != nil {
		return err
	}
	return validateInboundRaw(buildRaw)
}

func stripInboundClientsForBuildMap(m map[string]interface{}) (json.RawMessage, error) {
	cp := shallowCopyMap(m)
	if settings, ok := cp["settings"].(map[string]interface{}); ok {
		settings = shallowCopyMap(settings)
		settings["clients"] = []interface{}{}
		delete(settings, "users")
		cp["settings"] = settings
	}
	raw, err := json.Marshal(cp)
	if err != nil {
		return nil, errors.New("marshal inbound").Base(err)
	}
	return raw, nil
}

// PatchConfigOutbounds upserts validated outbound JSON objects into the config file.
func PatchConfigOutbounds(configPath string, upsert []json.RawMessage, removeTags []string) error {
	for _, raw := range upsert {
		if err := validateOutboundRaw(raw); err != nil {
			return err
		}
	}
	root, err := readConfigRoot(configPath)
	if err != nil {
		return err
	}
	if err := patchOutboundsInRoot(root, upsert, removeTags); err != nil {
		return err
	}
	return validateAndWriteConfig(configPath, root)
}

// PatchConfigInboundUsers merges clients from request settings into an existing inbound in the config file.
func PatchConfigInboundUsers(configPath string, patches []apiserver.InboundUserFilePatch) error {
	if len(patches) == 0 {
		return errors.New("inbounds array is required")
	}
	root, err := readConfigRoot(configPath)
	if err != nil {
		return err
	}
	for _, p := range patches {
		if p.Tag == "" {
			return errors.New("inbound tag is required")
		}
		if p.Settings == nil {
			return errors.New("settings is required for inbound ", p.Tag)
		}
		protocol, err := inboundProtocolFromRoot(root, p.Tag)
		if err != nil {
			return err
		}
		if err := ValidateInboundUserSettings(protocol, p.Settings); err != nil {
			return err
		}
		if err := patchInboundClientsInRoot(root, p.Tag, *p.Settings, nil); err != nil {
			return err
		}
	}
	return validateAndWriteConfig(configPath, root)
}

// PatchConfigInboundUsersRemove removes users by email from an inbound in the config file.
func PatchConfigInboundUsersRemove(configPath, tag string, emails []string) error {
	if tag == "" {
		return errors.New("tag is required")
	}
	root, err := readConfigRoot(configPath)
	if err != nil {
		return err
	}
	if err := patchInboundClientsInRoot(root, tag, nil, emails); err != nil {
		return err
	}
	return validateAndWriteConfig(configPath, root)
}

// ListConfigInbounds returns inbounds from the config file (optionally filtered by runtime tags).
func ListConfigInbounds(configPath string, runtimeTags map[string]struct{}) ([]interface{}, error) {
	root, err := readConfigRoot(configPath)
	if err != nil {
		return nil, err
	}
	arr, _ := root["inbounds"].([]interface{})
	if len(runtimeTags) == 0 {
		return arr, nil
	}
	out := make([]interface{}, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		tag, _ := m["tag"].(string)
		if tag == "" {
			continue
		}
		if _, ok := runtimeTags[tag]; ok {
			out = append(out, m)
		}
	}
	return out, nil
}

// ListConfigOutbounds returns outbounds from the config file (filtered by runtime tags when set).
func ListConfigOutbounds(configPath string, runtimeTags map[string]struct{}) ([]interface{}, error) {
	root, err := readConfigRoot(configPath)
	if err != nil {
		return nil, err
	}
	arr, _ := root["outbounds"].([]interface{})
	if len(runtimeTags) == 0 {
		return arr, nil
	}
	out := make([]interface{}, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		tag, _ := m["tag"].(string)
		if tag == "" {
			continue
		}
		if _, ok := runtimeTags[tag]; ok {
			out = append(out, m)
		}
	}
	return out, nil
}

// ListConfigRules returns routing rules from the config file in config.json format.
func ListConfigRules(configPath string) ([]interface{}, error) {
	root, err := readConfigRoot(configPath)
	if err != nil {
		return nil, err
	}
	routing, _ := root["routing"].(map[string]interface{})
	if routing == nil {
		return nil, nil
	}
	arr, _ := routing["rules"].([]interface{})
	return arr, nil
}

func patchInboundsInRoot(root map[string]interface{}, upsert []json.RawMessage, removeTags []string) error {
	removed := tagSet(removeTags)
	upsertByTag := map[string]map[string]interface{}{}
	for _, raw := range upsert {
		var m map[string]interface{}
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		tag, _ := m["tag"].(string)
		if tag == "" {
			continue
		}
		upsertByTag[tag] = m
	}
	original, _ := root["inbounds"].([]interface{})
	root["inbounds"] = mergeTaggedSection(original, upsertByTag, removed)
	return nil
}

func patchOutboundsInRoot(root map[string]interface{}, upsert []json.RawMessage, removeTags []string) error {
	removed := tagSet(removeTags)
	upsertByTag := map[string]map[string]interface{}{}
	var appendUntagged []interface{}
	for _, raw := range upsert {
		var m map[string]interface{}
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		tag, _ := m["tag"].(string)
		if tag == "" {
			appendUntagged = append(appendUntagged, m)
			continue
		}
		upsertByTag[tag] = m
	}
	original, _ := root["outbounds"].([]interface{})
	merged := mergeTaggedSection(original, upsertByTag, removed)
	if len(appendUntagged) > 0 {
		merged = append(merged, appendUntagged...)
	}
	root["outbounds"] = merged
	return nil
}

func patchInboundClientsInRoot(root map[string]interface{}, tag string, settings json.RawMessage, removeEmails []string) error {
	arr, ok := root["inbounds"].([]interface{})
	if !ok {
		return errors.New("inbound ", tag, " not found in config")
	}
	var addClients []interface{}
	if settings != nil {
		var settingsMap map[string]interface{}
		if err := json.Unmarshal(settings, &settingsMap); err != nil {
			return errors.New("invalid settings json").Base(err)
		}
		if clients, ok := settingsMap["clients"].([]interface{}); ok {
			addClients = clients
		}
	}
	found := false
	for i, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		itemTag, _ := m["tag"].(string)
		if itemTag != tag {
			continue
		}
		found = true
		settingsMap, _ := m["settings"].(map[string]interface{})
		if settingsMap == nil {
			settingsMap = map[string]interface{}{}
		}
		mergeClientsInSettings(settingsMap, addClients, removeEmails)
		m["settings"] = settingsMap
		arr[i] = m
		break
	}
	if !found {
		return errors.New("inbound ", tag, " not found in config")
	}
	root["inbounds"] = arr
	return nil
}

func mergeClientsInSettings(settings map[string]interface{}, addClients []interface{}, removeEmails []string) {
	removeSet := tagSet(removeEmails)
	var kept []interface{}
	if existing, ok := settings["clients"].([]interface{}); ok {
		for _, cl := range existing {
			cm, ok := cl.(map[string]interface{})
			if !ok {
				kept = append(kept, cl)
				continue
			}
			email, _ := cm["email"].(string)
			if email != "" {
				if _, remove := removeSet[email]; remove {
					continue
				}
			}
			kept = append(kept, cl)
		}
	}
	for _, cl := range addClients {
		cm, ok := cl.(map[string]interface{})
		if !ok {
			kept = append(kept, cl)
			continue
		}
		email, _ := cm["email"].(string)
		replaced := false
		if email != "" {
			for i, ex := range kept {
				em, ok := ex.(map[string]interface{})
				if ok && em["email"] == email {
					kept[i] = cl
					replaced = true
					break
				}
			}
		}
		if !replaced {
			kept = append(kept, cl)
		}
	}
	settings["clients"] = kept
}

func mergeTaggedSection(original []interface{}, upsertByTag map[string]map[string]interface{}, removed map[string]struct{}) []interface{} {
	out := make([]interface{}, 0, len(original)+len(upsertByTag))
	seen := map[string]bool{}
	for _, item := range original {
		m, ok := item.(map[string]interface{})
		if !ok {
			out = append(out, item)
			continue
		}
		tag, _ := m["tag"].(string)
		if tag != "" {
			if _, isRemoved := removed[tag]; isRemoved {
				continue
			}
			if upd, ok := upsertByTag[tag]; ok {
				out = append(out, upd)
				seen[tag] = true
				continue
			}
		}
		out = append(out, m)
	}
	for tag, m := range upsertByTag {
		_, isRemoved := removed[tag]
		if !seen[tag] && !isRemoved {
			out = append(out, m)
		}
	}
	return out
}

func tagSet(tags []string) map[string]struct{} {
	s := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		if t != "" {
			s[t] = struct{}{}
		}
	}
	return s
}

func validateAndWriteConfig(configPath string, root map[string]interface{}) error {
	if err := validateConfigRoot(root); err != nil {
		return err
	}
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return errors.New("marshal config").Base(err)
	}
	dir := filepath.Dir(configPath)
	tmp, err := os.CreateTemp(dir, ".xray-config-*.json")
	if err != nil {
		return errors.New("create temp config").Base(err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return errors.New("write temp config").Base(err)
	}
	if err := tmp.Close(); err != nil {
		return errors.New("close temp config").Base(err)
	}
	if err := os.Rename(tmpPath, configPath); err != nil {
		return errors.New("replace config file").Base(err)
	}
	return nil
}

// PatchConfigRulesAdd merges validated routing rules from request JSON into the config file.
func PatchConfigRulesAdd(configPath string, routing json.RawMessage, prepend bool) error {
	if err := ValidateRoutingRules(routing); err != nil {
		return err
	}
	var incoming RouterConfig
	_ = json.Unmarshal(routing, &incoming)
	root, err := readConfigRoot(configPath)
	if err != nil {
		return err
	}
	routingMap, ok := root["routing"].(map[string]interface{})
	if !ok {
		routingMap = map[string]interface{}{}
		root["routing"] = routingMap
	}
	existing, _ := routingMap["rules"].([]interface{})
	var add []interface{}
	for _, raw := range incoming.RuleList {
		var m map[string]interface{}
		if err := json.Unmarshal(raw, &m); err != nil {
			return errors.New("invalid rule json").Base(err)
		}
		add = append(add, m)
	}
	if prepend {
		routingMap["rules"] = append(add, existing...)
	} else {
		routingMap["rules"] = append(existing, add...)
	}
	return validateAndWriteConfig(configPath, root)
}

// PatchConfigRulesEdit replaces one routing rule in the config file by ruleTag.
func PatchConfigRulesEdit(configPath, ruleTag string, rule json.RawMessage) error {
	if ruleTag == "" {
		return errors.New("rule_tag is required")
	}
	wrapped, err := wrapRoutingRulesInput(rule)
	if err != nil {
		return err
	}
	if err := ValidateRoutingRules(wrapped); err != nil {
		return err
	}
	var incoming RouterConfig
	if err := json.Unmarshal(wrapped, &incoming); err != nil {
		return errors.New("invalid routing json").Base(err)
	}
	if len(incoming.RuleList) != 1 {
		return errors.New("exactly one rule is required")
	}
	var newRule map[string]interface{}
	if err := json.Unmarshal(incoming.RuleList[0], &newRule); err != nil {
		return errors.New("invalid rule json").Base(err)
	}
	root, err := readConfigRoot(configPath)
	if err != nil {
		return err
	}
	routingMap, ok := root["routing"].(map[string]interface{})
	if !ok {
		return errors.New("routing section not found in config")
	}
	arr, ok := routingMap["rules"].([]interface{})
	if !ok || len(arr) == 0 {
		return errors.New("no routing rules in config")
	}
	found := false
	for i, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		tag, _ := m["ruleTag"].(string)
		if tag == ruleTag {
			arr[i] = newRule
			found = true
			break
		}
	}
	if !found {
		return errors.New("routing rule not found in config: ", ruleTag)
	}
	routingMap["rules"] = arr
	return validateAndWriteConfig(configPath, root)
}

func wrapRoutingRulesInput(rule json.RawMessage) (json.RawMessage, error) {
	if len(rule) == 0 {
		return nil, errors.New("rule is required")
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(rule, &probe); err != nil {
		return nil, errors.New("invalid rule json").Base(err)
	}
	if raw, ok := probe["rules"]; ok {
		return json.RawMessage(`{"rules":` + string(raw) + `}`), nil
	}
	if raw, ok := probe["routing"]; ok {
		return raw, nil
	}
	wrapped, err := json.Marshal(map[string]interface{}{"rules": []json.RawMessage{rule}})
	if err != nil {
		return nil, errors.New("marshal routing rule").Base(err)
	}
	return wrapped, nil
}

// PatchConfigRulesRemove removes routing rules from the config file by ruleTag and/or index.
func PatchConfigRulesRemove(configPath string, ruleTags []string, indices []int) error {
	if len(ruleTags) == 0 && len(indices) == 0 {
		return errors.New("rule_tags or indices required")
	}
	root, err := readConfigRoot(configPath)
	if err != nil {
		return err
	}
	routingMap, ok := root["routing"].(map[string]interface{})
	if !ok {
		return errors.New("routing section not found in config")
	}
	arr, ok := routingMap["rules"].([]interface{})
	if !ok || len(arr) == 0 {
		return errors.New("no routing rules in config")
	}
	removeTags := tagSet(ruleTags)
	for idx := range sortIndicesDesc(indices) {
		if idx < 0 || idx >= len(arr) {
			return errors.New("rule index out of range: ", idx)
		}
		arr = append(arr[:idx], arr[idx+1:]...)
	}
	if len(removeTags) > 0 {
		filtered := make([]interface{}, 0, len(arr))
		for _, item := range arr {
			m, ok := item.(map[string]interface{})
			if !ok {
				filtered = append(filtered, item)
				continue
			}
			tag, _ := m["ruleTag"].(string)
			if tag != "" {
				if _, remove := removeTags[tag]; remove {
					continue
				}
			}
			filtered = append(filtered, m)
		}
		arr = filtered
	}
	routingMap["rules"] = arr
	return validateAndWriteConfig(configPath, root)
}

// PatchConfigRulesReplace replaces the entire routing.rules list (and optional domainStrategy/domainMatcher).
func PatchConfigRulesReplace(configPath string, routing json.RawMessage) error {
	if err := ValidateRoutingRules(routing); err != nil {
		return err
	}
	var incoming map[string]interface{}
	if err := json.Unmarshal(routing, &incoming); err != nil {
		return errors.New("invalid routing json").Base(err)
	}
	rules, ok := incoming["rules"].([]interface{})
	if !ok {
		return errors.New("routing.rules is required")
	}
	root, err := readConfigRoot(configPath)
	if err != nil {
		return err
	}
	routingMap, ok := root["routing"].(map[string]interface{})
	if !ok {
		routingMap = map[string]interface{}{}
		root["routing"] = routingMap
	}
	routingMap["rules"] = rules
	if v, ok := incoming["domainStrategy"]; ok {
		routingMap["domainStrategy"] = v
	}
	if v, ok := incoming["domainMatcher"]; ok {
		routingMap["domainMatcher"] = v
	}
	if v, ok := incoming["balancers"]; ok {
		routingMap["balancers"] = v
	}
	return validateAndWriteConfig(configPath, root)
}

func sortIndicesDesc(indices []int) []int {
	if len(indices) == 0 {
		return nil
	}
	out := append([]int(nil), indices...)
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] > out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
