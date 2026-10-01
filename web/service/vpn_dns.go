package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/logger"
)

// The VPN name's record on the active edge (#225, map
// SBKubric/sane-3x-ui-orchestrator#52 decision 7, docs/spec/users.md §15).
// The A record of the VPN name follows the active edge through the DNSExit
// API: POST https://api.dnsexit.com/dns/ with
//
//	{"apikey": "…", "domain": "example.com",
//	 "update": [{"type": "A", "name": "vpn.example.com", "content": "192.0.2.10", "ttl": 5}]}
//
// — the domain is the zone, the name may be the full name (DNSExit appends
// the domain only to a name that does not end with it), the TTL is in
// minutes; the answer is {"code": 0, "message": "…"}, 0 for success, 1 for
// "some execution problems — may not indicate failure", 2-7 for errors.
// DNSExit asks for at most one update every four minutes.
//
// VPNNameService.Tick runs every 15 seconds (job.VPNNameJob). It does not
// hook the places that switch the edge (the switch button, /proxy, the API):
// it compares the address the record should have — the active edge's IPv4 —
// with the one it last set or saw, and calls DNSExit when they differ, no
// sooner than four minutes after its previous call. A burst of switches
// makes one call, for the edge active when the four minutes are up. A
// refused call is retried after 4, 8, 16 and then every 30 minutes; the
// third failure in a row is posted to the notification channel, once, and
// so is the success after it. At start and then hourly the name is resolved:
// pointing elsewhere (edited by hand at DNSExit, a lost update), it is set
// again. With no key, no name or no active edge nothing is called.

// Test seams: DNSExit's address, the clock, the resolver and the channel.
var (
	dnsExitEndpoint = "https://api.dnsexit.com/dns/"
	vpnDNSNow       = time.Now
	vpnDNSLookup    = func(ctx context.Context, host string) ([]net.IP, error) {
		return net.DefaultResolver.LookupIP(ctx, "ip4", host)
	}
	vpnDNSNotify = func(key string, params ...string) {
		t := &Tgbot{}
		t.SendMsgToNotifyChannel(t.I18nBot(key, params...))
	}
	vpnDNSClient = &http.Client{Timeout: 30 * time.Second}
)

const (
	// vpnDNSMinInterval is DNSExit's limit: one update every four minutes.
	vpnDNSMinInterval = 4 * time.Minute
	// vpnDNSReconcileEvery is how often the name is resolved and compared.
	vpnDNSReconcileEvery = time.Hour
	// vpnDNSAlertAfter is how many failures in a row reach the channel.
	vpnDNSAlertAfter = 3
	// vpnDNSMaxBackoff caps the pause after a failure.
	vpnDNSMaxBackoff = 30 * time.Minute
)

// dnsExitRequest is the body of a DNSExit API call.
type dnsExitRequest struct {
	ApiKey string          `json:"apikey"`
	Domain string          `json:"domain"`
	Update []dnsExitRecord `json:"update"`
}

// dnsExitRecord is one record to update.
type dnsExitRecord struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
}

// dnsExitReply is DNSExit's answer.
type dnsExitReply struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// VPNNameService keeps the VPN name's record on the active edge.
type VPNNameService struct {
	settingService SettingService
}

// vpnDNSUpdater is what the updater remembers between ticks; in memory: a
// restart resolves the name again before it calls anyone.
type vpnDNSUpdater struct {
	mu        sync.Mutex
	applied   string    // "name=ip" the record is known to have; "" for unknown
	lastCall  time.Time // the last call to DNSExit
	lastCheck time.Time // the last time the name was resolved (or set)
	nextTry   time.Time // no call before this after a failure
	failures  int       // failures in a row
	alerted   bool      // the channel was told of them
	lastErr   string    // the last failure, logged once
}

var vpnDNS = &vpnDNSUpdater{}

func (u *vpnDNSUpdater) reset() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.forget()
	u.lastCall = time.Time{}
}

// forget drops what the updater knows of the record: the key or the name is
// gone, or the panel starts over.
func (u *vpnDNSUpdater) forget() {
	u.applied, u.lastCheck, u.nextTry, u.failures, u.alerted, u.lastErr = "", time.Time{}, time.Time{}, 0, false, ""
}

