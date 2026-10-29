package tools

import (
	"context"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/export"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/pyfmt"
)

func waitForEvents(ctx context.Context, c *Call) (any, error) {
	a := c.Args
	return c.Bridge.WaitForEvents(ctx, a.Int("timeout_seconds"), a.String("chat_jid"), a.Int("max_events"), c.DB.EventFilter), nil
}

// exportChat writes the chat to a file under store/exports (internal/export),
// downloading its media as download_media does.
func exportChat(ctx context.Context, c *Call) (any, error) {
	a := c.Args
	download := func(ctx context.Context, messageID, chatJID string) *pyfmt.OMap {
		return fetchMedia(ctx, c, messageID, chatJID)
	}
	return export.Chat(ctx, c.DB, download, a.String("chat_jid"), a.String("format"), a.String("after"), a.String("before"),
		a.Bool("include_media"), ""), nil
}
