package xray

import "testing"

// TestUserCallsWithoutConnectionReturnError reproduces the crash of adding
// or removing an enabled client while xray is stopped: Init fails (there is no
// API port), and AddUser / RemoveUser then dereferenced the nil handler
// client. They must return an error instead.
func TestUserCallsWithoutConnectionReturnError(t *testing.T) {
	var api XrayAPI
	if err := api.Init(0); err == nil {
		t.Fatal("Init(0) succeeded, want an error")
	}
	if err := api.AddUser("vless", "in-1", map[string]any{"email": "alice", "id": "aaaaaaaa-0000-0000-0000-000000000001", "flow": ""}); err == nil {
		t.Error("AddUser without a connection returned nil, want an error")
	}
	if err := api.RemoveUser("in-1", "alice"); err == nil {
		t.Error("RemoveUser without a connection returned nil, want an error")
	}
}

// TestInboundCallsWithoutConnectionReturnError: saving an enabled inbound
// while xray is stopped took the same path through AddInbound / DelInbound,
// which dereferenced the nil handler client and panicked (#102, #250). They
// must return an error instead.
func TestInboundCallsWithoutConnectionReturnError(t *testing.T) {
	var api XrayAPI
	if err := api.Init(0); err == nil {
		t.Fatal("Init(0) succeeded, want an error")
	}
	if err := api.AddInbound([]byte(`{"tag":"inbound-1","port":1,"protocol":"vless","settings":{"clients":[],"decryption":"none"}}`)); err == nil {
		t.Error("AddInbound without a connection returned nil, want an error")
	}
	if err := api.DelInbound("inbound-1"); err == nil {
		t.Error("DelInbound without a connection returned nil, want an error")
	}
}
