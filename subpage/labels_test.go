package subpage

import (
	"encoding/base64"
	"testing"
)

func TestLinkLabel(t *testing.T) {
	vmess := "vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"v":"2","ps":"de","add":"vpn.example.com","port":"443","id":"x","net":"ws"}`))
	for _, tc := range []struct{ link, want string }{
		{"vless://id@vpn.example.com:443?type=xhttp&security=reality&path=%2Fapi&mode=auto#nl", "VLESS + XHTTP"},
		{"vless://id@vpn.example.com:443?security=reality&type=tcp#nl", "VLESS + TCP"},
		{"vless://id@vpn.example.com:443?security=reality#no-type", "VLESS + TCP"},
		{"vless://id@vpn.example.com:443?type=raw#raw", "VLESS + TCP"},
		{"vless://id@vpn.example.com:443?type=splithttp#old", "VLESS + XHTTP"},
		{"VLESS://id@vpn.example.com:443?type=XHTTP#upper", "VLESS + XHTTP"},
		{"vless://id@vpn.example.com:443?type=grpc#g", "VLESS + gRPC"},
		{"trojan://pw@vpn.example.com:443?type=ws#t", "Trojan + WS"},
		{"trojan://pw@vpn.example.com:443?type=httpupgrade#t", "Trojan + HTTPUpgrade"},
		{"vless://id@vpn.example.com:443?type=kcp#k", "VLESS + mKCP"},
		{vmess, "VMess + WS"},
		{"ss://YWVzLTI1Ni1nY206cHc@vpn.example.com:443#ss", "Shadowsocks"},
		{"hysteria2://pw@vpn.example.com:443#h", "Hysteria2"},
		{"hy2://pw@vpn.example.com:443#h", "Hysteria2"},
		{"vless://id@vpn.example.com:443?type=weird#w", "VLESS + WEIRD"},
		{"vmess://not-base64!", "VMess"},
		{"something://else", ""},
		{"", ""},
	} {
		if got := LinkLabel(tc.link); got != tc.want {
			t.Errorf("LinkLabel(%q) = %q, want %q", tc.link, got, tc.want)
		}
	}
}

const (
	awg1Conf = "[Interface]\nPrivateKey = k\nAddress = 10.8.0.2/32\nJc = 4\nJmin = 40\nJmax = 70\nS1 = 0\nS2 = 0\nH1 = 1\nH2 = 2\nH3 = 3\nH4 = 4\n\n[Peer]\nPublicKey = p\nEndpoint = 192.0.2.1:51820\n"
	awg2Conf = "[Interface]\nPrivateKey = k\nJc = 4\nS1 = 20\nS2 = 30\nS3 = 12\nS4 = 16\nH1 = 100000-800000\nI1 = <r 128>\n\n[Peer]\nPublicKey = p\n"
	awg3Conf = "[Interface]\nPrivateKey = k\nS3 = 20\nS4 = 16\nI1 = <r 128>\nHeaderProtectionKey = aGVsbG8=\nRekeyAfterTime = 100-130\n\n[Peer]\nPublicKey = p\n"
)

func TestTunnelLabel(t *testing.T) {
	for _, tc := range []struct{ kind, conf, want string }{
		{"awg", awg3Conf, "AWG 3"},
		{"awg", "[Interface]\nPrivateKey = k\nDisableCookies = true\n", "AWG 3"},
		{"awg", "[Interface]\nPrivateKey = k\nrandomtrailers = 1\n", "AWG 3"},
		{"awg", awg2Conf, "AWG 2"},
		{"awg", "[Interface]\nPrivateKey = k\nH1 = 1-2\n", "AWG 2"},
		{"awg", awg1Conf, "AWG 1"},
		// A key named in [Peer] is not an interface parameter.
		{"awg", "[Interface]\nPrivateKey = k\n\n[Peer]\nHeaderProtectionKey = x\n", "AWG 1"},
		{"wg", "[Interface]\nPrivateKey = k\n", "WireGuard"},
		{"other", "", ""},
	} {
		if got := TunnelLabel(tc.kind, tc.conf); got != tc.want {
			t.Errorf("TunnelLabel(%q, %q) = %q, want %q", tc.kind, tc.conf, got, tc.want)
		}
	}
}

func TestLinkName(t *testing.T) {
	vmess := "vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"ps":"de-1","add":"vpn.example.com"}`))
	for _, tc := range []struct {
		link string
		idx  int
		want string
	}{
		{"vless://id@vpn.example.com:443?type=xhttp#nl%20main", 0, "nl main"},
		{vmess, 1, "de-1"},
		{"ss://abc@vpn.example.com:443#ss-1", 2, "ss-1"},
		{"vless://id@vpn.example.com:443?type=tcp", 3, "#4"},
		{"vless://id@vpn.example.com:443#%zz", 4, "%zz"},
	} {
		if got := LinkName(tc.link, tc.idx); got != tc.want {
			t.Errorf("LinkName(%q, %d) = %q, want %q", tc.link, tc.idx, got, tc.want)
		}
	}
}
