package tools

// Sending messages, files, stickers and statuses. Files are checked here
// first: the bridge sends only from its media roots, and a converted copy
// would land in its outbox and pass that check.

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/bridgeapi"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/mediaconv"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/pyfmt"
)

func sendMessage(ctx context.Context, c *Call) (any, error) {
	a := c.Args
	if a.String("recipient") == "" {
		return pyfmt.NewOMap("success", false, "message", "Recipient must be provided"), nil
	}
	return sendResult(c.Bridge.SendMessage(ctx, a.String("recipient"), a.String("message"),
		a.String("reply_to_message_id"), a.StringSlice("mentions"))), nil
}

// sendable checks a file to send: a recipient, a path (when pathRequired),
// a regular file inside the media roots. It returns the file's absolute
// path, or the outcome of a refused send.
func sendable(ctx context.Context, c *Call, recipient, mediaPath string, pathRequired bool) (string, *bridgeapi.Outcome) {
	refuse := func(msg string) (string, *bridgeapi.Outcome) {
		o := bridgeapi.Fail(msg)
		return "", &o
	}
	if recipient == "" {
		return refuse("Recipient must be provided")
	}
	if pathRequired && mediaPath == "" {
		return refuse("Media path must be provided")
	}
	mediaPath = pyfmt.AbsPath(mediaPath)
	if !mediaconv.IsFile(mediaPath) {
		return refuse("Media file not found: " + mediaPath)
	}
	if refused := mediaconv.CheckSendable(ctx, c.DB, mediaPath); refused != "" {
		return refuse(refused)
	}
	return mediaPath, nil
}

// sendFileTo sends a file with a caption, optionally as a reply.
func sendFileTo(ctx context.Context, c *Call, recipient, mediaPath, caption, replyTo string) bridgeapi.Outcome {
	path, refused := sendable(ctx, c, recipient, mediaPath, true)
	if refused != nil {
		return *refused
	}
	payload := pyfmt.NewOMap("recipient", recipient, "media_path", path, "message", caption)
	if replyTo != "" {
		payload.Set("reply_to_message_id", replyTo)
	}
	return c.Bridge.Send(ctx, payload)
}

func sendFile(ctx context.Context, c *Call) (any, error) {
	a := c.Args
	return sendResult(sendFileTo(ctx, c, a.String("recipient"), a.String("media_path"), a.String("caption"), a.String("reply_to_message_id"))), nil
}

// sendAudioMessage sends audio as a voice message, converted to Ogg/Opus
// unless it is one already.
func sendAudioMessage(ctx context.Context, c *Call) (any, error) {
	recipient := c.Args.String("recipient")
	path, refused := sendable(ctx, c, recipient, c.Args.String("media_path"), true)
	if refused != nil {
		return sendResult(*refused), nil
	}
	if !strings.HasSuffix(path, ".ogg") {
		converted, err := c.Media.Audio.ToOpusOgg(ctx, path, mediaconv.Outbox(ctx, c.DB))
		if err != nil {
			return sendResult(bridgeapi.Fail(fmt.Sprintf("Error converting file to opus ogg: %v", err))), nil
		}
		// The converted copy is ours; leaving it behind filled /tmp over time.
		defer os.Remove(converted)
		path = converted
	}
	return sendResult(c.Bridge.Send(ctx, pyfmt.NewOMap("recipient", recipient, "media_path", path))), nil
}

// sendSticker sends an image as a sticker; non-WebP images are converted
// first. A missing path is reported as a missing file.
func sendSticker(ctx context.Context, c *Call) (any, error) {
	recipient, replyTo := c.Args.String("recipient"), c.Args.String("reply_to_message_id")
	path, refused := sendable(ctx, c, recipient, c.Args.String("media_path"), false)
	if refused != nil {
		return sendResult(*refused), nil
	}
	if !strings.HasSuffix(strings.ToLower(path), ".webp") {
		converted, err := c.Media.Sticker.ToSticker(path, mediaconv.Outbox(ctx, c.DB))
		if err != nil {
			return sendResult(bridgeapi.Fail(fmt.Sprintf("Cannot convert %s to a sticker: %v", path, err))), nil
		}
		defer os.Remove(converted)
		path = converted
	}
	payload := pyfmt.NewOMap("recipient", recipient, "media_path", path)
	if replyTo != "" {
		payload.Set("reply_to_message_id", replyTo)
	}
	return sendResult(c.Bridge.Sticker(ctx, payload)), nil
}

func sendReaction(ctx context.Context, c *Call) (any, error) {
	a := c.Args
	return result(c.Bridge.SendReaction(ctx, a.String("chat_jid"), a.String("message_id"), a.String("emoji"))), nil
}

func sendPoll(ctx context.Context, c *Call) (any, error) {
	a := c.Args
	return sendResult(c.Bridge.SendPoll(ctx, a.String("recipient"), a.String("question"), a.StringSlice("options"), a.Int("selectable_count"))), nil
}

func sendLocation(ctx context.Context, c *Call) (any, error) {
	a := c.Args
	return sendResult(c.Bridge.SendLocation(ctx, a.String("recipient"), a.Float("latitude"), a.Float("longitude"),
		a.String("name"), a.String("address"))), nil
}

func votePoll(ctx context.Context, c *Call) (any, error) {
	a := c.Args
	return sendResult(c.Bridge.VotePoll(ctx, a.String("chat_jid"), a.String("message_id"), a.StringSlice("options"))), nil
}

func sendContact(ctx context.Context, c *Call) (any, error) {
	a := c.Args
	return sendResult(c.Bridge.SendContact(ctx, a.String("recipient"), a.String("name"), a.String("phone"), a.String("vcard"))), nil
}

func forwardMessage(ctx context.Context, c *Call) (any, error) {
	a := c.Args
	return sendResult(c.Bridge.ForwardMessage(ctx, a.String("chat_jid"), a.String("message_id"), a.String("recipient"))), nil
}

// postStatus posts a text, image or video status, visible to the contacts
// the status privacy settings allow.
func postStatus(ctx context.Context, c *Call) (any, error) {
	a := c.Args
	if mediaPath := a.String("media_path"); mediaPath != "" {
		return sendResult(sendFileTo(ctx, c, bridgeapi.StatusJID, mediaPath, a.String("caption"), "")), nil
	}
	if a.String("text") == "" {
		return sendResult(bridgeapi.Fail("text or media_path must be given")), nil
	}
	return sendResult(c.Bridge.Send(ctx, pyfmt.NewOMap("recipient", bridgeapi.StatusJID, "message", a.String("text")))), nil
}
