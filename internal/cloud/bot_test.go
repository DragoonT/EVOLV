package cloud

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		input      string
		wantAction string
		wantArgs   int
		wantFound  bool
	}{
		{"@evolv-bot fix memory leak in queue", "fix", 4, true},
		{"Hey team,\n/evolv review\nThanks!", "review", 0, true},
		{"@evolv test", "test", 0, true},
		{"@evolv-bot evolv kernel logic", "evolv", 2, true},
		{"@evolv-bot help", "help", 0, true},
		{"just a regular comment without any bot triggers", "", 0, false},
		{"", "", 0, false},
	}

	for _, tt := range tests {
		cmd, found := ParseCommand(tt.input)
		if found != tt.wantFound {
			t.Errorf("ParseCommand(%q) found = %v, want %v", tt.input, found, tt.wantFound)
			continue
		}
		if found && cmd.Action != tt.wantAction {
			t.Errorf("ParseCommand(%q) action = %q, want %q", tt.input, cmd.Action, tt.wantAction)
		}
		if found && len(cmd.Args) != tt.wantArgs {
			t.Errorf("ParseCommand(%q) args len = %d, want %d", tt.input, len(cmd.Args), tt.wantArgs)
		}
	}
}

func TestVerifyWebhookSignature(t *testing.T) {
	secret := "super-secret-token"
	payload := []byte(`{"action":"created","issue":{"number":42}}`)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	validSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if !VerifyWebhookSignature(secret, payload, validSig) {
		t.Errorf("expected signature to be valid")
	}

	// Tampered payload
	tampered := []byte(`{"action":"created","issue":{"number":43}}`)
	if VerifyWebhookSignature(secret, tampered, validSig) {
		t.Errorf("expected tampered payload signature to fail")
	}

	// Bad header
	if VerifyWebhookSignature(secret, payload, "sha256=invalidhex") {
		t.Errorf("expected invalid hex signature to fail")
	}
}

func TestBotExecuteCommandIssueComment(t *testing.T) {
	meter := NewMeter()
	meter.RegisterTenant("test-org", "Test Organization", TierPro)

	mockGH := NewMockGitHubClient()
	bot := NewBot("test-secret", meter, nil, mockGH)

	commentEv := WebhookCommentEvent{
		Action: "created",
	}
	commentEv.Issue.Number = 101
	commentEv.Issue.Title = "Fix database race condition"
	commentEv.Comment.Body = "@evolv-bot fix please patch this race"
	commentEv.Repository.FullName = "myorg/backend"

	payload, err := json.Marshal(commentEv)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	res, err := bot.HandleEvent(context.Background(), "test-org", "issue_comment", payload)
	if err != nil {
		t.Fatalf("handle event: %v", err)
	}
	if res == nil {
		t.Fatalf("expected non-nil dispatch result")
	}
	if res.Action != "fix" || res.Number != 101 {
		t.Errorf("result mismatch: action=%s number=%d", res.Action, res.Number)
	}
	if res.PRUrl == "" {
		t.Errorf("expected PR URL to be generated")
	}

	// Verify GitHub comments were delivered
	key := fmt.Sprintf("%s#%d", "myorg/backend", 101)
	comments := mockGH.Comments[key]
	if len(comments) < 2 {
		t.Fatalf("expected at least 2 comments (ack + result), got %d", len(comments))
	}

	// Verify usage metered
	usage, err := meter.GetUsage("test-org")
	if err != nil {
		t.Fatalf("get usage: %v", err)
	}
	if usage.RunsCount != 1 {
		t.Errorf("runs count = %d, want 1", usage.RunsCount)
	}
}
