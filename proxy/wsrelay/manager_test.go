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
	key := manager.poolKey(account.ID(), wsURL)

	session := NewSession(account.ID(), manager)
	session.SetConnected(true)
	conn := &WsConnection{
		session:  session,
		URL:      wsURL,
		httpResp: &http.Response{StatusCode: http.StatusSwitchingProtocols},
	}
	conn.SetState(StateConnected)
	conn.Touch()
	manager.connections.Store(key, conn)
	manager.sessions.Store(key, session)

	got, err := manager.AcquireConnection(context.Background(), account, wsURL, http.Header{}, "")
	if err != nil {
		t.Fatalf("AcquireConnection() error = %v", err)
	}
	if got != conn {
		t.Fatal("expected existing connection to be reused")
	}
}
