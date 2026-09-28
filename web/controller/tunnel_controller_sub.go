package controller

import (
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/web/service"
)

// The subscription half of the AWG/WG client API
// (docs/spec/tunnel-subscription.md §4, docs/spec/users.md §4). It lives here
// so tunnel_controller.go carries only the call sites.

// tunnelClientReq is a client body with its subscription: nil when the
// request does not send subId (the link is left alone), "" to unlink, a
// subId to link.
type tunnelClientReq struct {
	model.TunnelClient
	SubId *string `json:"subId"`
}

// tunnelClientResp is a client with the subId it is linked to, "" for none.
type tunnelClientResp struct {
	model.TunnelClient
	SubId string `json:"subId"`
}

// guardClientWrite runs the user rules on a write through this API. id or
// clientUUID names the client an update replaces; both empty for an add.
func (a *TunnelController) guardClientWrite(id int, clientUUID string, req *tunnelClientReq) error {
	return (&service.SubUserService{}).ValidateTunnelClientWrite(id, clientUUID, req.Email, req.SubId)
}

// linkClient applies the request's subId to the client, if it sent one.
func (a *TunnelController) linkClient(clientUUID string, subId *string) error {
	if subId == nil {
		return nil
	}
	return (&service.TunnelSubscriptionService{}).Set(clientUUID, a.kind.Name, *subId)
}

// withSubIds pairs every client with the subId it is linked to.
func (a *TunnelController) withSubIds(clients []model.TunnelClient) ([]tunnelClientResp, error) {
	uuids := make([]string, 0, len(clients))
	for _, c := range clients {
		uuids = append(uuids, c.UUID)
	}
	subIds, err := (&service.TunnelSubscriptionService{}).SubIdsByUUIDs(uuids)
	if err != nil {
		return nil, err
	}
	out := make([]tunnelClientResp, 0, len(clients))
	for _, c := range clients {
		out = append(out, tunnelClientResp{TunnelClient: c, SubId: subIds[c.UUID]})
	}
	return out, nil
}
