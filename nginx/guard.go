package nginx

import (
	"fmt"
	"net"
	"net/netip"
	"path/filepath"
	"sort"
	"strings"
)

// MissLogPath is where the HTTP side writes its misses for fail2ban's probe
// jail (#141). It sits among nginx's own logs so the distro's logrotate rule
// for /var/log/nginx/*.log rotates it with them.
var MissLogPath = "/var/log/nginx/3ax-ui-miss.log"

// Guard is the protection of an only443 HTTP side (ADR 0005, #141): a limit
// per client address on every published path, and a log of the misses — the
// requests a prober makes — for fail2ban to ban on.
//
// A miss is not something a status code can show. The HTTP side answers an
// unknown path with the stub, 200 like any page of a real site, and it answers
// a client over its limit with the stub as well, not 429: a prober must not be
// able to tell a limit from a path that is not there, or a path that is there
// from one that is not. So the misses are written down on the side, in a log
// of their own, and that log is what the jail reads.
type Guard struct {
	// Exempt are the addresses (IPs or CIDRs) that are neither limited nor
	// banned: the neighbours in the chain, which fetch subscriptions and the
	// wave on behalf of every client behind them, and mon-server. Loopback
	// is always exempt.
	Exempt []string
	// MissLog is the file the misses go to.
	MissLog string
}

// zone is one limit on the HTTP side: a rate per client address with a burst
// on top, and a cap on the requests one address has open at once.
type zone struct {
	name  string
	rate  string
	burst int
	conns int
}

// The limits. Each is keyed by client address, so one household behind a NAT
// shares it; the chain's neighbours and mon-server are exempt altogether.
var (
	// zoneSub guards the subscription paths. A client app refreshes every
	// few hours, so a steady trickle is plenty; the burst is a household or
	// an office behind one address whose phones all refresh at once. A
	// prober walking subscription ids gets 30 a minute and is banned by the
	// probe jail after ten misses anyway.
	zoneSub = zone{name: "threeax_sub", rate: "30r/m", burst: 30, conns: 20}
	// zoneAPI guards the panel API, the wave and the monitoring contract.
	// The orchestrator calls the API in bursts — a run reads the inbounds,
	// writes a few, reads them back — so the burst is generous and the rate
	// high. Every call needs a session, so the limit is about load, not
	// guessing; the hops and mon-server, the steady callers, are exempt.
	zoneAPI = zone{name: "threeax_api", rate: "10r/s", burst: 100, conns: 50}
	// zoneLogin guards the one place a secret is typed by hand: the panel's
	// login, a box's join page. Five tries at once, then one every ten
	// seconds; the login jail bans after five failures in any case.
	zoneLogin = zone{name: "threeax_login", rate: "6r/m", burst: 5, conns: 5}
)

// connZone counts the requests an address has open, across all three limits.
const connZone = "threeax_conn"

// zoneSize is the shared memory of each zone: about 16 000 addresses a
// megabyte, and a zone that fills up evicts the least recently seen.
const zoneSize = "5m"

// limitedLocation is where a request over its limit is answered: the stub, as
// for a miss.
const limitedLocation = "@threeax_limited"

// validate reports a guard that would break nginx or protect nothing.
func (g *Guard) validate() error {
	if g.MissLog == "" || !filepath.IsAbs(g.MissLog) {
		return fmt.Errorf("the miss log %q is not an absolute path", g.MissLog)
	}
	for _, addr := range g.Exempt {
		if _, ok := exemptEntry(addr); !ok {
			return fmt.Errorf("exempt address %q is neither an IP nor a network", addr)
		}
	}
	return nil
}

// exemptEntry normalises an exempt address as geo wants it: an IP, or a
// network in CIDR notation.
func exemptEntry(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if prefix, err := netip.ParsePrefix(value); err == nil {
		return prefix.Masked().String(), true
	}
	if addr, err := netip.ParseAddr(value); err == nil {
		return addr.Unmap().String(), true
	}
	return "", false
}

// ExemptEntry normalises an exemption that may be a network — a front
// trusted address (#228) — as the guard renders it, and says false for
// anything it would refuse: a name, a port, nothing.
func ExemptEntry(value string) (string, bool) { return exemptEntry(value) }

// ExemptAddress turns an address as the chain stores it — an IP, host:port,
// [v6]:port — into the IP a guard can exempt. A host name is not resolved:
// its address can change under the config, and resolving it here would make
// every render a DNS lookup. The false answer is for a name, or for nothing.
func ExemptAddress(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return "", false
	}
	return addr.Unmap().String(), true
}

