package service

import (
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/mymmrac/telego"
)

// Buttons of users and clients with long names (#199): Telegram takes 64
// bytes of callback data, so longer data goes out as a hash (encodeQuery).
// Every press is decoded before it is routed and before the access list
// looks at it, for an admin and for a client alike.

// longName is a user name, and longSubId a subId, long enough that every
// button naming them is hashed.
var (
	longName  = strings.Repeat("l", 60)
	longSubId = strings.Repeat("s", 60)
)

// longClientUser is a user of usersTestChat, no admin, with a long name and
// subId: a VLESS client (inbound 1) whose email is long, and an AWG one.
func longClientUser(t *testing.T) (*SubUserView, string) {
	t.Helper()
	v := mustCreateUser(t, SubUserCreate{Name: longName, SubId: longSubId, TgId: usersTestChat, InboundIds: []int{1, 5}})
	email := clientByName(t, v, longName+"-NL-Amsterdam-1").Name
	return v, email
}

// subLinksFunc answers a request as http.RoundTripper.
type subLinksFunc func(*http.Request) (*http.Response, error)

func (f subLinksFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// withSubLinks points the bot's HTTP client, which fetches a subscription's
// links, at a subscription server that serves one VLESS link for any subId.
func withSubLinks(t *testing.T) {
	t.Helper()
	prev := optimizedHTTPClient
	optimizedHTTPClient = &http.Client{Transport: subLinksFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Request: r,
			Body: io.NopCloser(strings.NewReader("vless://link-of-" + r.URL.Path + "\n"))}, nil
	})}
	t.Cleanup(func() { optimizedHTTPClient = prev })
}

// hashedButton fails unless the button labelled label on message id goes
// out as a hash: the case under test.
func (f *screenTelegram) hashedButton(t *testing.T, id int, label string) {
	t.Helper()
	m := f.messages[id]
	if data := m.data[labelIndex(t, m, label)]; !hashStorage.IsMD5(data) {
		t.Fatalf("%q carries %q: not hashed, the test tests nothing", label, data)
	}
}

// TestClientLongNamesButtonsWork: a client whose subId and email are long
// gets every button of their screens working: «Show subscription», «My
// configs», the .conf of a tunnel client, «Links and QR codes», Back and
// Refresh.
func TestClientLongNamesButtonsWork(t *testing.T) {
	tg := usersBotFixture(t)
	longClientUser(t)
	withSubLinks(t)
	fake := withScreenTelegram(t)

	clientCommand(tg, "/start")
	if m := fake.clientScreen(t); !strings.Contains(m.text, "My subscription</b> · "+longName) {
		t.Fatalf("/start: %q", m.text)
	}

	fake.hashedButton(t, 1, "Show subscription")
	fake.clientPress(t, tg, 1, "Show subscription")
	if m := fake.messages[1]; !strings.Contains(m.text, "/sub/"+longSubId) || fake.documents() != 1 {
		t.Fatalf("Show subscription: %q, calls %q", m.text, fake.calls)
	}
	fake.clientPress(t, tg, 1, "Back")

	fake.hashedButton(t, 1, "My configs")
	fake.clientPress(t, tg, 1, "My configs")
	if m := fake.messages[1]; !strings.Contains(m.text, "My configs</b> · "+longName) || !strings.Contains(m.text, "AWG · "+longName+"-awg") {
		t.Fatalf("My configs: %q", m.text)
	}

	before := fake.documents()
	fake.clientPress(t, tg, 1, ".conf and QR")
	if got := fake.documents() - before; got != 2 {
		t.Errorf("the .conf button sent %d files, calls %q", got, fake.calls)
	}

	fake.hashedButton(t, 1, "Links and QR codes")
	sent := fake.next
	fake.clientPress(t, tg, 1, "Links and QR codes")
	if fake.next == sent || !strings.Contains(fake.messages[sent+1].text, "vless://link-of-/sub/"+longSubId) {
		t.Errorf("Links and QR codes sent no links: calls %q", fake.calls)
	}

	fake.clientPress(t, tg, 1, "Back")
	fake.clientPress(t, tg, 1, "Refresh")
	if m := fake.messages[1]; !strings.Contains(m.text, "My subscription</b> · "+longName) {
		t.Errorf("Refresh: %q", m.text)
	}
}

