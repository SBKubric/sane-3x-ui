package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/web/service"
)

// Tests for `x-ui setting -tgCaptchaHost` (#243): where the bot's captcha
// opens, as the orchestrator sets it — edge, panel or a hop's name, "" for
// the default.

func TestRunTgCaptchaHostSetting(t *testing.T) {
	newMonTestDB(t)
	s := service.SettingService{}
	var out bytes.Buffer
	for _, value := range []string{"panel", "edge", "edge-b"} {
		out.Reset()
		if err := runTgCaptchaHostSetting(&out, value); err != nil {
			t.Fatalf("%q: %v", value, err)
		}
		if out.String() != "tgCaptchaHost: "+value+"\n" {
			t.Errorf("%q: output %q", value, out.String())
		}
		if got, _ := s.GetTgCaptchaHost(); got != value {
			t.Errorf("%q: stored %q", value, got)
		}
	}

	// A refusal stores nothing, says why, and fails the command.
	out.Reset()
	err := runTgCaptchaHostSetting(&out, "https://panel.example.com")
	if err == nil || !strings.Contains(err.Error(), "tgCaptchaHost") || out.Len() != 0 {
		t.Errorf("an address: %v, output %q", err, out.String())
	}
	if got, _ := s.GetTgCaptchaHost(); got != "edge-b" {
		t.Errorf("after the refusal: %q", got)
	}

	// "" puts the default back.
	out.Reset()
	if err := runTgCaptchaHostSetting(&out, ""); err != nil {
		t.Fatal(err)
	}
	if out.String() != "tgCaptchaHost: \n" {
		t.Errorf("output %q", out.String())
	}
	if got, _ := s.GetTgCaptchaHost(); got != "" {
		t.Errorf("the default: %q", got)
	}
}
