package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// ToolGroups is a fresh copy of the fixed public catalog. No arbitrary Slack
// method names or implicit dependencies can be registered through configuration.
func ToolGroups() map[string][]string {
	return map[string][]string{
		"legacy_read":   {"slack_channels_list", "slack_conversations_history", "slack_conversations_replies"},
		"diagnostics":   {"slack_auth_test"},
		"conversations": {"slack_conversations_info", "slack_conversations_members"},
		"discovery":     {"slack_chat_get_permalink", "slack_users_info", "slack_messages_search"},
		"messages":      {"slack_chat_post_message", "slack_chat_update", "slack_chat_delete"},
		"schedule":      {"slack_chat_schedule_message", "slack_chat_scheduled_messages_list", "slack_chat_delete_scheduled_message"},
		"reactions":     {"slack_reactions_get", "slack_reactions_add", "slack_reactions_remove"},
		"pins":          {"slack_pins_list", "slack_pins_add", "slack_pins_remove"},
		"bookmarks":     {"slack_bookmarks_list", "slack_bookmarks_add", "slack_bookmarks_edit", "slack_bookmarks_remove"},
		"files":         {"slack_files_info", "slack_files_download", "slack_files_upload"},
		"dms":           {"slack_dms_open"},
	}
}

// Policy is immutable after startup. Zero value preserves the legacy catalog.
type Policy struct {
	Write                                 Allowlist
	TeamID, BotID, StateDir, ArtifactRoot string
	DMRead, DMWrite, MPIMRead, MPIMWrite  bool
	users                                 map[string]bool
	tools                                 map[string]bool
	groups                                []string
}

