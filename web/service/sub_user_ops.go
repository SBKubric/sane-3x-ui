package service

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/coinman-dev/3ax-ui/v2/util/common"
	"github.com/coinman-dev/3ax-ui/v2/util/random"
)

// SubUserParams are the limits a new client gets. They are per client: a
// user with two clients of 50 GB each may use up to 100 GB.
type SubUserParams struct {
	TotalGB    int64 `json:"totalGB"`    // bytes, 0 = unlimited
	ExpiryTime int64 `json:"expiryTime"` // ms; negative = that long after first use; 0 = never
	LimitIp    int   `json:"limitIp"`
	Reset      int   `json:"reset"` // auto-renew period in days, 0 = off
}

// SubUserCreate is a new user with one client in each of InboundIds.
type SubUserCreate struct {
	Name       string `json:"name"`
	SubId      string `json:"subId"` // "" = generate one
	TgId       int64  `json:"tgId"`
	Comment    string `json:"comment"`
	InboundIds []int  `json:"inboundIds"`
	SubUserParams
	// LinkExisting takes over an AmneziaWG client that already has the
	// generated name and no subscription, instead of refusing the create
	// with a SubUserConflictAwgLinkable conflict.
	LinkExisting bool `json:"linkExisting"`

	// ContactEmail is the user's mail address for contact, optional: not an
	// xray email (CheckContactEmail).
	ContactEmail string `json:"contactEmail"`

	// TgNick is the @nick of a Telegram account the bot has seen, instead of
	// TgId (#219); "" for none.
	TgNick string `json:"tgNick"`
}

// SubUserInbound is an inbound a user can have a client in.
type SubUserInbound struct {
	Id       int    `json:"id"`
	Remark   string `json:"remark"`
	Protocol string `json:"protocol"`
	Enable   bool   `json:"enable"`
}

// SubUserConflictAwgLinkable: an AmneziaWG client with the name the user's
// new client would get exists and has no subscription. The caller asks the
// operator and repeats the request with LinkExisting.
const SubUserConflictAwgLinkable = "awg_linkable"

// SubUserConflict is a refusal the caller can resolve by asking the operator.
type SubUserConflict struct {
	Code   string `json:"code"`
	Client string `json:"client"`
	// The Telegram refusals (#219, sub_user_telegram_bind.go): the id, and
	// the user it belongs to for SubUserConflictTgOwned.
	TgId       int64  `json:"tgId,omitempty"`
	Owner      string `json:"owner,omitempty"`
	OwnerSubId string `json:"ownerSubId,omitempty"`
	msg        string
}

func (e *SubUserConflict) Error() string { return e.msg }

// subUserMaxName bounds a user name; client names add the inbound's remark.
const subUserMaxName = 64

// userProtocols are the inbound protocols a user's client can be created in
// (docs/spec/users.md): the four xray protocols of the subscription, and
// AmneziaWG. Native WireGuard and MTProto are out.
var userProtocols = map[model.Protocol]bool{
	model.VLESS: true, model.VMESS: true, model.Trojan: true, model.Shadowsocks: true, model.AmneziaWG: true,
}

