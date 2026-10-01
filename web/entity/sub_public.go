package entity

import (
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/coinman-dev/3ax-ui/v2/util/common"
)

// The public subscription address (#224, map
// SBKubric/sane-3x-ui-orchestrator#52 decision 6): the domain the panel hands
// out its subscription links through, such as the subscription showcase's
// https://sub.example.com. Only the origin: the paths stay the panel's own
// (subPath, json, clash, tun), which the showcase proxies one to one. Empty
// means none, and the links are what they were without it.

// NormalizeSubPublicURL checks a public subscription address and returns it
// as the origin the links start with: the scheme and the host in lower case,
// the port only when it is not the scheme's default, no trailing slash. ""
// stays "".
func NormalizeSubPublicURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", subPublicURLError(raw)
	}
	scheme := strings.ToLower(u.Scheme)
	if (scheme != "http" && scheme != "https") || u.Opaque != "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", subPublicURLError(raw)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || strings.ContainsAny(host, " \t/\\") {
		return "", subPublicURLError(raw)
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", subPublicURLError(raw)
		}
		if (scheme == "https" && n == 443) || (scheme == "http" && n == 80) {
			port = ""
		}
		if port != "" {
			return scheme + "://" + net.JoinHostPort(host, port), nil
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return "", subPublicURLError(raw)
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]" // IPv6
	}
	return scheme + "://" + host, nil
}

func subPublicURLError(raw string) error {
	return common.NewError("public subscription address must be (http|https)://host[:port] with no path:", raw)
}

// checkSubPublicURL normalises the public subscription address the settings
// form sent and refuses one that is not an origin.
func checkSubPublicURL(s *AllSetting) error {
	normalized, err := NormalizeSubPublicURL(s.SubPublicURL)
	if err != nil {
		return err
	}
	s.SubPublicURL = normalized
	return nil
}
