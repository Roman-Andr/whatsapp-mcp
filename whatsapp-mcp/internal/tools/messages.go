package tools

import (
	"context"
	"time"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/bridgeapi"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/render"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/store"
)

func listMessages(ctx context.Context, c *Call) (any, error) {
	a := c.Args
	return render.ListMessages(ctx, c.DB, store.ListOpts{
		After: a.String("after"), Before: a.String("before"), SenderPhoneNumber: a.String("sender_phone_number"),
		ChatJID: a.String("chat_jid"), Query: a.String("query"), Limit: a.Int("limit"), Page: a.Int("page"),
		IncludeContext: a.Bool("include_context"), ContextBefore: a.Int("context_before"),
		ContextAfter: a.Int("context_after"), StarredOnly: a.Bool("starred_only"),
	})
}

func getLastInteraction(ctx context.Context, c *Call) (any, error) {
	return render.LastInteraction(ctx, c.DB, c.Args.String("jid")), nil
}

func getMessageContext(ctx context.Context, c *Call) (any, error) {
	a := c.Args
	return render.MessageContext(ctx, c.DB, a.String("message_id"), a.Int("before"), a.Int("after"), a.String("chat_jid")), nil
}

// listStatuses lists status updates (stories) of the last hours.
func listStatuses(ctx context.Context, c *Call) (any, error) {
	after := time.Now().UTC().Add(-time.Duration(max(1, c.Args.Int("hours"))) * time.Hour)
	o := store.DefaultListOpts()
	o.ChatJID = bridgeapi.StatusJID
	o.After = after.Format("2006-01-02T15:04:05.000000+00:00")
	o.Limit = c.Args.Int("limit")
	o.IncludeContext = false
	return render.ListMessages(ctx, c.DB, o)
}

// requestChatHistory asks the phone for older messages of a readable chat.
func requestChatHistory(ctx context.Context, c *Call) (any, error) {
	chatJID := c.Args.String("chat_jid")
	if chatJID == "" {
		return result(bridgeapi.Fail("chat_jid must be provided")), nil
	}
	if hidden := c.DB.CheckReadable(ctx, chatJID); hidden != "" {
		return result(bridgeapi.Fail(hidden)), nil
	}
	return result(c.Bridge.History(ctx, chatJID, c.Args.Int("count"), c.Args.String("before_message_id"))), nil
}
