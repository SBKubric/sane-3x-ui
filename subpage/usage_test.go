package subpage

import (
	"testing"
	"time"
)

func TestParseUserinfo(t *testing.T) {
	u := ParseUserinfo("upload=1048576; download=1048576; total=10485760; expire=1893456000")
	if u != (Usage{Known: true, Up: 1048576, Down: 1048576, Total: 10485760, Expire: 1893456000}) {
		t.Fatalf("ParseUserinfo = %+v", u)
	}
	if u := ParseUserinfo(""); u.Known {
		t.Errorf("an absent header is known usage: %+v", u)
	}
	if u := ParseUserinfo("upload=x; junk; expire=-2592000"); !u.Known || u.Expire != -2592000 || u.Up != 0 {
		t.Errorf("ParseUserinfo(junk) = %+v", u)
	}
}

func TestUsageView(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if v := (Usage{}).view(now); v != nil {
		t.Errorf("unknown usage has a view: %+v", v)
	}
	v := Usage{Known: true, Up: 1 << 20, Down: 1 << 20}.view(now)
	if v.Used != "2.00MB" || v.Total != "∞" || v.Remained != "" || v.Expire != "" || !v.Active {
		t.Errorf("unlimited view = %+v", v)
	}
	v = Usage{Known: true, Up: 3 << 30, Total: 2 << 30}.view(now)
	if v.Active || v.Remained != "0.00B" {
		t.Errorf("over quota view = %+v", v)
	}
	v = Usage{Known: true, Expire: now.Add(-time.Hour).Unix()}.view(now)
	if v.Active || v.Expire != "2026-10-01" {
		t.Errorf("expired view = %+v", v)
	}
	v = Usage{Known: true, Expire: -30 * 86400}.view(now)
	if !v.Active || v.ExpireDays != 30 || v.Expire != "" {
		t.Errorf("term from the first connection = %+v", v)
	}
	if v := (Usage{Known: true, Disabled: true}).view(now); v.Active {
		t.Errorf("disabled view = %+v", v)
	}
}