// exemptList is loopback plus the guard's own, normalised, sorted and without
// duplicates, so the same exemptions always render the same bytes.
func (g *Guard) exemptList() []string {
	seen := map[string]bool{}
	var out []string
	for _, addr := range g.Exempt {
		if entry, ok := exemptEntry(addr); ok && !seen[entry] {
			seen[entry] = true
			out = append(out, entry)
		}
	}
	sort.Strings(out)
	return append([]string{"127.0.0.0/8", "::1"}, out...)
}

// writeHTTPLevel emits what the guard needs at the http {} level: the
// exemptions, the key the limits count by, the zones and the miss log's
// formats. Our file is included from inside http {}, so it can.
func (g *Guard) writeHTTPLevel(b *strings.Builder) {
	b.WriteString("# The limits count per client address (#141). An exempt address — loopback,\n")
	b.WriteString("# the chain's neighbours and, on the panel, mon-server — has an empty key,\n")
	b.WriteString("# which limit_req and limit_conn do not count at all.\n")
	b.WriteString("geo $threeax_exempt {\n    default 0;\n")
	for _, entry := range g.exemptList() {
		fmt.Fprintf(b, "    %s 1;\n", entry)
	}
	b.WriteString("}\n\n")
	b.WriteString("map $threeax_exempt $threeax_limit_key {\n")
	b.WriteString("    1       \"\";\n")
	b.WriteString("    default $binary_remote_addr;\n")
	b.WriteString("}\n\n")
	for _, z := range []zone{zoneSub, zoneAPI, zoneLogin} {
		fmt.Fprintf(b, "limit_req_zone $threeax_limit_key zone=%s:%s rate=%s;\n", z.name, zoneSize, z.rate)
	}
	fmt.Fprintf(b, "limit_conn_zone $threeax_limit_key zone=%s:%s;\n\n", connZone, zoneSize)

	// An upstream's refusal under a secret prefix — an unknown subscription,
	// an API call without a session, a wave without the hop secret — is a
	// miss too. It keeps its own answer: the hops read a 404 as «revoked».
	b.WriteString("map $status $threeax_upstream_miss {\n")
	b.WriteString("    ~^40[0134]$ 1;\n")
	b.WriteString("    default     0;\n")
	b.WriteString("}\n\n")
	// The address comes first and the request last, escaped by nginx, so a
	// crafted path cannot put another address where the jail looks for one.
	b.WriteString("log_format threeax_miss '$remote_addr [$time_local] miss \"$request_method $request_uri\" $status';\n")
	b.WriteString("log_format threeax_limit '$remote_addr [$time_local] limit \"$request_method $request_uri\" $status';\n\n")
}

// writeServerLevel sends a request over its limit to the stub: the same page,
// the same status as a miss.
func (g *Guard) writeServerLevel(b *strings.Builder) {
	b.WriteString("    limit_req_status 429;\n")
	b.WriteString("    limit_conn_status 429;\n")
	b.WriteString("    error_page 429 = " + limitedLocation + ";\n")
	// A limit hit is in the miss log already; the error log stays quiet.
	b.WriteString("    limit_req_log_level warn;\n")
	b.WriteString("    limit_conn_log_level warn;\n\n")
}

// writeLimited emits the location a limited request lands in. try_files
// serves the page in place, so the request ends here and is logged here.
func (g *Guard) writeLimited(b *strings.Builder) {
	fmt.Fprintf(b, "    location %s {\n", limitedLocation)
	fmt.Fprintf(b, "        access_log %s threeax_limit;\n", g.MissLog)
	b.WriteString("        try_files /" + IndexFile + " =404;\n")
	b.WriteString("    }\n\n")
}

// writeDecoy emits a prefix whose unpublished paths answer as the stub and
// count as misses: the panel's base path, of which only the login and the API
// are published.
func (g *Guard) writeDecoy(b *strings.Builder, prefix string) {
	b.WriteString("    # not published here: the stub, and a miss\n")
	fmt.Fprintf(b, "    location %s {\n", prefix)
	if g != nil {
		fmt.Fprintf(b, "        access_log %s threeax_miss;\n", g.MissLog)
	}
	b.WriteString("        try_files /" + IndexFile + " =404;\n")
	b.WriteString("    }\n\n")
}

// writeLimits emits a published location's limits and its miss log.
func (g *Guard) writeLimits(b *strings.Builder, z zone) {
	fmt.Fprintf(b, "        limit_req zone=%s burst=%d nodelay;\n", z.name, z.burst)
	fmt.Fprintf(b, "        limit_conn %s %d;\n", connZone, z.conns)
	fmt.Fprintf(b, "        access_log %s threeax_miss if=$threeax_upstream_miss;\n", g.MissLog)
}
