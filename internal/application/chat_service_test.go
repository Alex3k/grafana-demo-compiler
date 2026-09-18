package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Alex3k/grafana-demo-compiler/internal/chat"
)

func TestSuggestedTitle(t *testing.T) {
	t.Parallel()

	if got := SuggestedTitle("  build a focused camera fleet demo for tomorrow please  "); got != "build a focused camera fleet demo for" {
		t.Fatalf("SuggestedTitle() = %q", got)
	}
	if got := SuggestedTitle("   "); got != "Untitled demo" {
		t.Fatalf("SuggestedTitle(empty) = %q", got)
	}
}

func TestValidateMessage(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		content string
		want    string
	}{
		{name: "empty", want: "A message is required"},
		{name: "too long", content: strings.Repeat("x", 32_001), want: "Message exceeds 32,000 characters"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateMessage(test.content)
			var applicationFault *Fault
			if !errors.As(err, &applicationFault) || applicationFault.Public != test.want {
				t.Fatalf("validateMessage() error = %#v, want Fault with public %q", err, test.want)
			}
		})
	}
}

func TestFriendlyChatError(t *testing.T) {
	t.Parallel()

	if got := FriendlyChatError(chat.ErrNotConfigured); !strings.Contains(got, "Amazon Bedrock is not configured") {
		t.Fatalf("FriendlyChatError(not configured) = %q", got)
	}
	if got := FriendlyChatError(chat.ErrIncompleteResponse); !strings.Contains(got, "Check Grafana actions") || !strings.Contains(got, "before retrying") {
		t.Fatalf("FriendlyChatError(incomplete) = %q", got)
	}
	if got := FriendlyChatError(context.Canceled); got != "The response was interrupted. You can retry your message." {
		t.Fatalf("FriendlyChatError(canceled) = %q", got)
	}
	if got := FriendlyChatError(errors.New("provider failed")); got != "The assistant could not complete this response. Check the server log and retry." {
		t.Fatalf("FriendlyChatError(other) = %q", got)
	}
}
