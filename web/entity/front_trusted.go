package entity

import (
	"net/netip"
	"strings"

	"github.com/coinman-dev/3ax-ui/v2/util/common"
)

// The front's trusted addresses (#228, map
// SBKubric/sane-3x-ui-orchestrator#52): the hosts that call the fronts'
// HTTP side on behalf of many clients — the subscription showcase above all —
// and so must be neither limited nor banned there. Every hop gets the list
// in its chain document (frontTrustedAddrs) and puts it beside its chain
// neighbours in the guard's exemptions: limit_req and limit_conn skip them,
// the probe jail ignores them. The panel's own front does the same.
//
// An entry is an IPv4 or IPv6 address, or a network in CIDR notation. A
// name is refused, as the guard never resolves one: the address behind it
// could change under the config.

// FrontTrustedMax is the most entries the list holds. A showcase is one
// address; the cap is there so that a pasted log cannot become a config.
const FrontTrustedMax = 64

// The narrowest networks a trusted entry may be: a host, or at most a
// provider's block. Anything wider switches the protection off for a large
// part of the internet, which is no longer trusting a host.
const (
	frontTrustedMinBits4 = 16
	frontTrustedMinBits6 = 32
)

// ParseFrontTrustedAddrs reads the list as the form, the API or the CLI sends
// it — entries separated by commas, semicolons, spaces or new lines — and
// returns it normalised: an address in its canonical form (a v4-mapped one
// as plain v4), a network masked, in the order given, without repeats. ""
// is nil.
func ParseFrontTrustedAddrs(raw string) ([]string, error) {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	var out []string
	seen := map[string]bool{}
	for _, field := range fields {
		entry, err := frontTrustedEntry(field)
		if err != nil {
			return nil, err
		}
		if !seen[entry] {
			seen[entry] = true
			out = append(out, entry)
		}
	}
	if len(out) > FrontTrustedMax {
		return nil, common.NewErrorf("at most %d front trusted addresses, got %d", FrontTrustedMax, len(out))
	}
	return out, nil
}

// NormalizeFrontTrustedAddrs is ParseFrontTrustedAddrs as the one string the
// setting stores: the entries joined by commas.
func NormalizeFrontTrustedAddrs(raw string) (string, error) {
	list, err := ParseFrontTrustedAddrs(raw)
	if err != nil {
		return "", err
	}
	return strings.Join(list, ","), nil
}

func frontTrustedEntry(value string) (string, error) {
	if prefix, err := netip.ParsePrefix(value); err == nil {
		prefix = prefix.Masked()
		minBits := frontTrustedMinBits6
		if prefix.Addr().Is4() {
			minBits = frontTrustedMinBits4
		}
		if prefix.Bits() < minBits {
			return "", common.NewErrorf("front trusted address %q is too wide a network: /%d at the widest", value, minBits)
		}
		return prefix.String(), nil
	}
	addr, err := netip.ParseAddr(value)
	if err != nil || addr.Zone() != "" {
		return "", common.NewErrorf("front trusted address %q is neither an IP address nor a network in CIDR notation", value)
	}
	addr = addr.Unmap()
	if addr.IsUnspecified() {
		return "", common.NewErrorf("front trusted address %q is the unspecified address", value)
	}
	return addr.String(), nil
}

// checkFrontTrustedAddrs normalises the list the settings form sent and
// refuses one with an entry that is not an address or a network.
func checkFrontTrustedAddrs(s *AllSetting) error {
	normalized, err := NormalizeFrontTrustedAddrs(s.FrontTrustedAddrs)
	if err != nil {
		return err
	}
	s.FrontTrustedAddrs = normalized
	return nil
}
