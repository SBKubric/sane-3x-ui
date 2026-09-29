package service

import (
	"strconv"
	"strings"
)

// Search returns every user query names exactly, ignoring case: by its name,
// its subId, the name of one of its clients (an xray email, a tunnel
// client's name) or a Telegram id, its own or one of its clients'. The
// users come in List's order; none is not an error. A technical user is
// found by its name or its clients.
func (s *SubUserService) Search(query string) ([]*SubUserView, error) {
	idx, err := s.synced()
	if err != nil {
		return nil, err
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	tgId, _ := strconv.ParseInt(q, 10, 64)
	hit := map[string]bool{}
	for key, u := range idx.users {
		if strings.EqualFold(u.Name, q) || (!u.IsTechnical() && strings.EqualFold(u.SubId, q)) || (tgId > 0 && u.TgId == tgId) {
			hit[key] = true
		}
	}
	for _, c := range idx.clients {
		if (c.Name != "" && strings.EqualFold(c.Name, q)) || (tgId > 0 && c.TgId == tgId) {
			hit[c.owner] = true
		}
	}
	var out []*SubUserView
	for _, u := range idx.sortedUsers() {
		if hit[u.SubId] {
			out = append(out, idx.view(u))
		}
	}
	return out, nil
}
