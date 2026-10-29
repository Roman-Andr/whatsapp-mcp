package tools

// Changes to your own messages and chat state.

import "context"

func editMessage(ctx context.Context, c *Call) (any, error) {
	a := c.Args
	return sendResult(c.Bridge.EditMessage(ctx, a.String("chat_jid"), a.String("message_id"), a.String("new_text"))), nil
}

func deleteMessage(ctx context.Context, c *Call) (any, error) {
	return sendResult(c.Bridge.DeleteMessage(ctx, c.Args.String("chat_jid"), c.Args.String("message_id"))), nil
}

func starMessage(ctx context.Context, c *Call) (any, error) {
	a := c.Args
	return result(c.Bridge.StarMessage(ctx, a.String("chat_jid"), a.String("message_id"), a.Bool("starred"))), nil
}

func markRead(ctx context.Context, c *Call) (any, error) {
	return result(c.Bridge.MarkRead(ctx, c.Args.String("chat_jid"), c.Args.StringSlice("message_ids"))), nil
}

func sendTyping(ctx context.Context, c *Call) (any, error) {
	return result(c.Bridge.SendTyping(ctx, c.Args.String("chat_jid"), c.Args.String("state"))), nil
}
