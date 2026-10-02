package service

import (
	"fmt"
	"strings"
	"testing"
)

// What a person gets when they register (#245): one message, the link to
// their subscription page over «My subscription» with its buttons — no QR
// pictures, no .conf files, no separate xray links. The configs stay a press
// away on «🔗 Show subscription» and «📄 My configs».

// filesSent are the calls of fake that sent something other than a text
// message: a photo, a document, an album.
func (f *fakeTelegram) filesSent() []string {
	var out []string
	for _, c := range f.calls {
		if strings.HasPrefix(c.method, "send") && c.method != "sendMessage" {
			out = append(out, c.method)
		}
	}
	return out
}

// pageLine is the line with the subscription page's link, as the fixtures'
// panel hands it out.
func pageLine(subId string) string {
	return "🔗 Your subscription page: http://localhost:2096/sub/" + subId
}

// TestApprovalSendsTheLinkOnly: «✅ Approve» of a request whose defaults
// carry an AWG client tells the applicant in one message — ready, the
// subscription page's link, «My subscription» with its buttons — and sends
// no file.
func TestApprovalSendsTheLinkOnly(t *testing.T) {
	tg, fake, _, r := requestsAdminFixture(t)

	adminPress(tg, fmt.Sprintf("%s %d", requestCardRoute, r.Id))
	adminTap(t, tg, fake, "✅ Approve")
	v, err := (&SubUserService{}).Find("petrov")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(usersProtocols(v), "AWG") {
		t.Fatalf("the request defaults have no AWG client: %s", usersProtocols(v))
	}

	told := fake.sentTo(testApplicant)
	if len(told) != 1 {
		t.Fatalf("the applicant got %d messages: %q", len(told), told)
	}
	for _, want := range []string{"Your subscription is ready", pageLine(v.SubId), "My subscription</b> · petrov"} {
		if !strings.Contains(told[0], want) {
			t.Errorf("the applicant's message lacks %q:\n%s", want, told[0])
		}
	}
	if strings.Contains(told[0], "://"+v.SubId) || strings.Contains(told[0], "vless://") {
		t.Errorf("the applicant's message carries a config link:\n%s", told[0])
	}
	if _, labels, _ := fake.screenOf(t, testApplicant); strings.Join(labels, "|") != "🔗 Show subscription|📄 My configs|🔄 Refresh" {
		t.Errorf("the applicant's buttons: %q", labels)
	}
	if files := fake.filesSent(); len(files) != 0 {
		t.Errorf("files were sent: %q", files)
	}
}

// TestApprovalWithChangesSendsTheLinkOnly: «⚙️ Approve with changes» tells
// the applicant the same: the link, no file.
func TestApprovalWithChangesSendsTheLinkOnly(t *testing.T) {
	tg, fake, _, r := requestsAdminFixture(t)

	adminPress(tg, fmt.Sprintf("%s %d", requestCardRoute, r.Id))
	adminTap(t, tg, fake, "Approve with changes")
	adminTap(t, tg, fake, "Create")
	v, err := (&SubUserService{}).Find("petrov")
	if err != nil {
		t.Fatal(err)
	}
	told := fake.sentTo(testApplicant)
	if len(told) != 1 || !strings.Contains(told[0], "Your subscription is ready") || !strings.Contains(told[0], pageLine(v.SubId)) {
		t.Errorf("the applicant was told %q", told)
	}
	if files := fake.filesSent(); len(files) != 0 {
		t.Errorf("files were sent: %q", files)
	}
}

// TestStartInviteSendsTheLinkOnly: Start with the invite of a user with an
// xray and an AWG client shows the sender, in one message, that Telegram is
// linked, the subscription page's link and «My subscription»; no file.
// Pressing Start again says the same.
func TestStartInviteSendsTheLinkOnly(t *testing.T) {
	tg, fake := notifyBotFixture(t, testNotifyChannel)
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{1, 5}})
	inv, err := (&TgInviteService{}).Invite(ivan.SubId)
	if err != nil {
		t.Fatal(err)
	}

	for range 2 {
		fake.calls = nil
		startFrom(tg, petrov, "/start "+inv.Token)
		told := fake.sentTo(petrov.ID)
		if len(told) != 1 {
			t.Fatalf("the sender got %d messages: %q", len(told), told)
		}
		for _, want := range []string{"Telegram is linked to your subscription", pageLine(ivan.SubId), "My subscription</b> · ivan"} {
			if !strings.Contains(told[0], want) {
				t.Errorf("the sender's message lacks %q:\n%s", want, told[0])
			}
		}
		if files := fake.filesSent(); len(files) != 0 {
			t.Errorf("files were sent: %q", files)
		}
	}
}

// TestStartInviteRefusedSendsNoLink: a used-up invite names no user, so
// gives no link.
func TestStartInviteRefusedSendsNoLink(t *testing.T) {
	tg, fake := notifyBotFixture(t, testNotifyChannel)
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{1}})
	inv, err := (&TgInviteService{}).Invite(ivan.SubId)
	if err != nil {
		t.Fatal(err)
	}
	startFrom(tg, petrov, "/start "+inv.Token)

	fake.calls = nil
	other := petrov
	other.ID, other.Username = 778, "other"
	startFrom(tg, other, "/start "+inv.Token)
	if told := strings.Join(fake.sentTo(other.ID), "\n"); strings.Contains(told, "/sub/") || strings.Contains(told, ivan.SubId) {
		t.Errorf("a refused sender got the link: %q", told)
	}
}

// TestSendTheLinkToANewUserIsTextOnly: a user the admin just made with a
// Telegram and an AWG client — not yet seen by the link watcher — gets
// «📣 Send the link» as one text message: the link, «📱 My subscription»
// and that a new .conf waits there; no QR picture, no .conf file. The
// broadcast remembers the .conf as told: the next one does not repeat it.
func TestSendTheLinkToANewUserIsTextOnly(t *testing.T) {
	tg := usersBotFixture(t)
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", TgId: linkPerson, InboundIds: []int{1, 5}})
	fake, _ := withLinkTelegram(t)

	for i := range 2 {
		adminPress(tg, "usr_c "+ivan.SubId)
		linkTap(t, tg, fake, "📣 Send the link")
		linkTap(t, tg, fake, "Confirm")
		got := fake.to(linkPerson)
		if len(got) != i+1 {
			t.Fatalf("broadcast %d: ivan got %+v", i+1, got)
		}
		msg := got[i]
		if msg.method != "sendMessage" || msg.file != "" || !strings.Contains(msg.text, "http://localhost:2096/sub/"+ivan.SubId) {
			t.Errorf("broadcast %d: ivan got %+v", i+1, msg)
		}
		msg.button(t, "📱 My subscription")
		if told := strings.Contains(msg.text, "There is a new .conf"); told != (i == 0) {
			t.Errorf("broadcast %d: the .conf line %v in %q", i+1, told, msg.text)
		}
	}
	for _, c := range fake.calls {
		if c.method == "sendPhoto" || c.method == "sendDocument" || c.method == "sendMediaGroup" {
			t.Errorf("a file was sent: %+v", c)
		}
	}
}
