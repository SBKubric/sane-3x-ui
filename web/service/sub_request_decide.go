package service

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/util/common"
	"gorm.io/gorm"
)

// An admin's decisions on requests (#188 points 4–7, #221,
// docs/spec/users.md §12): the bot's «📥 Incoming requests» reads the
// pending ones here and decides through these methods.
//   - Approve makes the user and marks the request approved, both under the
//     requests' lock, so an applicant's cancel cannot slip in between. The
//     user's Telegram is always the applicant's: Create binds it one to one,
//     as the users' rules bind a typed tg_id (#210, #219), and refuses one
//     that is another user's. A refused create approves nothing.
//   - Draft is the user «✅ Approve» would make — the request defaults of the
//     settings and a name from the account — for «⚙️ Approve with changes»
//     to edit; ApproveWithDefaults approves it as it is.
//   - Block rejects the account's pending request and takes none from it
//     until an admin unblocks it (SetBlocked).

// ErrNoRequest is a decision on a request that does not exist.
var ErrNoRequest = errors.New("no such request")

// Get returns the request id.
func (s *SubRequestService) Get(id int64) (*model.SubRequest, error) {
	var r model.SubRequest
	err := database.GetDB().First(&r, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNoRequest
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// Pending lists the requests that wait, oldest first.
func (s *SubRequestService) Pending() ([]model.SubRequest, error) {
	var out []model.SubRequest
	err := database.GetDB().Where("status = ?", model.SubRequestPending).Order("created_at, id").Find(&out).Error
	return out, err
}

// CountPending counts the requests that wait.
func (s *SubRequestService) CountPending() (int64, error) {
	var n int64
	err := database.GetDB().Model(&model.SubRequest{}).Where("status = ?", model.SubRequestPending).Count(&n).Error
	return n, err
}

// Rejections are the account's rejected requests, newest first.
func (s *SubRequestService) Rejections(tgId int64) ([]model.SubRequest, error) {
	var out []model.SubRequest
	err := database.GetDB().Where("tg_id = ? AND status = ?", tgId, model.SubRequestRejected).
		Order("decided_at DESC, id DESC").Find(&out).Error
	return out, err
}

// Blocked lists the blocked accounts.
func (s *SubRequestService) Blocked() ([]model.TgAccount, error) {
	var out []model.TgAccount
	err := database.GetDB().Where("requests_blocked = ?", true).Order("tg_id").Find(&out).Error
	return out, err
}

// pendingLocked returns the request id while it waits; the caller holds
// subRequestMu.
func (s *SubRequestService) pendingLocked(id int64) (*model.SubRequest, error) {
	r, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if r.Status != model.SubRequestPending {
		return nil, &SubRequestRefusal{Code: SubRequestNotPending}
	}
	return r, nil
}

// Approve — «✅ Approve» and the «Create» of «⚙️ Approve with changes» —
// makes the user req for the pending request id, with the applicant's
// Telegram whatever req says, and marks the request approved by by.
func (s *SubRequestService) Approve(id int64, by string, req SubUserCreate) (*SubUserView, error) {
	subRequestMu.Lock()
	defer subRequestMu.Unlock()
	r, err := s.pendingLocked(id)
	if err != nil {
		return nil, err
	}
	req.TgId, req.TgNick = r.TgId, ""
	v, err := (&SubUserService{}).Create(req)
	if err != nil {
		return nil, err
	}
	if _, err := s.decideLocked(r, model.SubRequestApproved, by, ""); err != nil {
		return nil, err
	}
	return v, nil
}

// ApproveWithDefaults approves the pending request id with its Draft.
func (s *SubRequestService) ApproveWithDefaults(id int64, by string) (*SubUserView, error) {
	req, err := s.Draft(id)
	if err != nil {
		return nil, err
	}
	return s.Approve(id, by, req)
}

// Draft is the user the pending request id gets by default: named after the
// account (requestUserName), with the request defaults of the settings.
func (s *SubRequestService) Draft(id int64) (SubUserCreate, error) {
	r, err := s.Get(id)
	if err != nil {
		return SubUserCreate{}, err
	}
	if r.Status != model.SubRequestPending {
		return SubUserCreate{}, &SubRequestRefusal{Code: SubRequestNotPending}
	}
	defaults, err := (&SettingService{}).GetSubRequestDefaults()
	if err != nil {
		return SubUserCreate{}, err
	}
	inbounds, err := requestInbounds(defaults)
	if err != nil {
		return SubUserCreate{}, err
	}
	account, err := (&TgAccountService{}).Get(r.TgId)
	if err != nil {
		return SubUserCreate{}, err
	}
	name, err := (&SubUserService{}).FreeName(requestUserName(r.TgId, account))
	if err != nil {
		return SubUserCreate{}, err
	}
	return SubUserCreate{Name: name, TgId: r.TgId, InboundIds: inbounds, SubUserParams: SubUserParams{
		TotalGB: int64(defaults.TrafficGB) << 30, ExpiryTime: -int64(defaults.ExpiryDays) * 86400000}}, nil
}

// requestInbounds are the inbounds of the request defaults that exist and
// can have a user's client: the chosen ones, or every enabled one. None is
// an error to show the admin.
func requestInbounds(d *SubRequestDefaults) ([]int, error) {
	all, err := (&SubUserService{}).Inbounds()
	if err != nil {
		return nil, err
	}
	var ids []int
	for _, ib := range all {
		if d.InboundIds == nil && ib.Enable || slices.Contains(d.InboundIds, ib.Id) {
			ids = append(ids, ib.Id)
		}
	}
	if len(ids) == 0 {
		return nil, common.NewError("the request defaults name no inbound a user can have: set them in the panel's Telegram settings")
	}
	return ids, nil
}

// requestUserName is the name a request's user is made with (#188 point 6):
// the account's @nick without the '@', else its Telegram name, else
// "tg<id>" — cut to leave room for the "-2" of a name taken.
func requestUserName(tgId int64, a *model.TgAccount) string {
	name := "tg" + strconv.FormatInt(tgId, 10)
	if a != nil {
		if a.Username != "" {
			name = a.Username
		} else if full := strings.Join(strings.Fields(a.FirstName+" "+a.LastName), " "); full != "" {
			name = full
		}
	}
	for len(name) > subUserMaxName-4 {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	return name
}

// FreeName is base, or base-2, base-3… — the first no user has (ignoring
// case) and no technical user is called.
func (s *SubUserService) FreeName(base string) (string, error) {
	idx, err := s.synced()
	if err != nil {
		return "", err
	}
	return uniqueUserName(base, idx.takenNames()), nil
}

// Block — «🚫 Block» — takes no more requests from the account tgId and
// rejects the one it has pending, as by.
func (s *SubRequestService) Block(tgId int64, by string) error {
	subRequestMu.Lock()
	defer subRequestMu.Unlock()
	if err := s.SetBlocked(tgId, true); err != nil {
		return err
	}
	var pending []model.SubRequest
	if err := database.GetDB().Where("tg_id = ? AND status = ?", tgId, model.SubRequestPending).Find(&pending).Error; err != nil {
		return err
	}
	for i := range pending {
		if _, err := s.decideLocked(&pending[i], model.SubRequestRejected, by, ""); err != nil {
			return err
		}
	}
	return nil
}