// TestClientLongNamesPickAmongSeveral: the pick among several users (legacy
// data) opens a user with a long subId.
func TestClientLongNamesPickAmongSeveral(t *testing.T) {
	tg := usersBotFixture(t)
	longClientUser(t)
	legacyUserOf(t, SubUserCreate{Name: "petr", InboundIds: []int{2}}, usersTestChat)
	fake := withScreenTelegram(t)

	clientCommand(tg, "/start")
	fake.hashedButton(t, 1, longName)
	fake.clientPress(t, tg, 1, longName)
	if m := fake.messages[1]; !strings.Contains(m.text, "My subscription</b> · "+longName) {
		t.Fatalf("the pick: %q", m.text)
	}
	fake.clientPress(t, tg, 1, "Show subscription")
	if m := fake.messages[1]; !strings.Contains(m.text, "/sub/"+longSubId) {
		t.Errorf("Show subscription: %q", m.text)
	}
}

// TestClientLongNamesOldButtonsWork: the buttons of the old client menu that
// name a client with a long email — hashed, as the old menu sent them —
// open the client's screens.
func TestClientLongNamesOldButtonsWork(t *testing.T) {
	tg := usersBotFixture(t)
	_, email := longClientUser(t)
	fake := withScreenTelegram(t)

	old := []struct{ label, data, want string }{
		{"Subscription", "client_sub_links " + email, "/sub/" + longSubId},
		{"Individual links", "client_individual_links " + email, "My configs</b> · " + longName},
		{"QR Code", "client_qr_links " + email, "My configs</b> · " + longName},
		{"Usage", "client_get_usage " + email, "My configs</b> · " + longName},
	}
	fake.next = 1
	fake.messages[1] = &screenMessage{}
	for _, b := range old {
		fake.messages[1].labels = append(fake.messages[1].labels, b.label)
		fake.messages[1].data = append(fake.messages[1].data, tg.encodeQuery(b.data))
	}
	for _, b := range old {
		fake.hashedButton(t, 1, b.label)
		fake.clientPress(t, tg, 1, b.label)
		if m := fake.clientScreen(t); !strings.Contains(m.text, b.want) {
			t.Errorf("%s: %q", b.label, m.text)
		}
	}
}

// TestClientHashNamingAnotherUserIsRefused: the access list looks at the
// decoded data, so a hash that stands for another user's route, or for an
// admin's, is refused as that route would be, and changes nothing.
func TestClientHashNamingAnotherUserIsRefused(t *testing.T) {
	tg := accessBotFixture(t)
	other := mustCreateUser(t, SubUserCreate{Name: longName, SubId: longSubId, InboundIds: []int{1}})
	email := clientByName(t, other, longName+"-NL-Amsterdam-1").Name
	fake := withFakeTelegram(t)
	before := botState(t)

	for _, data := range []string{
		mysubSubRoute + " " + longSubId, mysubConfigsRoute + " " + longSubId, mysubLinksAction + " " + longSubId,
		mysubUserRoute + " " + longSubId, "client_sub_links " + email, "client_qr_links " + email,
		"client_get_usage " + email, "usr_c " + longSubId, "usr_del " + longSubId, "reset_traffic_c " + email,
		"toggle_enable_c " + email,
	} {
		hash := tg.encodeQuery(data)
		if !hashStorage.IsMD5(hash) {
			t.Fatalf("%q is not hashed", data)
		}
		if tg.clientMayPress(&telego.CallbackQuery{From: telego.User{ID: usersTestChat}, Data: hash}) {
			t.Errorf("%q let through", data)
		}
		fake.calls = nil
		nonAdminPress(tg, hash)
		if len(fake.calls) != 1 || fake.calls[0].method != "answerCallbackQuery" || fake.calls[0].params["text"] != "❗ No result!" {
			t.Errorf("%q: the bot said\n%s", data, fake.texts())
		}
	}
	if after := botState(t); after != before {
		t.Fatalf("hashed routes changed the bot's state:\n before %s\n after  %s", before, after)
	}
}

