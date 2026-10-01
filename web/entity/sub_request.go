package entity

import (
	"slices"
	"strconv"
	"strings"

	"github.com/coinman-dev/3ax-ui/v2/util/common"
)

// The request defaults (#188 point 5, #221): the inbounds, the traffic per
// protocol and the expiry «✅ Approve» gives the user a request for a
// subscription makes.

// SubRequestMaxNumber bounds the defaults' traffic (GB) and expiry (days),
// as the bot's «New user» bounds a typed limit.
const SubRequestMaxNumber = 999999

// ParseSubRequestInbounds reads the request defaults' inbounds: ids separated
// by commas, each once, in the order given; nil for "" — every enabled
// inbound.
func ParseSubRequestInbounds(value string) ([]int, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	var ids []int
	for part := range strings.SplitSeq(value, ",") {
		id, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || id <= 0 {
			return nil, common.NewErrorf("request defaults: %q is not a list of inbound ids", value)
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// checkSubRequestDefaults tidies the request defaults the settings form sent
// and refuses a malformed inbound list or a limit out of range.
func checkSubRequestDefaults(s *AllSetting) error {
	ids, err := ParseSubRequestInbounds(s.SubRequestInbounds)
	if err != nil {
		return err
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	s.SubRequestInbounds = strings.Join(parts, ",")
	if s.SubRequestTrafficGB < 0 || s.SubRequestTrafficGB > SubRequestMaxNumber {
		return common.NewErrorf("request defaults: traffic %d GB is out of 0…%d", s.SubRequestTrafficGB, SubRequestMaxNumber)
	}
	if s.SubRequestExpiryDays < 0 || s.SubRequestExpiryDays > SubRequestMaxNumber {
		return common.NewErrorf("request defaults: expiry %d days is out of 0…%d", s.SubRequestExpiryDays, SubRequestMaxNumber)
	}
	return nil
}
