package mail

import (
	"errors"
	"testing"
)

// CanReadMailbox answers from the configuration alone why a Worker has no mailbox, so a
// watch is refused on a Worker that could never serve it.
func TestCanReadMailbox(t *testing.T) {
	for _, tc := range []struct {
		provider, sender, imap string
		want                   error
	}{
		{"", "", "", errNoIMAPEndpoint},
		{" SMTP ", "", "imaps://mail.example.ch:993", nil},
		{ProviderMicrosoft, "", "", errGraphHasNoSender},
		{ProviderMicrosoft, "atlas@example.ch", "", nil},
		{ProviderPreview, "atlas@example.ch", "imaps://x", errPreviewHasNoMailbox},
		{"gmail", "", "", nil},
	} {
		if got := CanReadMailbox(tc.provider, tc.sender, tc.imap); !errors.Is(got, tc.want) {
			t.Errorf("CanReadMailbox(%q, %q, %q) = %v, want %v", tc.provider, tc.sender, tc.imap, got, tc.want)
		}
	}
}
