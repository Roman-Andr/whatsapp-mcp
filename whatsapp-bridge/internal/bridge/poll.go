package bridge

import (
	"context"
	"fmt"
	"math"
	"strings"

	"go.mau.fi/whatsmeow/proto/waE2E"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/content"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/jid"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/store"
)

// WhatsApp clients accept 2 to 12 options.
const maxPollOptions = 12

// validatePoll checks a poll before anything is sent and returns the number
// of selectable options.
func validatePoll(req PollRequest) (int, error) {
	if strings.TrimSpace(req.Question) == "" {
		return 0, fmt.Errorf("question is required")
	}
	if len(req.Options) < 2 || len(req.Options) > maxPollOptions {
		return 0, fmt.Errorf("a poll needs 2 to %d options, got %d", maxPollOptions, len(req.Options))
	}
	seen := map[string]bool{}
	for _, o := range req.Options {
		if strings.TrimSpace(o) == "" {
			return 0, fmt.Errorf("poll options must not be empty")
		}
		// Votes identify options by the hash of their name, so two options
		// with the same name could not be told apart.
		if seen[o] {
			return 0, fmt.Errorf("duplicate poll option %q", o)
		}
		seen[o] = true
	}
	selectable := 1
	if req.SelectableCount != nil {
		selectable = *req.SelectableCount
	}
	if selectable < 0 || selectable > len(req.Options) {
		return 0, fmt.Errorf("selectable_count must be between 0 (any number) and %d", len(req.Options))
	}
	return selectable, nil
}

// SendPoll sends a poll and stores it, so votes on it can be read.
func (b *Bridge) SendPoll(ctx context.Context, req PollRequest) SendMessageResponse {
	selectable, err := validatePoll(req)
	if err != nil {
		return failed("%v", err)
	}
	chat, err := jid.ParseRecipient(req.Recipient)
	if err != nil {
		return failed("Error parsing recipient: %v", err)
	}
	if err := b.checkAllowed(ctx, chat); err != nil {
		return blockedResponse(err)
	}
	if !b.Online() {
		return failed("Not connected to WhatsApp")
	}
	// whatsmeow stores the poll's message secret when sending, which is what
	// later decrypts the votes.
	msg := b.client.BuildPollCreation(req.Question, req.Options, selectable)
	resp, err := b.client.SendMessage(ctx, chat, msg)
	if err != nil {
		return failed("Error sending poll: %v", err)
	}
	b.recordSent(ctx, chat, resp, store.Message{Content: content.Text(msg)})
	if err := b.store.SavePoll(ctx, resp.ID, b.storedChat(ctx, chat).String(), req.Question, req.Options, selectable); err != nil {
		b.log.Warn("failed to store poll", "message_id", resp.ID, "err", err)
	}
	return SendMessageResponse{Success: true, Message: fmt.Sprintf("Poll sent to %s", req.Recipient), MessageID: resp.ID, Timestamp: resp.Timestamp}
}

func validateLocation(req LocationRequest) error {
	if math.IsNaN(req.Latitude) || req.Latitude < -90 || req.Latitude > 90 {
		return fmt.Errorf("latitude must be between -90 and 90")
	}
	if math.IsNaN(req.Longitude) || req.Longitude < -180 || req.Longitude > 180 {
		return fmt.Errorf("longitude must be between -180 and 180")
	}
	return nil
}

// SendLocation sends a location pin.
func (b *Bridge) SendLocation(ctx context.Context, req LocationRequest) SendMessageResponse {
	if err := validateLocation(req); err != nil {
		return failed("%v", err)
	}
	chat, err := jid.ParseRecipient(req.Recipient)
	if err != nil {
		return failed("Error parsing recipient: %v", err)
	}
	if err := b.checkAllowed(ctx, chat); err != nil {
		return blockedResponse(err)
	}
	if !b.Online() {
		return failed("Not connected to WhatsApp")
	}
	loc := &waE2E.LocationMessage{
		DegreesLatitude:  new(req.Latitude),
		DegreesLongitude: new(req.Longitude),
	}
	if req.Name != "" {
		loc.Name = new(req.Name)
	}
	if req.Address != "" {
		loc.Address = new(req.Address)
	}
	msg := &waE2E.Message{LocationMessage: loc}
	resp, err := b.client.SendMessage(ctx, chat, msg)
	if err != nil {
		return failed("Error sending location: %v", err)
	}
	b.recordSent(ctx, chat, resp, store.Message{Content: content.Text(msg)})
	return SendMessageResponse{Success: true, Message: fmt.Sprintf("Location sent to %s", req.Recipient), MessageID: resp.ID, Timestamp: resp.Timestamp}
}