// SubUserClientName is the name of a user's client in an inbound:
// <user>-<remark>, cut down to letters, digits and . _ @ -, with every other
// run of characters written as one '-'.
func SubUserClientName(user, remark string) string {
	var b strings.Builder
	dash := false
	for _, r := range user + "-" + remark {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '@' {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash {
			b.WriteRune('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-.")
}

// List returns every user, technical ones first.
func (s *SubUserService) List() ([]*SubUserView, error) {
	idx, err := s.synced()
	if err != nil {
		return nil, err
	}
	users := idx.sortedUsers()
	out := make([]*SubUserView, 0, len(users))
	for _, u := range users {
		out = append(out, idx.view(u))
	}
	return out, nil
}

// Find returns the one user query names: a user name (ignoring case), a
// subId, or the name of one of its clients. A client name shared by clients
// of two users (a legacy duplicate) is refused as ambiguous.
func (s *SubUserService) Find(query string) (*SubUserView, error) {
	idx, err := s.synced()
	if err != nil {
		return nil, err
	}
	q := strings.TrimSpace(query)
	if u := idx.userByName(q); u != nil {
		return idx.view(u), nil
	}
	if u := idx.users[q]; u != nil {
		return idx.view(u), nil
	}
	var owners []string
	for _, c := range idx.clients {
		if c.Name != "" && strings.EqualFold(c.Name, q) && !slices.Contains(owners, c.owner) {
			owners = append(owners, c.owner)
		}
	}
	switch len(owners) {
	case 0:
		return nil, common.NewErrorf("no user, subId or client named %q", q)
	case 1:
		if u := idx.users[owners[0]]; u != nil {
			return idx.view(u), nil
		}
		return nil, common.NewErrorf("no user for the subscription of client %q", q)
	default:
		names := make([]string, 0, len(owners))
		for _, o := range owners {
			if u := idx.users[o]; u != nil {
				names = append(names, u.Name)
			}
		}
		return nil, common.NewErrorf("client name %q is shared by users %s", q, strings.Join(names, ", "))
	}
}

// Inbounds lists the inbounds a user can have a client in.
func (s *SubUserService) Inbounds() ([]SubUserInbound, error) {
	var inbounds []model.Inbound
	if err := database.GetDB().Order("id asc").Find(&inbounds).Error; err != nil {
		return nil, err
	}
	out := []SubUserInbound{}
	for _, ib := range inbounds {
		if userProtocols[ib.Protocol] {
			out = append(out, SubUserInbound{Id: ib.Id, Remark: ib.Remark, Protocol: string(ib.Protocol), Enable: ib.Enable})
		}
	}
	return out, nil
}

// Create makes a user with a client in each inbound, all or nothing: a
// failure removes the clients made so far and the user.
func (s *SubUserService) Create(req SubUserCreate) (*SubUserView, error) {
	subUserMu.Lock()
	defer subUserMu.Unlock()
	idx, err := s.syncLocked()
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	if err := idx.checkNewUserName(name); err != nil {
		return nil, err
	}
	contactEmail, err := CheckContactEmail(req.ContactEmail)
	if err != nil {
		return nil, err
	}
	if req.TgId, err = telegramOf(req.TgId, req.TgNick); err != nil {
		return nil, err
	}
	if err := idx.checkNewTgId(req.TgId); err != nil {
		return nil, err
	}
	subId := strings.TrimSpace(req.SubId)
	if subId == "" {
		subId = idx.freeSubId()
	} else if err := idx.checkNewSubId(subId); err != nil {
		return nil, err
	}
	u := &model.SubUser{SubId: subId, Name: name, TgId: req.TgId, Comment: req.Comment, ContactEmail: contactEmail}
	plans, err := idx.planClients(u, req.InboundIds, req.LinkExisting)
	if err != nil {
		return nil, err
	}

	db := database.GetDB()
	if err := db.Create(u).Error; err != nil {
		return nil, err
	}
	p := clientParams{SubUserParams: req.SubUserParams, tgId: req.TgId, enable: true}
	if err := s.applyPlans(idx, u, plans, p); err != nil {
		if delErr := db.Delete(&model.SubUser{}, "sub_id = ?", u.SubId).Error; delErr != nil {
			logger.Warning("users: could not remove the user of a failed create:", delErr)
		}
		return nil, err
	}
	return s.savedView(u.SubId)
}

// AddProtocol gives the user a client in one more inbound, with the limits
// of its xray client, else of its AmneziaWG client.
func (s *SubUserService) AddProtocol(key string, inboundId int, linkExisting bool) (*SubUserView, error) {
	subUserMu.Lock()
	defer subUserMu.Unlock()
	idx, u, err := s.regularUser(key)
	if err != nil {
		return nil, err
	}
	plans, err := idx.planClients(u, []int{inboundId}, linkExisting)
	if err != nil {
		return nil, err
	}
	if err := s.applyPlans(idx, u, plans, idx.paramsOf(u)); err != nil {
		return nil, err
	}
	return s.savedView(u.SubId)
}

// RemoveProtocol removes the user's client in the inbound. The user stays,
// with its subId, even when that was its last client.
func (s *SubUserService) RemoveProtocol(key string, inboundId int) (*SubUserView, error) {
	subUserMu.Lock()
	defer subUserMu.Unlock()
	idx, u, err := s.regularUser(key)
	if err != nil {
		return nil, err
	}
	var victims []SubUserClient
	for _, c := range idx.clientsOf(u.SubId) {
		if c.InboundId == inboundId {
			victims = append(victims, c)
		}
	}
	if len(victims) == 0 {
		return nil, common.NewErrorf("user %s has no client in inbound %d", u.Name, inboundId)
	}
	if err := s.removeClients(idx, victims); err != nil {
		return nil, err
	}
	return s.viewOf(u.SubId)
}

// SetEnable switches every client of the user on or off. Switching one
// client leaves the others alone: that is the client's own toggle.
func (s *SubUserService) SetEnable(key string, enable bool) (*SubUserView, error) {
	subUserMu.Lock()
	defer subUserMu.Unlock()
	idx, u, err := s.regularUser(key)
	if err != nil {
		return nil, err
	}
	for _, c := range idx.clientsOf(u.SubId) {
		if c.Enable == enable {
			continue
		}
		switch c.Kind {
		case SubUserClientXray:
			err = s.updateXrayClient(idx, c, func(m map[string]any) { m["enable"] = enable })
		case SubUserClientAwg:
			err = s.awgService.ToggleClientByUUID(c.Key, enable)
		case SubUserClientWg:
			err = s.wgService.ToggleClientByUUID(c.Key, enable)
		}
		if err != nil {
			return nil, fmt.Errorf("switch client %s: %w", c.Name, err)
		}
	}
	return s.savedView(u.SubId)
}

// Delete removes the user and all its clients.
func (s *SubUserService) Delete(key string) error {
	subUserMu.Lock()
	defer subUserMu.Unlock()
	idx, u, err := s.regularUser(key)
	if err != nil {
		return err
	}
	if err := s.removeClients(idx, idx.clientsOf(u.SubId)); err != nil {
		return err
	}
	return database.GetDB().Delete(&model.SubUser{}, "sub_id = ?", u.SubId).Error
}

// Assign gives a client of robot — an xray client without a subId, a tunnel
// client without a link — to the user. The client keeps its name.
func (s *SubUserService) Assign(key, clientName string) (*SubUserView, error) {
	subUserMu.Lock()
	defer subUserMu.Unlock()
	idx, u, err := s.regularUser(key)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(clientName)
	var found []SubUserClient
	for _, c := range idx.clients {
		if c.Name != "" && strings.EqualFold(c.Name, name) {
			found = append(found, c)
		}
	}
	if len(found) == 0 {
		return nil, common.NewErrorf("client %q not found", name)
	}
	if len(found) > 1 {
		return nil, common.NewErrorf("client name %q is shared by %d clients; assign it in its inbound", name, len(found))
	}
	c := found[0]
	if c.owner != model.SubUserRobotKey {
		return nil, common.NewErrorf("client %q is not robot's: %s", c.Name, idx.describe(&c))
	}
	switch c.Kind {
	case SubUserClientXray:
		err = s.updateXrayClient(idx, c, func(m map[string]any) { m["subId"] = u.SubId })
	default:
		err = s.links.Set(c.Key, c.Kind, u.SubId)
	}
	if err != nil {
		return nil, err
	}
	return s.savedView(u.SubId)
}

// --- helpers ------------------------------------------------------------------

// clientParams are what a new client is created with.
type clientParams struct {
	SubUserParams
	tgId   int64
	enable bool
}

// clientPlan is one client Create or AddProtocol is about to make: a new
// client in inbound, or the unlinked AmneziaWG client linkUUID taken over.
type clientPlan struct {
	inbound  *model.Inbound
	name     string
	linkUUID string
}

// regularUser syncs and returns the user under key, refusing the technical
// users: they are not edited by hand.
func (s *SubUserService) regularUser(key string) (*subUserIndex, *model.SubUser, error) {
	idx, err := s.syncLocked()
	if err != nil {
		return nil, nil, err
	}
	u := idx.users[key]
	if u == nil {
		return nil, nil, common.NewErrorf("user with subId %q not found", key)
	}
	if u.IsTechnical() {
		return nil, nil, common.NewErrorf("%s is a technical user and cannot be changed by hand", u.Name)
	}
	return idx, u, nil
}

// viewOf reads the user back after a change.
func (s *SubUserService) viewOf(key string) (*SubUserView, error) {
	idx, err := s.loadIndex()
	if err != nil {
		return nil, err
	}
	u := idx.users[key]
	if u == nil {
		return nil, common.NewErrorf("user with subId %q not found", key)
	}
	return idx.view(u), nil
}

// checkNewUserName: present, not too long, not reserved, not taken.
func (idx *subUserIndex) checkNewUserName(name string) error {
	switch {
	case name == "":
		return common.NewError("the user name is empty")
	case len(name) > subUserMaxName:
		return common.NewErrorf("the user name is longer than %d characters", subUserMaxName)
	case isReservedUserName(name):
		return common.NewErrorf("name %q is reserved for a technical user", name)
	}
	if other := idx.userByName(name); other != nil {
		return common.NewErrorf("user %s already exists", other.Name)
	}
	return nil
}

// checkNewSubId: a subId given for a new user must be nobody's.
func (idx *subUserIndex) checkNewSubId(subId string) error {
	if err := idx.checkSubId(subId, ""); err != nil {
		return err
	}
	if owner := idx.users[subId]; owner != nil {
		return common.NewErrorf("subId %q belongs to user %s", subId, owner.Name)
	}
	return nil
}

// freeSubId is a new random subId no user and no client has.
func (idx *subUserIndex) freeSubId() string {
	for {
		subId := random.Seq(16)
		if idx.users[subId] == nil && !isReservedSubId(subId) && subId != idx.probeSubId {
			return subId
		}
	}
}

// planClients checks every client the user would get in inboundIds and
// returns what to do, before anything is written.
func (idx *subUserIndex) planClients(u *model.SubUser, inboundIds []int, linkExisting bool) ([]clientPlan, error) {
	have := map[int]bool{}
	for _, c := range idx.clientsOf(u.SubId) {
		have[c.InboundId] = true
	}
	var plans []clientPlan
	var writes []ClientWrite
	seen := map[int]bool{}
	for _, id := range inboundIds {
		if seen[id] {
			continue
		}
		seen[id] = true
		ib := idx.inbounds[id]
		if ib == nil {
			return nil, common.NewErrorf("inbound %d not found", id)
		}
		if !userProtocols[ib.Protocol] {
			return nil, common.NewErrorf("inbound %s: %s clients cannot be given to users", ib.Remark, ib.Protocol)
		}
		if have[id] {
			return nil, common.NewErrorf("user %s already has a client in inbound %s", u.Name, ib.Remark)
		}
		remark := ib.Remark
		if strings.TrimSpace(remark) == "" {
			remark = string(ib.Protocol)
		}
		plan := clientPlan{inbound: ib, name: SubUserClientName(u.Name, remark)}
		if ib.Protocol == model.AmneziaWG {
			if c := idx.clientByName(plan.name); c != nil && c.Kind == SubUserClientAwg && c.owner == model.SubUserRobotKey {
				if !linkExisting {
					return nil, &SubUserConflict{Code: SubUserConflictAwgLinkable, Client: c.Name,
						msg: fmt.Sprintf("AmneziaWG client %q exists and has no subscription: link it instead of creating one", c.Name)}
				}
				plan.linkUUID = c.Key
				plans = append(plans, plan)
				continue
			}
		}
		plans = append(plans, plan)
		writes = append(writes, ClientWrite{Name: plan.name, SubId: u.SubId, User: u.Name})
	}
	if err := idx.validate(writes); err != nil {
		return nil, err
	}
	return plans, nil
}

// paramsOf are the limits of the user's xray client, else of its AmneziaWG
// client, else none: what AddProtocol copies. The tgId is the user's (#186).
func (idx *subUserIndex) paramsOf(u *model.SubUser) clientParams {
	clients := idx.clientsOf(u.SubId)
	for _, kind := range []string{SubUserClientXray, SubUserClientAwg} {
		for _, c := range clients {
			if c.Kind == kind {
				return clientParams{SubUserParams: SubUserParams{TotalGB: c.TotalGB, ExpiryTime: c.ExpiryTime, LimitIp: c.LimitIp, Reset: c.Reset},
					tgId: u.TgId, enable: c.Enable}
			}
		}
	}
	return clientParams{tgId: u.TgId, enable: true}
}

// applyPlans makes the planned clients under the user's subId. On a failure
// it undoes the ones made so far, newest first, and returns the failure.
func (s *SubUserService) applyPlans(idx *subUserIndex, u *model.SubUser, plans []clientPlan, p clientParams) error {
	var undo []func() error
	rollback := func(cause error) error {
		for i := len(undo) - 1; i >= 0; i-- {
			if err := undo[i](); err != nil {
				logger.Warning("users: rollback step failed:", err)
			}
		}
		return cause
	}
	for _, plan := range plans {
		switch {
		case plan.linkUUID != "":
			linkUUID := plan.linkUUID
			if err := s.links.Set(linkUUID, model.TunnelKindAwg, u.SubId); err != nil {
				return rollback(err)
			}
			undo = append(undo, func() error { return s.links.Clear(linkUUID) })
		case plan.inbound.Protocol == model.AmneziaWG:
			peer := &model.TunnelClient{Name: plan.name, Email: plan.name, Enable: p.enable, TotalGB: p.TotalGB,
				ExpiryTime: p.ExpiryTime, LimitIp: p.LimitIp, Reset: p.Reset, TgId: p.tgId}
			if err := s.awgService.AddClient(peer); err != nil {
				return rollback(fmt.Errorf("inbound %s: %w", plan.inbound.Remark, err))
			}
			peerUUID := peer.UUID
			undo = append(undo, func() error { return s.awgService.DeleteClientByUUID(peerUUID) })
			if err := s.links.Set(peerUUID, model.TunnelKindAwg, u.SubId); err != nil {
				return rollback(err)
			}
		default:
			if err := s.addXrayClient(plan, u.SubId, p); err != nil {
				return rollback(fmt.Errorf("inbound %s: %w", plan.inbound.Remark, err))
			}
			ib, email := plan.inbound, plan.name
			undo = append(undo, func() error { return s.restoreXrayInbound(ib, email) })
		}
	}
	return nil
}

// addXrayClient adds one client through the panel's own add path, so the
// traffic row, the live xray user and the guards are the usual ones.
func (s *SubUserService) addXrayClient(plan clientPlan, subId string, p clientParams) error {
	ib := plan.inbound
	existing, _ := s.inboundService.GetClients(ib)
	flow := ""
	for _, c := range existing {
		if !IsProbeAccount(c.Email) {
			flow = c.Flow
			break
		}
	}
	var settings map[string]any
	_ = json.Unmarshal([]byte(ib.Settings), &settings)
	method, _ := settings["method"].(string)
	client := model.Client{Email: plan.name, SubID: subId, Enable: p.enable, TotalGB: p.TotalGB, ExpiryTime: p.ExpiryTime,
		LimitIP: p.LimitIp, Reset: p.Reset, TgID: p.tgId}
	client.ID, client.Password = probeSecret(ib.Protocol, method)
	switch ib.Protocol {
	case model.VMESS:
		client.Security = "auto"
	case model.VLESS:
		client.Flow = flow
	}
	payload, err := json.Marshal(map[string]any{"clients": []model.Client{client}})
	if err != nil {
		return err
	}
	needRestart, err := s.inboundService.AddInboundClient(&model.Inbound{Id: ib.Id, Settings: string(payload)})
	if err != nil {
		// A failed save can still commit the client's traffic row; the name
		// was checked free, so any row under it is this attempt's.
		if delErr := s.inboundService.DelClientStat(database.GetDB(), plan.name); delErr != nil {
			logger.Warning("users: could not drop the traffic row of a failed add:", delErr)
		}
		return err
	}
	if needRestart && ib.Enable {
		s.xrayService.SetToNeedRestart()
	}
	return nil
}

// updateXrayClient changes fields of one stored xray client through the
// panel's update path. The client is edited as stored, so fields the model
// does not know survive.
func (s *SubUserService) updateXrayClient(idx *subUserIndex, c SubUserClient, change func(map[string]any)) error {
	ib := idx.inbounds[c.InboundId]
	if ib == nil {
		return common.NewErrorf("inbound %d not found", c.InboundId)
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(ib.Settings), &settings); err != nil {
		return err
	}
	for _, raw := range clientsArray(settings) {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if email, _ := m["email"].(string); email != c.Name {
			continue
		}
		change(m)
		payload, err := json.Marshal(map[string]any{"clients": []any{m}})
		if err != nil {
			return err
		}
		needRestart, err := s.inboundService.UpdateInboundClient(&model.Inbound{Id: ib.Id, Settings: string(payload)}, c.Key)
		if err != nil {
			return err
		}
		if needRestart && ib.Enable {
			s.xrayService.SetToNeedRestart()
		}
		return nil
	}
	return common.NewErrorf("client %s not found in inbound %s", c.Name, ib.Remark)
}

// removeClients removes the clients, refusing up front if that would leave
// an xray inbound with no client at all, which the panel never allows.
func (s *SubUserService) removeClients(idx *subUserIndex, victims []SubUserClient) error {
	removed := map[int]int{}
	for _, c := range victims {
		if c.Kind == SubUserClientXray {
			removed[c.InboundId]++
		}
	}
	for id, n := range removed {
		total := 0
		for _, c := range idx.clients {
			if c.Kind == SubUserClientXray && c.InboundId == id {
				total++
			}
		}
		if n >= total {
			return common.NewErrorf("inbound %s would be left without clients; delete the inbound instead", idx.inbounds[id].Remark)
		}
	}
	for _, c := range victims {
		var err error
		switch c.Kind {
		case SubUserClientXray:
			err = s.removeXrayClient(c.InboundId, c.Name)
		case SubUserClientAwg:
			err = s.awgService.DeleteClientByUUID(c.Key)
		case SubUserClientWg:
			err = s.wgService.DeleteClientByUUID(c.Key)
		}
		if err != nil {
			return fmt.Errorf("remove client %s: %w", c.Name, err)
		}
	}
	return nil
}

// removeXrayClient drops the client with the email from the inbound, with
// its traffic and IP rows and its live xray user. The last client of an
// inbound is not removed: the panel never leaves an inbound without one.
func (s *SubUserService) removeXrayClient(inboundId int, email string) error {
	ib, err := s.inboundService.GetInbound(inboundId)
	if err != nil {
		return err
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(ib.Settings), &settings); err != nil {
		return err
	}
	kept := []any{}
	found, live := false, false
	for _, raw := range clientsArray(settings) {
		if m, ok := raw.(map[string]any); ok {
			if e, _ := m["email"].(string); e == email {
				found = true
				live, _ = m["enable"].(bool)
				continue
			}
		}
		kept = append(kept, raw)
	}
	if !found {
		return common.NewErrorf("client %s not found in inbound %s", email, ib.Remark)
	}
	if len(kept) == 0 {
		return common.NewErrorf("inbound %s would be left without clients", ib.Remark)
	}
	settings["clients"] = kept
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return s.writeXrayInbound(ib, string(out), email, live)
}

// restoreXrayInbound is the rollback of a client add: the inbound's settings
// go back to what they were before (snapshot), the client's traffic and IP
// rows go, and so does its live xray user.
func (s *SubUserService) restoreXrayInbound(snapshot *model.Inbound, email string) error {
	return s.writeXrayInbound(snapshot, snapshot.Settings, email, true)
}

// writeXrayInbound stores settings as the inbound's and forgets the client
// with the email: its traffic and IP rows, and — when live — its xray user.
func (s *SubUserService) writeXrayInbound(ib *model.Inbound, settings, email string, live bool) error {
	tx := database.GetDB().Begin()
	if err := tx.Model(&model.Inbound{}).Where("id = ?", ib.Id).Update("settings", settings).Error; err != nil {
		tx.Rollback()
		return err
	}
	if err := s.inboundService.DelClientStat(tx, email); err != nil {
		tx.Rollback()
		return err
	}
	if err := s.inboundService.DelClientIPs(tx, email); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit().Error; err != nil {
		return err
	}
	if live && ib.Enable {
		s.removeLiveXrayUser(ib, email)
	}
	return nil
}

// removeLiveXrayUser takes the client off the running xray, or asks for a
// restart where the API cannot (mixed/http) or fails.
func (s *SubUserService) removeLiveXrayUser(ib *model.Inbound, email string) {
	if ib.Protocol == model.Mixed || ib.Protocol == model.HTTP {
		s.xrayService.SetToNeedRestart()
		return
	}
	api := s.inboundService.xrayApi
	if err := api.Init(xrayAPIPort()); err != nil {
		s.xrayService.SetToNeedRestart()
		return
	}
	defer api.Close()
	if err := api.RemoveUser(ib.Tag, email); err != nil && !strings.Contains(err.Error(), fmt.Sprintf("User %s not found.", email)) {
		s.xrayService.SetToNeedRestart()
	}
}
