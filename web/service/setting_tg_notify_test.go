package service

import (
	"strings"
	"testing"
)

// TestTgNotifyChatIdSetting: the notification channel (#195) is empty on a
// fresh panel, takes a chat id or an @username through the settings form
// with the spaces around it trimmed, and refuses anything else without
// touching the stored value.
func TestTgNotifyChatIdSetting(t *testing.T) {
	s := newMonitoringSettingService(t)

	all, err := s.GetAllSetting()
	if err != nil {
		t.Fatalf("GetAllSetting: %v", err)
	}
	if all.TgNotifyChatId != "" {
		t.Errorf("default = %q, want empty", all.TgNotifyChatId)
	}

	for _, value := range []string{"-1001234567890", " @my_channel ", "123456789", ""} {
		all.TgNotifyChatId = value
		if err := s.UpdateAllSetting(all); err != nil {
			t.Fatalf("save %q: %v", value, err)
		}
		got, err := s.GetTgNotifyChatId()
		if err != nil {
			t.Fatal(err)
		}
		if want := strings.TrimSpace(value); got != want {
			t.Errorf("save %q: stored %q, want %q", value, got, want)
		}
	}

	all.TgNotifyChatId = "@kept_channel"
	if err := s.UpdateAllSetting(all); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"my_channel", "@abc", "@1channel", "@bad-name", "-100abc", "0", "https://t.me/my_channel", "@" + strings.Repeat("a", 33)} {
		all.TgNotifyChatId = bad
		err := s.UpdateAllSetting(all)
		if err == nil || !strings.Contains(err.Error(), "notification channel") {
			t.Errorf("save %q: err = %v, want a notification channel error", bad, err)
		}
	}
	if got, _ := s.GetTgNotifyChatId(); got != "@kept_channel" {
		t.Errorf("after refused saves the channel is %q", got)
	}
}
