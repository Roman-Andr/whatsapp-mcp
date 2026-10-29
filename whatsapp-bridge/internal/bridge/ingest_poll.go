package bridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	hub "github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/events"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/store"
)

// matchPollOptions turns the SHA-256 hashes a vote carries back into option
// names. Hashes of unknown options are shown as "?".
func matchPollOptions(options []string, hashes [][]byte) []string {
	names := make([]string, 0, len(hashes))
	for _, h := range hashes {
		name := "?"
		for _, o := range options {
			if sum := sha256.Sum256([]byte(o)); bytes.Equal(sum[:], h) {
				name = o
				break
			}
		}
		names = append(names, name)
	}
	return names
}

// onPollVote decrypts a live poll vote and stores it.
func (b *Bridge) onPollVote(ctx context.Context, evt *events.Message, live bool) {
	update := evt.Message.GetPollUpdateMessage()
	pollID := update.GetPollCreationMessageKey().GetID()
	vote, err := b.client.DecryptPollVote(ctx, evt)
	if err != nil {
		b.log.Warn("failed to decrypt poll vote", "poll_id", pollID, "err", err)
		return
	}
	ts := evt.Info.Timestamp
	if ms := update.GetSenderTimestampMS(); ms > 0 {
		ts = time.UnixMilli(ms)
	}
	b.storePollVote(ctx, evt.Info.Chat, pollID, evt.Info.Sender, evt.Info.SenderAlt, evt.Info.PushName, vote.GetSelectedOptions(), ts, live)
}

// storeHistoryPollVotes stores the votes history sync delivers inside the
// poll message, already decrypted.
func (b *Bridge) storeHistoryPollVotes(ctx context.Context, chat types.JID, pollID string, web *waWeb.WebMessageInfo) {
	for _, u := range web.GetPollUpdates() {
		voter := b.keySender(chat, u.GetPollUpdateMessageKey())
		b.storePollVote(ctx, chat, pollID, voter, types.EmptyJID, "", u.GetVote().GetSelectedOptions(),
			time.UnixMilli(u.GetSenderTimestampMS()), false)
	}
}

func (b *Bridge) storePollVote(ctx context.Context, chat types.JID, pollID string, voter, voterAlt types.JID,
	pushName string, hashes [][]byte, ts time.Time, live bool) {
	if pollID == "" {
		return
	}
	chatJID := b.chatOf(ctx, chat, pollID)
	options, err := b.store.PollOptions(ctx, pollID, chatJID)
	if err != nil {
		b.log.Warn("vote on unknown poll", "poll_id", pollID, "err", err)
		return
	}
	vote := store.PollVote{
		MessageID: pollID, ChatJID: chatJID, VoterJID: voter.ToNonAD().String(),
		VoterName: b.resolveName(ctx, voter, voterAlt, pushName),
		Options:   matchPollOptions(options, hashes), Timestamp: ts,
	}
	if err := b.store.SavePollVote(ctx, vote); err != nil {
		b.log.Warn("failed to store poll vote", "poll_id", pollID, "err", err)
		return
	}
	if live {
		choice := strings.Join(vote.Options, ", ")
		b.log.Info("poll vote", "at", ts.Format(time.DateTime), "voter", vote.VoterName, "choice", choice, "poll_id", pollID)
		b.events.Publish(hub.Event{
			Type: "poll_vote", ChatJID: vote.ChatJID, MessageID: pollID, SenderJID: vote.VoterJID,
			SenderName: vote.VoterName, Content: choice, Timestamp: ts,
		})
	}
}

// keySender is the author of the message a key points to: ourselves, the
// group participant, or in a direct chat the other person.
func (b *Bridge) keySender(chat types.JID, key *waCommon.MessageKey) types.JID {
	if key.GetFromMe() {
		return b.ownJID(chat)
	}
	if p := key.GetParticipant(); p != "" {
		if parsed, err := types.ParseJID(p); err == nil {
			return parsed
		}
	}
	return chat
}
