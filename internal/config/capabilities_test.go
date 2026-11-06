package config

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestExplicitReadAndExactFilter(t *testing.T) {
	env := map[string]string{EnvBotToken: "xoxb-synthetic-test-token", EnvAllowedChannels: "C01234567", "SLACK_CAPABILITIES": "read", "SLACK_EXPECTED_TEAM_ID": "T01234567"}
	load := func() (*Config, error) { return Load(func(k string) string { return env[k] }) }
	c, e := load()
	if e != nil {
		t.Fatal(e)
	}
	if len(c.Policy.ToolNames()) != 3 || c.Policy.Write.Len() != 0 {
		t.Fatal("read group must enable exactly three read tools")
	}
	if e = c.Policy.CheckPlatform("windows"); e != nil {
		t.Fatal("explicit Windows reads failed")
	}
	env["SLACK_CAPABILITIES"] = "messages,diagnostics,schedule"
	env["SLACK_ENABLED_TOOLS"] = "slack_chat_post_message,slack_auth_test,slack_chat_update,slack_chat_schedule_message,slack_chat_scheduled_messages_list,slack_chat_delete_scheduled_message"
	env["SLACK_EXPECTED_TEAM_ID"] = "T01234567"
	env["SLACK_STATE_DIR"] = "/operator/receipts"
	delete(env, EnvAllowedChannels)
	c, e = load()
	if e != nil {
		t.Fatal(e)
	}
	if c.Allowlist().Len() != 0 || len(c.Policy.ToolNames()) != 6 || c.Policy.Enabled("slack_chat_delete") {
		t.Fatal("exact filter failed")
	}
	want := strings.Split(env["SLACK_ENABLED_TOOLS"], ",")
	sort.Strings(want)
	if !reflect.DeepEqual(want, c.Policy.ToolNames()) {
		t.Fatal("nondeterministic catalog")
	}
	if e = c.Policy.CheckPlatform("windows"); e == nil || !strings.Contains(e.Error(), "unsupported on Windows") {
		t.Fatal("writes must fail on Windows")
	}
}
func TestCapabilityConfigurationDenials(t *testing.T) {
	cases := []struct{ key, value string }{
		{"SLACK_CAPABILITIES", "*"}, {"SLACK_CAPABILITIES", "messages,unknown"}, {"SLACK_CAPABILITIES", "messages,"}, {"SLACK_ENABLED_TOOLS", "slack_unknown"}, {"SLACK_ENABLED_TOOLS", "slack_dms_open"},
		{"SLACK_WRITE_ALLOWED_CHANNELS", "*"}, {"SLACK_WRITE_ALLOWED_CHANNELS", "C01234567,*"}, {"SLACK_WRITE_ALLOWED_CHANNELS", "D01234567"}, {"SLACK_WRITE_ALLOWED_CHANNELS", "U01234567"}, {"SLACK_WRITE_ALLOWED_CHANNELS", "#general"}, {"SLACK_WRITE_ALLOWED_CHANNELS", "C01234567,"},
		{"SLACK_EXPECTED_TEAM_ID", ""}, {"SLACK_EXPECTED_TEAM_ID", "C01234567"}, {"SLACK_EXPECTED_BOT_ID", "U01234567"}, {"SLACK_STATE_DIR", ""}, {"SLACK_STATE_DIR", "relative"}, {"SLACK_ARTIFACT_ROOT", "relative"},
		{"SLACK_DM_READ_ENABLED", "yes"}, {"SLACK_DM_WRITE_ENABLED", "true"}, {"SLACK_MPIM_READ_ENABLED", "true"}, {"SLACK_DM_ALLOWED_USERS", "*"}, {"SLACK_DM_ALLOWED_USERS", "D01234567"},
	}
	for _, tc := range cases {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			env := map[string]string{EnvBotToken: "xoxb-synthetic-test-token", EnvAllowedChannels: "C01234567", "SLACK_CAPABILITIES": "messages", "SLACK_EXPECTED_TEAM_ID": "T01234567", "SLACK_STATE_DIR": "/operator/receipts"}
			env[tc.key] = tc.value
			c, e := Load(func(k string) string { return env[k] })
			if e == nil {
				t.Fatalf("accepted %+v", c)
			}
			if strings.Contains(e.Error(), env[EnvBotToken]) {
				t.Fatal("token leaked")
			}
		})
	}
}
func TestDMCapabilitiesRequireSeparatePermissions(t *testing.T) {
	env := map[string]string{EnvBotToken: "xoxb-synthetic-test-token", EnvAllowedChannels: "C01234567", "SLACK_CAPABILITIES": "read,dms", "SLACK_EXPECTED_TEAM_ID": "T01234567"}
	c, e := Load(func(k string) string { return env[k] })
	if e != nil {
		t.Fatal(e)
	}
	if c.Policy.DMRead || c.Policy.DMWrite || c.Policy.MPIMRead || c.Policy.MPIMWrite {
		t.Fatal("dms capability granted destination permission")
	}
	env["SLACK_DM_READ_ENABLED"] = "true"
	if _, e = Load(func(k string) string { return env[k] }); e == nil {
		t.Fatal("DM without recipient allowlist")
	}
	env["SLACK_DM_ALLOWED_USERS"] = "U01234567"
	c, e = Load(func(k string) string { return env[k] })
	if e != nil {
		t.Fatal(e)
	}
	if !c.Policy.DMRead || c.Policy.DMWrite || c.Policy.MPIMRead {
		t.Fatal("DM grant expanded to other modes")
	}
}

func TestExplicitCapabilitiesAndWorkspaceRequired(t *testing.T) {
	for _, group := range []string{"read", "diagnostics", "conversations", "discovery"} {
		env := map[string]string{EnvBotToken: goodToken, EnvAllowedChannels: "C01234567", "SLACK_CAPABILITIES": group, "SLACK_EXPECTED_TEAM_ID": "T01234567"}
		for _, key := range []string{"SLACK_CAPABILITIES", "SLACK_EXPECTED_TEAM_ID"} {
			for _, value := range []string{"", "   "} {
				t.Run(group+"/"+key+"/"+value, func(t *testing.T) {
					original := env[key]
					env[key] = value
					defer func() { env[key] = original }()
					if _, e := Load(fakeEnv(env)); e == nil || !strings.Contains(e.Error(), key) {
						t.Fatalf("missing explicit config accepted: %v", e)
					}
				})
			}
		}
	}
	env := map[string]string{EnvBotToken: goodToken, EnvAllowedChannels: "C01234567", "SLACK_CAPABILITIES": "legacy_read", "SLACK_EXPECTED_TEAM_ID": "T01234567"}
	if _, e := Load(fakeEnv(env)); e == nil {
		t.Fatal("removed capability alias accepted")
	}
	var empty Policy
	if len(empty.ToolNames()) != 0 || len(empty.Capabilities()) != 0 || empty.Enabled("slack_channels_list") {
		t.Fatal("zero policy enabled a fallback catalog")
	}
}
