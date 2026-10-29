package tools

// Media of messages: downloading it, showing images and video frames to
// the model, and transcribing voice messages.

import (
	"context"
	"errors"
	"fmt"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/mediaconv"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/pyfmt"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/render"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/media/transcribe"
)

// fetchMedia downloads a message's media through the bridge, unless the
// chat is hidden.
func fetchMedia(ctx context.Context, c *Call, messageID, chatJID string) *pyfmt.OMap {
	if hidden := c.DB.CheckReadable(ctx, chatJID); hidden != "" {
		return render.Refused(hidden)
	}
	return c.Bridge.Download(ctx, messageID, chatJID)
}

func downloadMedia(ctx context.Context, c *Call) (any, error) {
	r := fetchMedia(ctx, c, c.Args.String("message_id"), c.Args.String("chat_jid"))
	if pyfmt.Truthy(r.Get("success")) {
		return pyfmt.NewOMap("success", true, "message", "Media downloaded successfully", "file_path", r.Get("path"),
			"media_type", r.Get("media_type"), "mimetype", r.Get("mimetype")), nil
	}
	return render.Refused(render.MessageOr(r, "Failed to download media")), nil
}

// viewMedia shows an image or sticker, or a video's first frame.
func viewMedia(ctx context.Context, c *Call) (any, error) {
	id, chat := c.Args.String("message_id"), c.Args.String("chat_jid")
	info := c.DB.GetMediaInfo(ctx, id, chat)
	if info == nil {
		return render.Refused(fmt.Sprintf("Message %s has no media", id)), nil
	}
	if info.MediaType != "image" && info.MediaType != "sticker" && info.MediaType != "video" {
		return render.Refused(fmt.Sprintf("Message %s is a %s, not an image or video; use download_media", id, info.MediaType)), nil
	}
	r := fetchMedia(ctx, c, id, chat)
	if !pyfmt.Truthy(r.Get("success")) {
		return render.Refused(render.MessageOr(r, "Failed to download media")), nil
	}
	path := pyfmt.AsString(r.Get("path"))
	if info.MediaType != "video" {
		return loadImage(c, path)
	}
	frame, err := c.Media.Video.FirstFramePNG(ctx, path)
	if errors.Is(err, mediaconv.ErrVideoFrame) {
		return render.Refused(fmt.Sprintf("Cannot show video %s: %v. The file is at %s", id, err, path)), nil
	}
	if err != nil {
		return nil, err
	}
	data, mime, err := c.Media.Image.ForModel(frame)
	if err != nil {
		return nil, err
	}
	return imageResult{data, mime}, nil
}

// loadImage reads an image file, made small enough for the model.
func loadImage(c *Call, path string) (any, error) {
	data, mime, err := c.Media.ImageForModel(path)
	if err != nil {
		return nil, err
	}
	return imageResult{data, mime}, nil
}

func transcribeVoiceMessage(ctx context.Context, c *Call) (any, error) {
	text, err := transcribeVoice(ctx, c, c.Args.String("message_id"), c.Args.String("chat_jid"), c.Args.String("language"))
	if err != nil {
		return pyfmt.NewOMap("success", false, "message", err.Error()), nil
	}
	return pyfmt.NewOMap("success", true, "transcript", text), nil
}

// transcribeVoice transcribes a voice or audio message; transcripts are
// cached under store/transcripts and then shown by list_messages. The
// error's text is what the client sees.
//
// A fresh transcript starts with its language ("[en] ..."), a cached one
// does not: clients have always seen it this way.
func transcribeVoice(ctx context.Context, c *Call, messageID, chatJID, language string) (string, error) {
	if hidden := c.DB.CheckReadable(ctx, chatJID); hidden != "" {
		return "", errors.New(hidden)
	}
	if cached := c.DB.CachedTranscript(messageID, chatJID); cached != "" {
		return cached, nil
	}
	info := c.DB.GetMediaInfo(ctx, messageID, chatJID)
	if info == nil || info.MediaType != "audio" {
		return "", fmt.Errorf("Message %s is not an audio message", messageID) //nolint:staticcheck // the text clients see, kept verbatim
	}
	r := fetchMedia(ctx, c, messageID, chatJID)
	if !pyfmt.Truthy(r.Get("success")) {
		return "", errors.New(render.MessageOr(r, "Download failed"))
	}
	text, detected, err := c.Media.Transcriber.Transcribe(ctx, pyfmt.AsString(r.Get("path")), language)
	if err != nil {
		return "", err
	}
	if text == "" {
		return "", errors.New("No speech recognised") //nolint:staticcheck // the text clients see, kept verbatim
	}
	if err := transcribe.WriteCached(c.DB.TranscriptsDir(), chatJID, messageID, text); err != nil {
		return "", err
	}
	return fmt.Sprintf("[%s] %s", detected, text), nil
}
