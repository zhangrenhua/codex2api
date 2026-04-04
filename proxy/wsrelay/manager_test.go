package wsrelay

import (
	"context"
	"net/http"
	"testing"

	"github.com/codex2api/auth"
)

func TestAcquireConnectionReusesIdleConnectedConnection(t *testing.T) {
	manager := NewManager()
	t.Cleanup(manager.Stop)

	account := &auth.Account{DBID: 42}
	wsURL := "wss://example.test/responses"
	key := manager.poolKey(account.ID(), wsURL, "session-1")

	session := NewSession(account.ID(), manager)
	session.SetConnected(true)
	conn := &WsConnection{
		session:  session,
		URL:      wsURL,
		PoolKey:  key,
		httpResp: &http.Response{StatusCode: http.StatusSwitchingProtocols},
	}
	conn.SetState(StateConnected)
	conn.Touch()
	manager.connections.Store(key, conn)
	manager.sessions.Store(key, session)

	got, err := manager.AcquireConnection(context.Background(), account, wsURL, "session-1", http.Header{}, "")
	if err != nil {
		t.Fatalf("AcquireConnection() error = %v", err)
	}
	if got != conn {
		t.Fatal("expected existing connection to be reused")
	}
}

func TestPoolKeyIncludesSessionKey(t *testing.T) {
	manager := NewManager()
	t.Cleanup(manager.Stop)

	keyA := manager.poolKey(42, "wss://example.test/responses", "session-a")
	keyB := manager.poolKey(42, "wss://example.test/responses", "session-b")
	if keyA == keyB {
		t.Fatal("expected different session keys to produce different pool keys")
	}
}

func TestPoolKeyKeepsSameSessionStable(t *testing.T) {
	manager := NewManager()
	t.Cleanup(manager.Stop)

	keyA := manager.poolKey(42, "wss://example.test/responses", "session-a")
	keyB := manager.poolKey(42, "wss://example.test/responses", "session-a")
	if keyA != keyB {
		t.Fatal("expected identical session keys to produce the same pool key")
	}
}
