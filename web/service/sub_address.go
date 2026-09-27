package service

import (
	"net"
	"strings"

	"github.com/coinman-dev/3ax-ui/v2/nginx"
)

// publicSubBase is PublicSubBase for the given settings, so that the plan can
// ask it about the settings on screen and get the same answer the links will
// give once they are applied.
func (s *NginxService) publicSubBase(set NginxSettings) (scheme string, host string, ok bool) {
	if nginx.Mode(set.Mode) == nginx.ModeOff || !set.SubsBehind443 {
		return "", "", false
	}
	if set.Domain != "" {
		return "https", set.Domain, true
	}
	if host, ok := s.ipCertHost(); ok {
		return "https", host, true
	}
	return "", "", false
}

// subsUnreachable reports that the subscriptions are meant to be behind 443
// in only443, where their own port is closed, while nothing on 443 answers
// for them: no domain and no IP certificate. The links then fall back to the
// sub port (PublicSubBase says no), and so does the chain's poll of the panel
// — to a port the mode closes. It is a warning, not a blocker: the
// certificate may be on its way, and the operator may mean it.
func (s *NginxService) subsUnreachable(set NginxSettings) bool {
	if nginx.Mode(set.Mode) != nginx.ModeOnly443 || !set.SubsBehind443 {
		return false
	}
	if on, err := s.settingService.GetSubEnable(); err != nil || !on {
		return false
	}
	_, _, ok := s.publicSubBase(set)
	return !ok
}

// ipCertHost is the address a link to the HTTP side names when it answers by
// address only, ready to go into a URL (an IPv6 one in brackets).
//
// It is an address the IP certificate names, and nothing else: a request by
// address carries no server name, so only a link by address reaches the HTTP
// side at all, and only one the certificate names passes its TLS check. A
// name here — subDomain set to a domain the front does not serve — would be
// routed by SNI like any other unknown name and never get there. The request's
// own Host is no better: the panel is often reached through an SSH tunnel, as
// localhost. The certificate is the one source that is right by construction:
// Let's Encrypt issued it after reaching this box on that very address.
//
// When the certificate names several addresses, the one the operator already
// gave out wins — subDomain, then chainPanelHost — and IPv4 before IPv6
// otherwise, as the address more clients can reach.
func (s *NginxService) ipCertHost() (string, bool) {
	certFile, _, _, err := ipCertificate()
	if err != nil {
		return "", false
	}
	certs, err := parseCertificates(certFile)
	if err != nil || len(certs) == 0 || len(certs[0].IPAddresses) == 0 {
		return "", false
	}
	named := certs[0].IPAddresses

	var preferred []string
	if v, err := s.settingService.GetSubDomain(); err == nil {
		preferred = append(preferred, v)
	}
	if v, err := s.settingService.GetChainPanelHost(); err == nil {
		preferred = append(preferred, v)
	}
	for _, want := range preferred {
		ip := net.ParseIP(strings.Trim(strings.TrimSpace(want), "[]"))
		if ip == nil {
			continue
		}
		for _, have := range named {
			if have.Equal(ip) {
				return urlHost(have), true
			}
		}
	}
	for _, have := range named {
		if have.To4() != nil {
			return urlHost(have), true
		}
	}
	return urlHost(named[0]), true
}

// urlHost writes an address the way it goes into a URL.
func urlHost(ip net.IP) string {
	if ip.To4() != nil {
		return ip.String()
	}
	return "[" + ip.String() + "]"
}

// UseIPCertDirForTest points the panel at another /root/cert/ip and returns
// what undoes it. Package sub builds links through PublicSubBase and has no
// other way to give it an IP certificate.
func UseIPCertDirForTest(dir string) (restore func()) {
	prev := ipCertDir
	ipCertDir = dir
	return func() { ipCertDir = prev }
}