func (p Policy) Enabled(name string) bool {
	if p.tools == nil {
		return name == "slack_channels_list" || name == "slack_conversations_history" || name == "slack_conversations_replies"
	}
	return p.tools[name]
}
func (p Policy) Expanded() bool             { return p.TeamID != "" }
func (p Policy) UserAllowed(id string) bool { return p.users[id] }
func (p Policy) ToolNames() []string {
	var names []string
	for _, group := range ToolGroups() {
		for _, name := range group {
			if p.Enabled(name) {
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)
	return names
}
func (p Policy) Capabilities() []string {
	if p.groups == nil {
		return []string{"legacy_read"}
	}
	return append([]string{}, p.groups...)
}

var slackID = regexp.MustCompile(`^[A-Z][A-Z0-9]{6,20}$`)

func validID(s, prefixes string) bool {
	return slackID.MatchString(s) && strings.ContainsRune(prefixes, rune(s[0]))
}
func csv(raw string) ([]string, error) {
	var out []string
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, fmt.Errorf("empty configuration list entry")
		}
		out = append(out, s)
	}
	return out, nil
}
func loadPolicy(getenv func(string) string) (Policy, error) {
	p := Policy{users: map[string]bool{}}
	raw := strings.TrimSpace(getenv("SLACK_CAPABILITIES"))
	if raw == "" {
		raw = "legacy_read"
	}
	groups, err := csv(raw)
	if err != nil {
		return p, err
	}
	catalog := ToolGroups()
	p.tools = map[string]bool{}
	seen := map[string]bool{}
	expanded := false
	for _, group := range groups {
		names, ok := catalog[group]
		if !ok {
			return p, fmt.Errorf("SLACK_CAPABILITIES contains an unknown group")
		}
		if !seen[group] {
			p.groups = append(p.groups, group)
			seen[group] = true
		}
		if group != "legacy_read" {
			expanded = true
		}
		for _, n := range names {
			p.tools[n] = true
		}
	}
	sort.Strings(p.groups)
	if raw = getenv("SLACK_ENABLED_TOOLS"); strings.TrimSpace(raw) != "" {
		names, e := csv(raw)
		if e != nil {
			return p, e
		}
		filter := map[string]bool{}
		for _, n := range names {
			if !p.tools[n] {
				return p, fmt.Errorf("SLACK_ENABLED_TOOLS contains an unknown or disabled tool")
			}
			filter[n] = true
		}
		p.tools = filter
	}
	if raw = getenv("SLACK_WRITE_ALLOWED_CHANNELS"); strings.TrimSpace(raw) != "" {
		ids, e := csv(raw)
		if e != nil {
			return p, e
		}
		for _, id := range ids {
			if !validID(id, "CG") {
				return p, fmt.Errorf("SLACK_WRITE_ALLOWED_CHANNELS requires explicit C/G IDs")
			}
		}
		p.Write = newAllowlist(ids)
	}
	for _, item := range []struct {
		name  string
		value *bool
	}{{"SLACK_DM_READ_ENABLED", &p.DMRead}, {"SLACK_DM_WRITE_ENABLED", &p.DMWrite}, {"SLACK_MPIM_READ_ENABLED", &p.MPIMRead}, {"SLACK_MPIM_WRITE_ENABLED", &p.MPIMWrite}} {
		switch strings.TrimSpace(getenv(item.name)) {
		case "", "false":
		case "true":
			*item.value = true
		default:
			return p, fmt.Errorf("%s must be true or false", item.name)
		}
	}
	if raw = getenv("SLACK_DM_ALLOWED_USERS"); strings.TrimSpace(raw) != "" {
		ids, e := csv(raw)
		if e != nil {
			return p, e
		}
		for _, id := range ids {
			if !validID(id, "UW") {
				return p, fmt.Errorf("SLACK_DM_ALLOWED_USERS requires explicit user IDs")
			}
			p.users[id] = true
		}
	}
	if p.DMRead || p.DMWrite || p.MPIMRead || p.MPIMWrite {
		expanded = true
		if !seen["dms"] || len(p.users) == 0 {
			return p, fmt.Errorf("DM permissions require dms capability and SLACK_DM_ALLOWED_USERS")
		}
	}
	p.TeamID = strings.TrimSpace(getenv("SLACK_EXPECTED_TEAM_ID"))
	p.BotID = strings.TrimSpace(getenv("SLACK_EXPECTED_BOT_ID"))
	if expanded && !validID(p.TeamID, "T") {
		return p, fmt.Errorf("expanded capabilities require SLACK_EXPECTED_TEAM_ID")
	}
	if p.TeamID != "" && !validID(p.TeamID, "T") {
		return p, fmt.Errorf("invalid SLACK_EXPECTED_TEAM_ID")
	}
	if p.BotID != "" && !validID(p.BotID, "B") {
		return p, fmt.Errorf("invalid SLACK_EXPECTED_BOT_ID")
	}
	p.StateDir = strings.TrimSpace(getenv("SLACK_STATE_DIR"))
	p.ArtifactRoot = strings.TrimSpace(getenv("SLACK_ARTIFACT_ROOT"))
	for _, path := range []string{p.StateDir, p.ArtifactRoot} {
		if path != "" && !filepath.IsAbs(path) {
			return p, fmt.Errorf("state and artifact directories must be absolute")
		}
	}
	for _, name := range []string{"slack_chat_post_message", "slack_chat_schedule_message", "slack_files_upload"} {
		if p.Enabled(name) && p.StateDir == "" {
			return p, fmt.Errorf("post, schedule and upload require SLACK_STATE_DIR")
		}
	}
	if err := p.CheckPlatform(runtime.GOOS); err != nil {
		return p, err
	}
	return p, nil
}

// CheckPlatform deliberately refuses writes on Windows until receipt ACLs,
// directory durability and race-safe file opening have a native implementation.
// The default legacy catalog and optional read/diagnostic tools remain usable.
func (p Policy) CheckPlatform(goos string) error {
	if goos != "windows" {
		return nil
	}
	for _, name := range p.ToolNames() {
		switch name {
		case "slack_chat_post_message", "slack_chat_update", "slack_chat_delete", "slack_chat_schedule_message", "slack_chat_delete_scheduled_message", "slack_reactions_add", "slack_reactions_remove", "slack_pins_add", "slack_pins_remove", "slack_bookmarks_add", "slack_bookmarks_edit", "slack_bookmarks_remove", "slack_files_upload", "slack_dms_open":
			return fmt.Errorf("expanded Slack writes are unsupported on Windows; legacy reads remain available")
		}
	}
	if p.StateDir != "" || p.DMWrite || p.MPIMWrite {
		return fmt.Errorf("write state and DM write permissions are unsupported on Windows")
	}
	return nil
}
