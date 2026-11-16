package live

import (
	"context"
	"net/http"

	"github.com/gorilla/websocket"
	"github.com/term-llm/term-llm/internal/proxyclient"
)

func dialAttributedWebSocket(dialer *websocket.Dialer, ctx context.Context, endpoint string, header http.Header) (*websocket.Conn, *http.Response, error) {
	return dialer.DialContext(ctx, endpoint, proxyclient.Annotate(endpoint, header))
}
