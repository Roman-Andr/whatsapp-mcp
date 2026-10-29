package render

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/store"
)

// short cuts text to length characters on one line.
func short(text string, length int) string {
	r := []rune(strings.ReplaceAll(text, "\n", " "))
	if len(r) <= length {
		return string(r)
	}
	return string(r[:length-1]) + "…"
}

var mentionRe = regexp.MustCompile(`@(\p{Nd}{6,})`)

// renderMentions shows "@<number>" mentions as "@<name>" when the number is
// a known contact.
func renderMentions(ctx context.Context, text string, r *store.Names) string {
	if r == nil || !strings.Contains(text, "@") {
		return text
	}
	return mentionRe.ReplaceAllStringFunc(text, func(m string) string {
		user := m[1:]
		if name := r.Must(ctx, user, "", ""); name != user {
			return "@" + name
		}
		return m
	})
}

// FormatMessage renders one message on one line (plus continuation lines
// for multi-line text).
//
// The message id and chat JID are always shown: they are what react,
// reply, download and context tools take.
func FormatMessage(ctx context.Context, m *store.Message, showChatInfo bool, r *store.Names) string {
	// The offset is part of the time: the bridge stores each message with
	// the offset it was received in, and without it "12:00" is ambiguous.
	ts := "?"
	if m.Timestamp != nil {
		ts = m.Timestamp.Display()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] ", ts)
	if showChatInfo && store.Deref(m.ChatName) != "" {
		fmt.Fprintf(&b, "Chat: %s ", *m.ChatName)
	}
	fmt.Fprintf(&b, "From: %s: [%s] ", senderName(ctx, m, r), strings.Join(tags(m), " | "))
	if store.Deref(m.QuotedID) != "" {
		author := ""
		if store.Deref(m.QuotedSender) != "" && r != nil {
			author = r.Must(ctx, *m.QuotedSender, "", "") + ": "
		}
		fmt.Fprintf(&b, "(reply to %s %s\"%s\") ", *m.QuotedID, author, short(store.Deref(m.QuotedText), 80))
	}
	b.WriteString(renderMentions(ctx, m.Content, r))
	writeDetails(ctx, &b, m, r)
	b.WriteString("\n")
	return b.String()
}

// senderName is who sent m: "Me", the name r resolves, or (without r) the
// stored name or number.
func senderName(ctx context.Context, m *store.Message, r *store.Names) string {
	switch {
	case m.IsFromMe:
		return "Me"
	case r != nil:
		return r.Must(ctx, m.Sender, store.Deref(m.SenderJID), store.Deref(m.SenderName))
	}
	if name := store.Deref(m.SenderName); name != "" {
		return name
	}
	return m.Sender
}

// tags are the bracketed facts of m: its media, id, chat and state.
func tags(m *store.Message) []string {
	out := []string{"id: " + m.ID, "chat: " + m.ChatJID}
	if m.MediaType != "" {
		out = append([]string{mediaLabel(m)}, out...)
	}
	if m.EditedAt != nil {
		out = append(out, "edited")
	}
	if m.DeletedAt != nil {
		out = append(out, "deleted by sender")
	}
	if m.Starred {
		out = append(out, "starred")
	}
	return out
}

// mediaLabel is the media type with its detail, document name, size and
// duration.
func mediaLabel(m *store.Message) string {
	label := m.MediaType
	if store.Deref(m.MediaDetail) != "" {
		label += fmt.Sprintf(" (%s)", *m.MediaDetail)
	}
	if store.Deref(m.Filename) != "" && m.MediaType == "document" {
		label += " " + *m.Filename
	}
	if m.MediaWidth != nil && *m.MediaWidth != 0 && m.MediaHeight != nil && *m.MediaHeight != 0 {
		label += fmt.Sprintf(" %dx%d", *m.MediaWidth, *m.MediaHeight)
	}
	if m.MediaSeconds != nil && *m.MediaSeconds != 0 {
		s := *m.MediaSeconds
		label += fmt.Sprintf(" %d:%02d", floorDiv(s, 60), s-60*floorDiv(s, 60))
	}
	return label
}

// writeDetails appends the voice transcript, poll votes, event responses
// and reactions of m.
func writeDetails(ctx context.Context, b *strings.Builder, m *store.Message, r *store.Names) {
	if m.Transcript != "" {
		fmt.Fprintf(b, " [voice transcript: %s]", m.Transcript)
	}
	if m.PollVotes != nil {
		parts := make([]string, len(m.PollVotes.Keys))
		for i, option := range m.PollVotes.Keys {
			voters := m.PollVotes.Who[option]
			parts[i] = fmt.Sprintf("%s (%d)", option, len(voters))
			if len(voters) > 0 {
				parts[i] += ": " + strings.Join(voters, ", ")
			}
		}
		fmt.Fprintf(b, " [votes: %s]", strings.Join(parts, "; "))
	}
	if m.EventResponses != nil && len(m.EventResponses.Keys) > 0 {
		parts := make([]string, len(m.EventResponses.Keys))
		for i, answer := range m.EventResponses.Keys {
			who := m.EventResponses.Who[answer]
			parts[i] = fmt.Sprintf("%s (%d): %s", answer, len(who), strings.Join(who, ", "))
		}
		fmt.Fprintf(b, " [responses: %s]", strings.Join(parts, "; "))
	}
	if len(m.Reactions) > 0 {
		parts := make([]string, len(m.Reactions))
		for i, re := range m.Reactions {
			parts[i] = re.Emoji + " " + reactor(ctx, re, r)
		}
		fmt.Fprintf(b, " [reactions: %s]", strings.Join(parts, ", "))
	}
}

// reactor is who reacted: the stored name, else the resolved one.
func reactor(ctx context.Context, re store.Reaction, r *store.Names) string {
	if who := store.Deref(re.SenderName); who != "" {
		return who
	}
	if r != nil {
		return r.Must(ctx, re.SenderJID, "", "")
	}
	return re.SenderJID
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// FormatMessages renders messages one per line, or says there are none.
func FormatMessages(ctx context.Context, messages []*store.Message, showChatInfo bool, r *store.Names) string {
	if len(messages) == 0 {
		return "No messages to display."
	}
	var b strings.Builder
	for _, m := range messages {
		b.WriteString(FormatMessage(ctx, m, showChatInfo, r))
	}
	return b.String()
}
