package service

import (
	"fmt"
	"strings"

	"github.com/coinman-dev/3ax-ui/v2/captcha"
	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/util/common"
)

// ClientWrite is one client about to be added or changed, as the user rules
// see it (docs/spec/users.md §4).
type ClientWrite struct {
	Name    string // the client's name (email) after the write
	OldName string // its name before, for a change; "" for a new client
	SubId   string // the subId it will carry; "" for none or unchanged
	// User is the user the write means the client for, when the caller names
	// one (the bot, the users API); "" when it only gives a subId, which is
	// how the panel adds a client to an existing subscription.
	User string
}

// ValidateClientWrites is the one check every client write goes through:
//
//   - a client name is unique across all protocols — xray emails and tunnel
//     client emails, ignoring case, within the batch too. Only new names are
//     checked, so a legacy duplicate can still be edited under its name;
//   - the technical users' names are not client names either;
//   - a subId belongs to exactly one user: a technical key is never a subId,
//     the probe subId is monitoring's, and a write naming a user may not use
//     another user's subId.
//
// Errors name the conflicting client or user. It reads and never locks, so a
// SubUserService call holding subUserMu can go through the guarded paths.
func (s *SubUserService) ValidateClientWrites(writes ...ClientWrite) error {
	idx, err := s.loadIndex()
	if err != nil {
		return err
	}
	return idx.validate(writes)
}

func (idx *subUserIndex) validate(writes []ClientWrite) error {
	batch := map[string]bool{}
	for _, w := range writes {
		if w.Name != "" && !strings.EqualFold(w.Name, w.OldName) {
			if strings.EqualFold(w.Name, model.SubUserRobot) || strings.EqualFold(w.Name, model.SubUserMonitoring) {
				return common.NewErrorf("name %q is reserved for a technical user", w.Name)
			}
			if batch[strings.ToLower(w.Name)] {
				return common.NewErrorf("client name %q appears twice in the request", w.Name)
			}
			if c := idx.clientByName(w.Name); c != nil {
				return common.NewErrorf("client name %q is already taken: %s", w.Name, idx.describe(c))
			}
			batch[strings.ToLower(w.Name)] = true
		}
		if err := idx.checkSubId(w.SubId, w.User); err != nil {
			return err
		}
	}
	return nil
}

// checkSubId is the subId half of validate.
func (idx *subUserIndex) checkSubId(subId, user string) error {
	if subId == "" {
		return nil
	}
	if isReservedSubId(subId) {
		return common.NewErrorf("subId %q is reserved for a technical user", subId)
	}
	if strings.EqualFold(subId, captcha.Segment) {
		// <subPath>captcha is the captcha page (#220): a subscription there
		// would never be served.
		return common.NewErrorf("subId %q is reserved: it is the captcha page's path", subId)
	}
	if idx.probeSubId != "" && subId == idx.probeSubId {
		return common.NewErrorf("subId %q belongs to technical user %s", subId, model.SubUserMonitoring)
	}
	if owner := idx.users[subId]; owner != nil && user != "" && !strings.EqualFold(owner.Name, user) {
		return common.NewErrorf("subId %q belongs to user %s", subId, owner.Name)
	}
	return nil
}

// clientByName finds a client by name, ignoring case; the first one when a
// legacy duplicate has several.
func (idx *subUserIndex) clientByName(name string) *SubUserClient {
	for i := range idx.clients {
		if idx.clients[i].Name != "" && strings.EqualFold(idx.clients[i].Name, name) {
			return &idx.clients[i]
		}
	}
	return nil
}

// describe names a client and its user for an error message. A subId whose
// user Sync has not created yet is named as the subscription.
func (idx *subUserIndex) describe(c *SubUserClient) string {
	owner := "subscription " + c.owner
	if u := idx.users[c.owner]; u != nil {
		owner = "user " + u.Name
	}
	switch c.Kind {
	case SubUserClientAwg:
		return fmt.Sprintf("AmneziaWG client of %s", owner)
	case SubUserClientWg:
		return fmt.Sprintf("WireGuard client of %s", owner)
	default:
		return fmt.Sprintf("%s client in inbound %s of %s", c.Protocol, c.InboundRemark, owner)
	}
}

// validateUserRules is the InboundService side of the user rules: the xray
// client add path (through rejectNewProbeClients) and update path call it
// with the batch they are about to write. oldEmail is the name of the client
// an update replaces, "" for an add.
func (s *InboundService) validateUserRules(oldEmail string, clients []model.Client) error {
	writes := make([]ClientWrite, 0, len(clients))
	for _, c := range clients {
		writes = append(writes, ClientWrite{Name: c.Email, OldName: oldEmail, SubId: c.SubID})
	}
	return (&SubUserService{}).ValidateClientWrites(writes...)
}

// ValidateTunnelClientWrite is ValidateClientWrites for the AWG/WG client
// API. id or clientUUID names the client an update replaces (both empty for
// an add); subId is what the request sent, nil when it sent none.
func (s *SubUserService) ValidateTunnelClientWrite(id int, clientUUID, name string, subId *string) error {
	w := ClientWrite{Name: name}
	if subId != nil {
		w.SubId = *subId
	}
	if id != 0 || clientUUID != "" {
		var old model.TunnelClient
		q := database.GetDB().Model(&model.TunnelClient{})
		if id != 0 {
			q = q.Where("id = ?", id)
		} else {
			q = q.Where("uuid = ?", clientUUID)
		}
		if err := q.First(&old).Error; err == nil {
			w.OldName = old.Email
		}
	}
	return s.ValidateClientWrites(w)
}
