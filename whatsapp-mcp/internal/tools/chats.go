package tools

import (
	"context"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/store"
)

func listChats(ctx context.Context, c *Call) (any, error) {
	a := c.Args
	return c.DB.ListChats(ctx, store.ChatOpts{Query: a.String("query"), Limit: a.Int("limit"), Page: a.Int("page"),
		IncludeLastMessage: a.Bool("include_last_message"), SortBy: a.String("sort_by"), Label: a.String("label")}), nil
}

func listUnreadChats(ctx context.Context, c *Call) (any, error) {
	return c.DB.ListUnreadChats(ctx, c.Args.Int("limit")), nil
}

func getChat(ctx context.Context, c *Call) (any, error) {
	return c.DB.GetChat(ctx, c.Args.String("chat_jid"), c.Args.Bool("include_last_message")), nil
}

func getDirectChatByContact(ctx context.Context, c *Call) (any, error) {
	return c.DB.GetDirectChatByContact(ctx, c.Args.String("sender_phone_number")), nil
}

func getContactChats(ctx context.Context, c *Call) (any, error) {
	return c.DB.GetContactChats(ctx, c.Args.String("jid"), c.Args.Int("limit"), c.Args.Int("page")), nil
}
