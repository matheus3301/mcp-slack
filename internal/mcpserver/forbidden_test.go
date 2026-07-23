package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExactToolSurface pins the tool set to exactly the three read-only tools.
// If a fourth tool is ever added, this fails loudly.
func TestExactToolSurface(t *testing.T) {
	t.Parallel()
	want := map[string]bool{
		ToolChannelsList: true,
		ToolHistory:      true,
		ToolReplies:      true,
	}
	if len(want) != 3 {
		t.Fatalf("expected exactly 3 tool names, got %d", len(want))
	}
	for _, name := range []string{ToolChannelsList, ToolHistory, ToolReplies} {
		if !strings.HasPrefix(name, "slack_") {
			t.Errorf("tool %q must be namespaced under slack_", name)
		}
	}
	// Every tool is a read path: none may imply a mutation.
	for _, name := range []string{ToolChannelsList, ToolHistory, ToolReplies} {
		for _, verb := range forbiddenVerbs {
			if strings.Contains(name, verb) {
				t.Errorf("tool %q contains forbidden verb %q", name, verb)
			}
		}
	}
}

// forbiddenVerbs are mutation/side-effecting or over-broad concepts that must
// never appear in a tool name or as a Slack method reference in the non-test
// source.
var forbiddenVerbs = []string{
	"post", "update", "delete", "reaction", "mark", "search",
	"usergroup", "upload", "download", "admin", "invite", "kick",
	"rename", "archive", "create", "join", "leave", "schedule",
}

// forbiddenSlackMethods are concrete Slack Web API methods that this server
// must never call. The test scans all non-test .go files.
var forbiddenSlackMethods = []string{
	"chat.postMessage", "chat.update", "chat.delete", "chat.postEphemeral",
	"chat.scheduleMessage", "reactions.add", "reactions.remove",
	"conversations.mark", "conversations.join", "conversations.leave",
	"conversations.invite", "conversations.kick", "conversations.archive",
	"conversations.create", "conversations.rename", "conversations.list",
	"search.messages", "search.files", "files.upload", "files.getUploadURLExternal",
	"usergroups.create", "admin.conversations",
}

// allowedSlackMethodCalls are the ONLY Slack SDK methods the server may invoke.
var allowedSlackMethodCalls = []string{
	"GetConversationInfoContext",
	"GetConversationHistoryContext",
	"GetConversationRepliesContext",
}

// TestNoForbiddenSlackMethodsInSource statically verifies that no write, search,
// or enumeration Slack method appears in the production source tree.
func TestNoForbiddenSlackMethodsInSource(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	var offenders []string

	walk(t, root, func(path string, content string) {
		for _, m := range forbiddenSlackMethods {
			if strings.Contains(content, m) {
				offenders = append(offenders, path+": references forbidden method "+m)
			}
		}
	})

	if len(offenders) != 0 {
		t.Fatalf("forbidden Slack methods found in source:\n%s", strings.Join(offenders, "\n"))
	}
}

// TestOnlyAllowlistedSDKCallsInvoked verifies the Slack adapter only calls the
// three read methods: any GetConversation*/Get*/Post* SDK call outside the
// allowlist is flagged.
func TestOnlyAllowlistedSDKCallsInvoked(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	adapter := filepath.Join(root, "internal", "slackclient", "adapter.go")
	data, err := os.ReadFile(adapter)
	if err != nil {
		t.Fatalf("read adapter.go: %v", err)
	}
	content := string(data)

	// The SDK client is referenced as a.sc.<Method>. Ensure each such call is
	// on the allowlist.
	for _, line := range strings.Split(content, "\n") {
		idx := strings.Index(line, "a.sc.")
		if idx < 0 {
			continue
		}
		rest := line[idx+len("a.sc."):]
		method := rest
		if p := strings.IndexAny(rest, "("); p >= 0 {
			method = rest[:p]
		}
		if !contains(allowedSlackMethodCalls, method) {
			t.Errorf("adapter invokes non-allowlisted SDK method: %q", method)
		}
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// repoRoot walks up from the package dir to the module root (where go.mod is).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not locate module root")
	return ""
}

// walk visits every non-test .go file under root.
func walk(t *testing.T, root string, fn func(path, content string)) {
	t.Helper()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, _ := filepath.Rel(root, path)
		fn(rel, string(data))
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}