// TestClientLostHashShowsTheScreenAfresh: a hashed button outlives its hash
// (the storage keeps them 20 minutes, a restart none). Pressed then, it shows
// the client's view afresh, with buttons that work again, as an admin's
// screen does — not «No result» for good.
func TestClientLostHashShowsTheScreenAfresh(t *testing.T) {
	tg := usersBotFixture(t)
	longClientUser(t)
	fake := withScreenTelegram(t)

	clientCommand(tg, "/start")
	fake.clientPress(t, tg, 1, "My configs")
	hashStorage.Reset()
	fake.calls = nil
	fake.clientPress(t, tg, 1, "Links and QR codes")
	if m := fake.messages[1]; !strings.Contains(m.text, "My configs</b> · "+longName) || !slices.Contains(fake.calls, "editMessageText #1") {
		t.Fatalf("the lost hash: %q, calls %q", m.text, fake.calls)
	}
	fake.clientPress(t, tg, 1, "Back")
	fake.clientPress(t, tg, 1, "Show subscription")
	if m := fake.messages[1]; !strings.Contains(m.text, "/sub/"+longSubId) {
		t.Errorf("the refreshed buttons: %q", m.text)
	}

	// The view shown afresh goes through the client's routes: a screen whose
	// route names someone else's (an admin's screen before) shows the
	// client's own main menu.
	fake.clientPress(t, tg, 1, "Back")
	botScreens.of(usersTestChat).route = "usr_c s-other"
	hashStorage.Reset()
	before := botState(t)
	fake.clientPress(t, tg, 1, "My configs")
	if m := fake.messages[1]; !strings.Contains(m.text, "My subscription</b> · "+longName) || strings.Contains(m.text, "other") {
		t.Errorf("a lost hash on a screen of someone else's: %q", m.text)
	}
	if after := botState(t); after != before {
		t.Fatalf("a lost hash changed the bot's state:\n before %s\n after  %s", before, after)
	}
}

// TestAdminLongNamesButtonsWork: an admin's cards of a user and an xray
// client with long names work through their hashed buttons, and a lost hash
// shows the card afresh.
func TestAdminLongNamesButtonsWork(t *testing.T) {
	tg := usersBotFixture(t)
	v := mustCreateUser(t, SubUserCreate{Name: longName, SubId: longSubId, InboundIds: []int{1}})
	email := clientByName(t, v, longName+"-NL-Amsterdam-1").Name
	fake := withScreenTelegram(t)

	adminCommand(tg, "/start")
	fake.press(t, tg, 1, "👥 Users")
	fake.hashedButton(t, 1, longName)
	fake.press(t, tg, 1, longName)
	if m := fake.messages[1]; !strings.Contains(m.text, "<b>"+longName+"</b>") {
		t.Fatalf("the user card: %q", m.text)
	}
	fake.hashedButton(t, 1, "Show subscription")
	fake.press(t, tg, 1, "Show subscription")
	if m := fake.messages[1]; !strings.Contains(m.text, "/sub/"+longSubId) {
		t.Fatalf("Show subscription: %q", m.text)
	}
	fake.press(t, tg, 1, "Back")

	// The xray client's card, and its traffic limit typed on the keypad.
	fake.press(t, tg, 1, "VLESS · "+email)
	if m := fake.messages[1]; !strings.Contains(m.text, email) {
		t.Fatalf("the client card: %q", m.text)
	}
	for _, label := range []string{"Traffic Limit", "Custom", "1", "5", "Confirm adding: 15"} {
		fake.hashedButton(t, 1, label)
		fake.press(t, tg, 1, label)
	}
	if got, _ := (&SubUserService{}).Get(longSubId); clientByName(t, got, email).TotalGB != 15<<30 {
		t.Errorf("the limit was not saved: %+v", clientByName(t, got, email))
	}

	hashStorage.Reset()
	fake.press(t, tg, 1, "IP Log")
	if m := fake.messages[1]; !strings.Contains(m.text, email) || !strings.Contains(strings.Join(m.labels, "|"), "IP Log") {
		t.Errorf("the lost hash: %q %q", m.text, m.labels)
	}
	fake.press(t, tg, 1, "IP Log")
	if m := fake.messages[1]; !strings.Contains(strings.Join(m.labels, "|"), "Clear IPs") {
		t.Errorf("the refreshed buttons: %q %q", m.text, m.labels)
	}
}
