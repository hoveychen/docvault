package config

import "testing"

// set applies env vars for one test and clears them afterward.
func set(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func TestLoadFeishuConnections_JSONMulti(t *testing.T) {
	set(t, map[string]string{
		"DOCVAULT_FEISHU_CONNECTIONS": `[{"key":"acme","label":"Acme","app_id":"a","app_secret":"s1","domain":"lark"},{"key":"globex","app_id":"b","app_secret":"s2"}]`,
	})
	conns, err := loadFeishuConnections()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(conns) != 2 {
		t.Fatalf("want 2 connections, got %d", len(conns))
	}
	if conns[0].Key != "acme" || conns[0].Domain != "lark" || conns[0].Label != "Acme" {
		t.Errorf("conn[0] wrong: %+v", conns[0])
	}
	// globex omitted domain+label -> defaults
	if conns[1].Domain != "feishu" {
		t.Errorf("want default domain feishu, got %q", conns[1].Domain)
	}
	if conns[1].Label != "飞书" {
		t.Errorf("want default label 飞书, got %q", conns[1].Label)
	}
}

func TestLoadFeishuConnections_LegacySingle(t *testing.T) {
	set(t, map[string]string{
		"DOCVAULT_FEISHU_APP_ID":     "cli_x",
		"DOCVAULT_FEISHU_APP_SECRET": "secret",
		"DOCVAULT_FEISHU_DOMAIN":     "lark",
	})
	conns, err := loadFeishuConnections()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(conns) != 1 {
		t.Fatalf("want 1 connection, got %d", len(conns))
	}
	if conns[0].Key != "feishu" || conns[0].Domain != "lark" || conns[0].Label != "Lark" {
		t.Errorf("legacy conn wrong: %+v", conns[0])
	}
}

func TestLoadFeishuConnections_DuplicateKey(t *testing.T) {
	set(t, map[string]string{
		"DOCVAULT_FEISHU_CONNECTIONS": `[{"key":"dup","app_id":"a","app_secret":"s"},{"key":"dup","app_id":"b","app_secret":"s"}]`,
	})
	if _, err := loadFeishuConnections(); err == nil {
		t.Fatal("expected duplicate-key error, got nil")
	}
}

func TestLoadFeishuConnections_MissingFields(t *testing.T) {
	set(t, map[string]string{
		"DOCVAULT_FEISHU_CONNECTIONS": `[{"key":"x","app_id":""}]`,
	})
	if _, err := loadFeishuConnections(); err == nil {
		t.Fatal("expected missing-field error, got nil")
	}
}

func TestLoadFeishuConnections_None(t *testing.T) {
	conns, err := loadFeishuConnections()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(conns) != 0 {
		t.Fatalf("want 0 connections, got %d", len(conns))
	}
}
