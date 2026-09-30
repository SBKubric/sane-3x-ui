package service

import (
	"fmt"
	"html"
	"strconv"
	"strings"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// The «➕ New user» dialogue (#193, docs/spec/users.md §10) on the admin's
// screen, as the navigation prototype of #185 lays it out:
//
//  1. the name, typed: a taken or reserved one is refused on the same step;
//  2. the contact email, typed, or «Skip»: not the xray email, which stays
//     generated;
//  3. the protocols: the inbounds a user can have, the enabled ones ticked;
//  4. the traffic per protocol and the expiry: presets or «Custom value»;
//  5. the Telegram id, typed, or «Later»;
//
// then the review, whose «✅ Create» makes the user through
// SubUserService.Create (all or nothing) and shows its card, and whose
// «◀ Change» goes back to the protocols. «✖ Cancel» on every step drops the
// draft and shows the main menu. The draft is the chat's (usersSessions), so
// two admins never share one (#200); the typed texts go through the chat
// state usersStateNewUser, and the screen deletes them.

// usersStateNewUser is the chat state while a step of the dialogue waits for
// a text: the draft's step says which.
const usersStateNewUser = "usr_nu"

// The dialogue's steps.
const (
	newUserStepName      = "name"
	newUserStepEmail     = "email"
	newUserStepProtocols = "protos"
	newUserStepLimits    = "limits"
	newUserStepTraffic   = "gb"   // a custom traffic limit, typed
	newUserStepDays      = "days" // a custom expiry, typed
	newUserStepTelegram  = "tg"
	newUserStepReview    = "review"
	newUserStepLink      = "link" // Create asks to link an existing AWG client
)

// newUserSteps numbers the steps that ask for something, as the screen
// counts them.
var newUserSteps = map[string]int{
	newUserStepName: 1, newUserStepEmail: 2, newUserStepProtocols: 3,
	newUserStepLimits: 4, newUserStepTraffic: 4, newUserStepDays: 4, newUserStepTelegram: 5,
}

const newUserStepCount = 5

// The presets of the limits step, as the prototype has them; 0 = unlimited.
var (
	newUserTrafficPresets = []int{50, 100, 0}
	newUserDaysPresets    = []int{30, 90, 0}
)

// newUserMaxNumber bounds a typed limit: GB or days.
const newUserMaxNumber = 999999

// usersDraft is the user being created: the step shown, the inbounds offered
// and ticked, and what the steps collected.
type usersDraft struct {
	step     string
	inbounds []SubUserInbound
	selected map[int]bool
	name     string
	email    string
	gb       int   // traffic per protocol in GB, 0 = unlimited
	days     int   // expiry in days after first use, 0 = none
	tgId     int64 // 0 = later
	// awgClient is the AmneziaWG client Create offered to link.
	awgClient string
}

// request is the draft as the service takes it, the inbounds in the order
// they were offered.
func (d *usersDraft) request(linkExisting bool) SubUserCreate {
	req := SubUserCreate{Name: d.name, ContactEmail: d.email, TgId: d.tgId, LinkExisting: linkExisting,
		SubUserParams: SubUserParams{TotalGB: int64(d.gb) << 30, ExpiryTime: -int64(d.days) * 86400000}}
	for _, ib := range d.inbounds {
		if d.selected[ib.Id] {
			req.InboundIds = append(req.InboundIds, ib.Id)
		}
	}
	return req
}

// newUserCallback runs the dialogue's buttons; false for data that is not
// theirs.
func (t *Tgbot) newUserCallback(chatId int64, data string) (usersReply, bool) {
	action, args, _ := strings.Cut(data, " ")
	n, _ := strconv.Atoi(args)
	switch action {
	case "add_client": // «➕ New user» in the main menu
		return t.newUserStart(chatId), true
	case "nu_x":
		usersSessions.with(chatId, func(s *usersSession) { s.draft = nil })
		menu := t.screenMainMenu().usersReply
		menu.toast = t.I18nBot("tgbot.newUser.cancelled")
		return menu, true
	case "nu_skip": // no contact email
		return t.newUserEdit(chatId, func(d *usersDraft) string {
			d.email = ""
			d.step = newUserStepProtocols
			return ""
		}), true
	case "nu_t":
		return t.newUserToggle(chatId, n), true
	case "nu_go":
		return t.newUserGo(chatId, args), true
	case "nu_gb", "nu_dy":
		return t.newUserEdit(chatId, func(d *usersDraft) string {
			n = max(0, min(n, newUserMaxNumber))
			if action == "nu_gb" {
				d.gb = n
			} else {
				d.days = n
			}
			d.step = newUserStepLimits
			return ""
		}), true
	case "nu_gbc", "nu_dyc": // «Custom value»
		return t.newUserEdit(chatId, func(d *usersDraft) string {
			d.step = newUserStepTraffic
			if action == "nu_dyc" {
				d.step = newUserStepDays
			}
			return ""
		}), true
	case "nu_ok":
		return t.newUserCreate(chatId, false), true
	case "nu_lnk":
		return t.newUserCreate(chatId, true), true
	}
	return usersReply{}, false
}

// newUserStart opens a new draft on the name step, the enabled inbounds
// ticked and the prototype's limits preset.
func (t *Tgbot) newUserStart(chatId int64) usersReply {
	inbounds, err := (&SubUserService{}).Inbounds()
	if err != nil {
		return t.usersError(err)
	}
	if len(inbounds) == 0 {
		return usersReply{text: t.I18nBot("tgbot.answers.getInboundsFailed")}
	}
	d := &usersDraft{step: newUserStepName, inbounds: inbounds, selected: map[int]bool{},
		gb: newUserTrafficPresets[0], days: newUserDaysPresets[0]}
	for _, ib := range inbounds {
		d.selected[ib.Id] = ib.Enable
	}
	usersSessions.with(chatId, func(s *usersSession) { s.draft = d })
	return t.newUserView(chatId, d, "")
}

// newUserEdit changes the chat's draft and shows it; fn returns the error
// the step shows, "" for none. A chat without a draft (a restart, or a
// finished dialogue) is told it expired.
func (t *Tgbot) newUserEdit(chatId int64, fn func(*usersDraft) string) usersReply {
	var reply usersReply
	found := false
	usersSessions.with(chatId, func(s *usersSession) {
		if s.draft == nil {
			return
		}
		found = true
		errText := fn(s.draft)
		reply = t.newUserView(chatId, s.draft, errText)
	})
	if !found {
		return t.usersExpired()
	}
	return reply
}

// newUserToggle ticks or unticks an inbound on the protocols step: only the
// buttons change.
func (t *Tgbot) newUserToggle(chatId int64, inboundId int) usersReply {
	var kb *telego.InlineKeyboardMarkup
	usersSessions.with(chatId, func(s *usersSession) {
		if d := s.draft; d != nil && d.step == newUserStepProtocols {
			d.selected[inboundId] = !d.selected[inboundId]
			kb = t.newUserView(chatId, d, "").keyboard
		}
	})
	if kb == nil { // no draft, or another step: show what there is
		return t.newUserEdit(chatId, func(*usersDraft) string { return "" })
	}
	return usersReply{keyboard: kb}
}

// newUserGo moves the draft to a step by a button. Every step needs the
// name; the limits and after them a protocol ticked, else the protocols stay
// with a toast.
func (t *Tgbot) newUserGo(chatId int64, step string) usersReply {
	switch step {
	case newUserStepProtocols, newUserStepLimits, newUserStepTelegram, newUserStepReview:
	default:
		return t.newUserEdit(chatId, func(*usersDraft) string { return "" })
	}
	var toast string
	reply := t.newUserEdit(chatId, func(d *usersDraft) string {
		switch {
		case d.name == "":
			d.step = newUserStepName
		case step != newUserStepProtocols && len(d.request(false).InboundIds) == 0:
			toast = t.I18nBot("tgbot.users.noneSelected")
			d.step = newUserStepProtocols
		default:
			d.step = step
		}
		return ""
	})
	if toast != "" {
		return usersReply{toast: toast}
	}
	return reply
}

// newUserText takes the text a step waits for. The checks that read the
// users run before the draft is locked.
func (t *Tgbot) newUserText(chatId int64, text string) usersReply {
	text = strings.TrimSpace(text)
	var step string
	usersSessions.with(chatId, func(s *usersSession) {
		if s.draft != nil {
			step = s.draft.step
		}
	})
	var errText string
	var number int
	var email string
	var tgId int64
	switch step {
	case newUserStepName:
		if err := (&SubUserService{}).CheckNewName(text); err != nil {
			errText = err.Error()
		}
	case newUserStepEmail:
		var err error
		if email, err = CheckContactEmail(text); err != nil || email == "" {
			errText = t.I18nBot("tgbot.newUser.badEmail", "Text=="+text)
		}
	case newUserStepTraffic, newUserStepDays:
		var err error
		if number, err = strconv.Atoi(text); err != nil || number < 0 || number > newUserMaxNumber {
			errText = t.I18nBot("tgbot.newUser.badNumber", "Text=="+text)
		}
	case newUserStepTelegram:
		// A typed numeric id is the minimal way to bind Telegram; the other
		// ways (a forwarded message, a shared contact, an invite link) are
		// #187's.
		var err error
		if tgId, err = strconv.ParseInt(text, 10, 64); err != nil || tgId <= 0 {
			errText = t.I18nBot("tgbot.newUser.badTgId", "Text=="+text)
		} else if err := (&SubUserService{}).CheckNewTgId(tgId); err != nil {
			errText = err.Error()
		}
	}
	return t.newUserEdit(chatId, func(d *usersDraft) string {
		if d.step != step || errText != "" {
			return errText
		}
		switch step {
		case newUserStepName:
			d.name, d.step = text, newUserStepEmail
		case newUserStepEmail:
			d.email, d.step = email, newUserStepProtocols
		case newUserStepTraffic:
			d.gb, d.step = number, newUserStepLimits
		case newUserStepDays:
			d.days, d.step = number, newUserStepLimits
		case newUserStepTelegram:
			d.tgId, d.step = tgId, newUserStepReview
		}
		return ""
	})
}

// newUserCreate is «✅ Create»: the user, all or nothing, and its card. An
// AmneziaWG client that already has the new client's name and no
// subscription is offered for linking first; a refusal stays on the review.
func (t *Tgbot) newUserCreate(chatId int64, linkExisting bool) usersReply {
	var req *SubUserCreate
	usersSessions.with(chatId, func(s *usersSession) {
		if s.draft != nil && (s.draft.step == newUserStepReview || s.draft.step == newUserStepLink) {
			r := s.draft.request(linkExisting)
			req = &r
		}
	})
	if req == nil {
		return t.usersExpired()
	}
	v, err := (&SubUserService{}).Create(*req)
	if conflict, ok := awgLinkable(err); ok {
		return t.newUserEdit(chatId, func(d *usersDraft) string {
			d.step, d.awgClient = newUserStepLink, conflict.Client
			return ""
		})
	}
	if err != nil {
		reply := t.newUserEdit(chatId, func(d *usersDraft) string {
			d.step = newUserStepReview
			return err.Error()
		})
		reply.toast = t.I18nBot("tgbot.answers.errorOperation")
		return reply
	}
	usersSessions.with(chatId, func(s *usersSession) { s.draft = nil })
	reply := t.usersCardOf(v)
	subURL, _ := t.subscriptionURLs(v.SubId)
	reply.toast = t.I18nBot("tgbot.users.created", "Name=="+v.Name)
	reply.text = t.I18nBot("tgbot.users.created", "Name=="+html.EscapeString(v.Name)) + "\r\n" +
		t.I18nBot("tgbot.users.subscription", "Url=="+html.EscapeString(subURL)) + "\r\n" + reply.text
	// Back from the new card leads to the main menu, as after the prototype's
	// create: the dialogue is gone.
	reply.root = true
	return reply
}

// --- the view ------------------------------------------------------------------

// newUserView shows the draft's step, with errText under it; a step that
// waits for a text makes the chat wait.
func (t *Tgbot) newUserView(chatId int64, d *usersDraft, errText string) usersReply {
	var b strings.Builder
	if n := newUserSteps[d.step]; n > 0 {
		title := t.I18nBot("tgbot.newUser.title")
		if d.name != "" {
			title = "<b>" + html.EscapeString(d.name) + "</b>"
		}
		b.WriteString(t.I18nBot("tgbot.newUser.step", "Title=="+title, "Step=="+strconv.Itoa(n),
			"Count=="+strconv.Itoa(newUserStepCount)))
	}
	button := func(label, data string) telego.InlineKeyboardButton {
		return tu.InlineKeyboardButton(label).WithCallbackData(data)
	}
	key := func(k, data string) telego.InlineKeyboardButton { return button(t.I18nBot(k), data) }
	cancel := key("tgbot.newUser.cancel", "nu_x")
	var rows [][]telego.InlineKeyboardButton
	waits := false
	switch d.step {
	case newUserStepName:
		b.WriteString(t.I18nBot("tgbot.users.namePrompt"))
		waits = true
	case newUserStepEmail:
		b.WriteString(t.I18nBot("tgbot.newUser.emailPrompt"))
		rows = append(rows, tu.InlineKeyboardRow(key("tgbot.newUser.skip", "nu_skip")))
		waits = true
	case newUserStepProtocols:
		b.WriteString(t.I18nBot("tgbot.users.chooseProtocols"))
		for _, ib := range d.inbounds {
			mark := "⬜"
			if d.selected[ib.Id] {
				mark = "✅"
			}
			rows = append(rows, tu.InlineKeyboardRow(button(fmt.Sprintf("%s %s · %s", mark, protocolLabel(ib.Protocol), ib.Remark),
				fmt.Sprintf("nu_t %d", ib.Id))))
		}
		rows = append(rows, tu.InlineKeyboardRow(key("tgbot.users.next", "nu_go "+newUserStepLimits)))
	case newUserStepLimits:
		b.WriteString(t.I18nBot("tgbot.newUser.limitsPrompt"))
		b.WriteString(t.newUserSummary(d))
		preset := func(n, chosen int, label, data string) telego.InlineKeyboardButton {
			if n == chosen {
				label = "● " + label
			}
			return button(label, fmt.Sprintf("%s %d", data, n))
		}
		var traffic, days []telego.InlineKeyboardButton
		for _, gb := range newUserTrafficPresets {
			traffic = append(traffic, preset(gb, d.gb, t.newUserTraffic(gb), "nu_gb"))
		}
		for _, n := range newUserDaysPresets {
			days = append(days, preset(n, d.days, t.newUserDays(n), "nu_dy"))
		}
		rows = append(rows,
			append(traffic, key("tgbot.newUser.custom", "nu_gbc")),
			append(days, key("tgbot.newUser.custom", "nu_dyc")),
			tu.InlineKeyboardRow(key("tgbot.users.next", "nu_go "+newUserStepTelegram)))
	case newUserStepTraffic:
		b.WriteString(t.I18nBot("tgbot.newUser.trafficPrompt"))
		rows = append(rows, tu.InlineKeyboardRow(key("tgbot.newUser.change", "nu_go "+newUserStepLimits)))
		waits = true
	case newUserStepDays:
		b.WriteString(t.I18nBot("tgbot.newUser.daysPrompt"))
		rows = append(rows, tu.InlineKeyboardRow(key("tgbot.newUser.change", "nu_go "+newUserStepLimits)))
		waits = true
	case newUserStepTelegram:
		b.WriteString(t.I18nBot("tgbot.newUser.tgPrompt"))
		b.WriteString(t.newUserSummary(d))
		rows = append(rows, tu.InlineKeyboardRow(key("tgbot.newUser.later", "nu_go "+newUserStepReview)))
		waits = true
	case newUserStepReview:
		b.WriteString(t.I18nBot("tgbot.newUser.review"))
		b.WriteString(t.newUserSummary(d))
		rows = append(rows, tu.InlineKeyboardRow(key("tgbot.users.create", "nu_ok")),
			tu.InlineKeyboardRow(key("tgbot.newUser.change", "nu_go "+newUserStepProtocols)))
	case newUserStepLink:
		b.WriteString(t.I18nBot("tgbot.users.linkAwg", "Client=="+html.EscapeString(d.awgClient)))
		rows = append(rows, tu.InlineKeyboardRow(key("tgbot.users.link", "nu_lnk")),
			tu.InlineKeyboardRow(key("tgbot.newUser.change", "nu_go "+newUserStepProtocols)))
	}
	if errText != "" {
		b.WriteString(t.I18nBot("tgbot.newUser.error", "Error=="+html.EscapeString(errText)))
	}
	rows = append(rows, tu.InlineKeyboardRow(cancel))
	if waits {
		userStates[chatId] = usersStateNewUser
	}
	return usersReply{text: b.String(), keyboard: tu.InlineKeyboard(rows...)}
}

// newUserSummary is what the draft holds so far.
func (t *Tgbot) newUserSummary(d *usersDraft) string {
	var protocols []string
	for _, ib := range d.inbounds {
		if d.selected[ib.Id] {
			protocols = append(protocols, fmt.Sprintf("%s (%s)", ib.Remark, protocolLabel(ib.Protocol)))
		}
	}
	none := t.I18nBot("tgbot.newUser.none")
	email, tg, protos := none, t.I18nBot("tgbot.newUser.tgLater"), none
	if d.email != "" {
		email = d.email
	}
	if d.tgId != 0 {
		tg = strconv.FormatInt(d.tgId, 10)
	}
	if len(protocols) > 0 {
		protos = strings.Join(protocols, ", ")
	}
	return t.I18nBot("tgbot.newUser.summary",
		"Name=="+html.EscapeString(d.name),
		"Email=="+html.EscapeString(email),
		"Protocols=="+html.EscapeString(protos),
		"Traffic=="+t.newUserTraffic(d.gb),
		"Exp=="+t.newUserDays(d.days),
		"Tg=="+tg)
}

// newUserTraffic words a traffic limit per protocol: 50 GB, or ∞.
func (t *Tgbot) newUserTraffic(gb int) string {
	if gb == 0 {
		return "∞"
	}
	return strconv.Itoa(gb) + " " + t.I18nBot("tgbot.screen.gb")
}

// newUserDays words an expiry in days after first use: 30 d, or ∞.
func (t *Tgbot) newUserDays(days int) string {
	if days == 0 {
		return "∞"
	}
	return t.I18nBot("tgbot.newUser.days", "Days=="+strconv.Itoa(days))
}