// Tick looks once: is the record where the active edge is, and if not, may
// DNSExit be called now.
func (s *VPNNameService) Tick() {
	u := vpnDNS
	u.mu.Lock()
	defer u.mu.Unlock()
	key, _ := s.settingService.GetDnsExitApiKey()
	name, _ := s.settingService.GetVPNName()
	if key == "" || name == "" {
		u.forget()
		return
	}
	host, on := s.settingService.GetProxyOverride()
	if !on {
		return // no active edge: the record stays where it is
	}
	now := vpnDNSNow()
	if now.Before(u.nextTry) {
		return
	}
	ip, err := edgeIPv4(host)
	if err != nil {
		u.failed(now, name, host, err)
		return
	}
	want := name + "=" + ip
	switch {
	case u.applied == want:
		if now.Sub(u.lastCheck) < vpnDNSReconcileEvery {
			return
		}
		u.lastCheck = now
		if resolvesTo(name, ip) {
			return
		}
		logger.Warningf("VPN name: %s no longer points at the active edge %s, setting it again", name, ip)
		u.applied = ""
	case u.applied == "" && u.lastCheck.IsZero():
		// The first look since the start: the record may well be right.
		u.lastCheck = now
		if resolvesTo(name, ip) {
			u.applied = want
			return
		}
	}
	if !u.lastCall.IsZero() && now.Before(u.lastCall.Add(vpnDNSMinInterval)) {
		return // the last wanted address goes when the four minutes are up
	}
	ttl, _ := s.settingService.GetVPNNameTTL()
	u.lastCall = now
	if err := dnsExitUpdate(key, name, ip, ttl); err != nil {
		u.failed(now, name, ip, err)
		return
	}
	logger.Infof("VPN name: %s now points at %s", name, ip)
	u.applied, u.lastCheck, u.nextTry, u.failures, u.lastErr = want, now, time.Time{}, 0, ""
	if u.alerted {
		u.alerted = false
		vpnDNSNotify("tgbot.vpnname.recovered", "Name=="+html.EscapeString(name), "IP=="+ip)
	}
}

// failed counts a failure: the pause before the next try grows, and the
// third in a row is posted to the channel.
func (u *vpnDNSUpdater) failed(now time.Time, name, target string, err error) {
	u.failures++
	backoff := vpnDNSMinInterval << (u.failures - 1)
	if u.failures > 4 || backoff > vpnDNSMaxBackoff {
		backoff = vpnDNSMaxBackoff
	}
	u.nextTry = now.Add(backoff)
	if msg := err.Error(); msg != u.lastErr {
		u.lastErr = msg
		logger.Warningf("VPN name: could not point %s at %s: %v", name, target, err)
	}
	if u.failures >= vpnDNSAlertAfter && !u.alerted {
		u.alerted = true
		vpnDNSNotify("tgbot.vpnname.failed", "Name=="+html.EscapeString(name), "IP=="+html.EscapeString(target),
			"Count=="+strconv.Itoa(u.failures), "Error=="+html.EscapeString(err.Error()))
	}
}

// edgeIPv4 is the IPv4 address an A record names for host: host itself, or
// what it resolves to.
func edgeIPv4(host string) (string, error) {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return v4.String(), nil
		}
		return "", fmt.Errorf("the active edge %s has no IPv4 address for an A record", host)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ips, err := vpnDNSLookup(ctx, host)
	if err != nil {
		return "", fmt.Errorf("resolving the active edge %s: %w", host, err)
	}
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			return v4.String(), nil
		}
	}
	return "", fmt.Errorf("the active edge %s has no IPv4 address for an A record", host)
}

// resolvesTo reports whether name resolves to ip now.
func resolvesTo(name, ip string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ips, err := vpnDNSLookup(ctx, name)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(ips, func(have net.IP) bool { return have.String() == ip })
}

// dnsExitUpdate points name's A record at ip. The key goes in the body only,
// and never into an error.
func dnsExitUpdate(key, name, ip string, ttl int) error {
	body, err := json.Marshal(dnsExitRequest{ApiKey: key, Domain: vpnNameZone(name),
		Update: []dnsExitRecord{{Type: "A", Name: name, Content: ip, TTL: ttl}}})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, dnsExitEndpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := vpnDNSClient.Do(req)
	if err != nil {
		return fmt.Errorf("DNSExit: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("DNSExit answered HTTP %d", resp.StatusCode)
	}
	var reply dnsExitReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return fmt.Errorf("DNSExit answered something that is not its JSON: %.80q", raw)
	}
	switch reply.Code {
	case 0:
		return nil
	case 1:
		// "May not indicate failure": the hourly reconcile tells.
		logger.Warningf("VPN name: DNSExit: %s", reply.Message)
		return nil
	}
	return fmt.Errorf("DNSExit code %d: %s", reply.Code, reply.Message)
}
