package tools

// Tool scenarios that need the store's read allowlist, media roots or
// conversions next to the bridge, against the shared fixture rows.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/mediaconv"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/pyfmt"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/storetest"
)

// handle calls a handler directly with args.
func handle(t *testing.T, s *testStore, h handler, args Args) any {
	t.Helper()
	v, err := h(t.Context(), s.call(args))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// compact is a handler's *pyfmt.OMap result as compact JSON.
func compact(t *testing.T, v any) string {
	t.Helper()
	m, ok := v.(*pyfmt.OMap)
	if !ok {
		t.Fatalf("got %T", v)
	}
	return pyfmt.Compact(m)
}

type fakeAudio struct{ out string }

func (f fakeAudio) ToOpusOgg(context.Context, string, string) (string, error) { return f.out, nil }

type failingAudio struct{}

func (failingAudio) ToOpusOgg(context.Context, string, string) (string, error) {
	return "", errors.New("ffmpeg is not installed")
}

func TestSendAudio(t *testing.T) {
	t.Parallel()
	t.Run("removes the converted file", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		b := storetest.NewBridge(t, s.Fixture)
		src := writeFile(t, filepath.Join(t.TempDir(), "voice.mp3"), []byte("x"))
		converted := writeFile(t, filepath.Join(t.TempDir(), "converted.ogg"), []byte("OggS"))
		s.media.Audio = fakeAudio{converted}
		if got := compact(t, handle(t, s, sendAudioMessage, Args{"recipient": group, "media_path": src})); got != `{"success": true, "message": "ok", "message_id": "NEW"}` {
			t.Errorf("got %s", got)
		}
		if got := b.Calls()[0].Payload["media_path"]; got != converted {
			t.Errorf("sent %v", got)
		}
		if _, err := os.Stat(converted); !os.IsNotExist(err) {
			t.Errorf("converted file left behind: %v", err)
		}
	})
	t.Run("reports a failed conversion", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		s.media.Audio = failingAudio{}
		src := writeFile(t, filepath.Join(t.TempDir(), "voice.mp3"), []byte("x"))
		want := `{"success": false, "message": "Error converting file to opus ogg: ffmpeg is not installed", "message_id": null}`
		if got := compact(t, handle(t, s, sendAudioMessage, Args{"recipient": group, "media_path": src})); got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	})
}

type fakeSticker struct{ out string }

func (f fakeSticker) ToSticker(string, string) (string, error) {
	return f.out, os.WriteFile(f.out, []byte("RIFF"), 0o600)
}

type failingSticker struct{}

func (failingSticker) ToSticker(string, string) (string, error) {
	return "", errors.New("decode image: bad")
}

func TestSendSticker(t *testing.T) {
	t.Parallel()
	t.Run("converts other images", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		b := storetest.NewBridge(t, s.Fixture)
		png := writeFile(t, filepath.Join(t.TempDir(), "logo.PNG"), []byte("png"))
		converted := filepath.Join(t.TempDir(), "sticker.webp")
		s.media.Sticker = fakeSticker{converted}
		handle(t, s, sendSticker, Args{"recipient": group, "media_path": png})
		if got := b.Summary(); got != jsonOf(t, `[["sticker", {"recipient": "`+group+`", "media_path": "`+converted+`"}]]`) {
			t.Errorf("got %s", got)
		}
		if _, err := os.Stat(converted); !os.IsNotExist(err) {
			t.Errorf("converted sticker left behind: %v", err)
		}
	})
	t.Run("reports a failed conversion", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		s.media.Sticker = failingSticker{}
		png := writeFile(t, filepath.Join(t.TempDir(), "logo.png"), []byte("png"))
		want := `{"success": false, "message": "Cannot convert ` + png + ` to a sticker: decode image: bad", "message_id": null}`
		if got := compact(t, handle(t, s, sendSticker, Args{"recipient": group, "media_path": png})); got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	})
	t.Run("a missing path is a missing file", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		got := compact(t, handle(t, s, sendSticker, Args{"recipient": group, "media_path": ""}))
		if want := `{"success": false, "message": "Media file not found: ` + pyfmt.AbsPath("") + `", "message_id": null}`; got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	})
}

func TestSendChecks(t *testing.T) {
	t.Parallel()
	file := writeFile(t, filepath.Join(t.TempDir(), "r.pdf"), []byte("%PDF"))
	cases := []struct {
		name string
		h    handler
		args Args
		want string
	}{
		{"file without recipient", sendFile, Args{"recipient": "", "media_path": file}, "Recipient must be provided"},
		{"file without path", sendFile, Args{"recipient": group, "media_path": ""}, "Media path must be provided"},
		{"audio without path", sendAudioMessage, Args{"recipient": group, "media_path": ""}, "Media path must be provided"},
		{"sticker without recipient", sendSticker, Args{"recipient": "", "media_path": file}, "Recipient must be provided"},
		{"status of a missing file", postStatus, Args{"media_path": "/nonexistent/x.jpg"}, "Media file not found: /nonexistent/x.jpg"},
		{"a directory", sendFile, Args{"recipient": group, "media_path": filepath.Dir(file)}, "Media file not found: " + filepath.Dir(file)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			want := `{"success": false, "message": "` + tc.want + `", "message_id": null}`
			if got := compact(t, handle(t, newStore(t), tc.h, tc.args)); got != want {
				t.Errorf("got %s, want %s", got, want)
			}
		})
	}
}

// recordingConverter records the directories it converts into.
type recordingConverter struct{ dirs []string }

func (r *recordingConverter) ToOpusOgg(_ context.Context, _, dir string) (string, error) {
	r.dirs = append(r.dirs, dir)
	out := filepath.Join(dir, "converted.ogg")
	return out, os.WriteFile(out, []byte("OggS"), 0o600)
}

func (r *recordingConverter) ToSticker(_, dir string) (string, error) {
	r.dirs = append(r.dirs, dir)
	out := filepath.Join(dir, "converted.webp")
	return out, os.WriteFile(out, []byte("RIFF"), 0o600)
}

func TestMediaRootsAreCheckedBeforeConverting(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	b := storetest.NewBridge(t, s.Fixture)
	tmp := t.TempDir()
	root := filepath.Join(tmp, "outbox")
	secret := writeFile(t, filepath.Join(tmp, "private", "photo.png"), []byte("x"))
	writeFile(t, filepath.Join(root, ".git", "config.mp3"), []byte("x"))
	inside := writeFile(t, filepath.Join(root, "voice.mp3"), []byte("x"))
	if err := os.Symlink(secret, filepath.Join(root, "escape.png")); err != nil {
		t.Fatal(err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	s.PublishMediaRoots([]string{realRoot}, realRoot)
	conv := &recordingConverter{}
	s.media.Audio, s.media.Sticker = conv, conv

	sends := map[string]handler{"send_file": sendFile, "send_audio_message": sendAudioMessage, "send_sticker": sendSticker}
	for _, p := range []string{secret, filepath.Join(root, "escape.png"), filepath.Join(root, "sub", "..", "..", "private", "photo.png")} {
		for name, h := range sends {
			msg := fmt.Sprintf("refusing to send %s: it is outside the media roots (%s). "+
				"Copy it into one of them, or add its directory to WHATSAPP_MEDIA_ROOTS", pyfmt.AbsPath(p), realRoot)
			if got := handle(t, s, h, Args{"recipient": group, "media_path": p}).(*pyfmt.OMap).Get("message"); got != msg {
				t.Errorf("%s %s: %v", name, p, got)
			}
		}
	}
	hidden := filepath.Join(root, ".git", "config.mp3")
	if got := handle(t, s, sendAudioMessage, Args{"recipient": group, "media_path": hidden}).(*pyfmt.OMap).Get("message"); got !=
		"refusing to send "+hidden+": hidden files and directories cannot be sent" {
		t.Errorf("hidden: %v", got)
	}
	if len(conv.dirs) != 0 || len(b.Calls()) != 0 {
		t.Fatalf("refused files were converted (%v) or sent (%s)", conv.dirs, b.Summary())
	}
	// Inside the root the file is converted into the outbox.
	if got := handle(t, s, sendAudioMessage, Args{"recipient": group, "media_path": inside}).(*pyfmt.OMap).Get("success"); got != true {
		t.Fatalf("not sent: %s", b.Summary())
	}
	if !reflect.DeepEqual(conv.dirs, []string{realRoot}) {
		t.Errorf("converted into %v", conv.dirs)
	}
}

type fakeTranscriber struct{ calls int }

func (f *fakeTranscriber) Transcribe(context.Context, string, string) (string, string, error) {
	f.calls++
	return "move the plow", "en", nil
}

type silentTranscriber struct{}

func (silentTranscriber) Transcribe(context.Context, string, string) (string, string, error) {
	return "", "en", nil
}

func TestTranscription(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T) *testStore {
		t.Helper()
		s := newStore(t)
		s.Exec(`INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me, media_type)
			VALUES ('V2', '` + group + `', '5', '', '2026-10-27 13:00:00+03:00', 0, 'audio')`)
		b := storetest.NewBridge(t, s.Fixture)
		b.Answer = func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
			w.Write([]byte(`{"success": true, "path": "/nonexistent/voice.ogg"}`))
		}
		return s
	}
	args := Args{"message_id": "V2", "chat_jid": group, "language": ""}
	t.Run("is cached privately, and shown without its language", func(t *testing.T) {
		t.Parallel()
		s := setup(t)
		fake := &fakeTranscriber{}
		s.media.Transcriber = fake
		if got := compact(t, handle(t, s, transcribeVoiceMessage, args)); got != `{"success": true, "transcript": "[en] move the plow"}` {
			t.Fatalf("got %s", got)
		}
		if got := compact(t, handle(t, s, transcribeVoiceMessage, args)); got != `{"success": true, "transcript": "move the plow"}` || fake.calls != 1 {
			t.Errorf("not cached: %s, %d calls", got, fake.calls)
		}
		path := filepath.Join(s.Dir, "transcripts", group+"_V2.txt")
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Errorf("transcript mode %v", st.Mode().Perm())
		}
		if dir, _ := os.Stat(filepath.Dir(path)); dir.Mode().Perm() != 0o700 {
			t.Errorf("transcripts dir mode %v", dir.Mode().Perm())
		}
	})
	t.Run("no speech", func(t *testing.T) {
		t.Parallel()
		s := setup(t)
		s.media.Transcriber = silentTranscriber{}
		if got := compact(t, handle(t, s, transcribeVoiceMessage, args)); got != `{"success": false, "message": "No speech recognised"}` {
			t.Errorf("got %s", got)
		}
	})
	t.Run("the audio cannot be read", func(t *testing.T) {
		t.Parallel()
		s := setup(t)
		want := `{"success": false, "message": "open /nonexistent/voice.ogg: no such file or directory"}`
		if got := compact(t, handle(t, s, transcribeVoiceMessage, args)); got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	})
	t.Run("download fails", func(t *testing.T) {
		t.Parallel()
		s := setup(t)
		s.Config.APIBaseURL = storetest.Unreachable
		got := handle(t, s, transcribeVoiceMessage, args).(*pyfmt.OMap)
		if got.Get("success") != false || !strings.HasPrefix(pyfmt.AsString(got.Get("message")), "Request error (is the bridge running?): ") {
			t.Errorf("got %s", pyfmt.Compact(got))
		}
	})
}

func TestReadAllowlistHidesOtherChatsEverywhere(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	// The bridge publishes its list; the direct chat is listed by phone
	// number and stored under its LID, which the LID map connects.
	s.PublishReadAllowlist("18046727669@s.whatsapp.net")
	b := storetest.NewBridge(t, s.Fixture)
	hidden := "Chat " + group + " is not in the read allowlist (WHATSAPP_READ_ALLOWLIST)"
	refused := `{"success": false, "message": "` + hidden + `"}`
	cases := []struct {
		name string
		h    handler
		args Args
		want string
	}{
		{"download_media", downloadMedia, Args{"message_id": "A3", "chat_jid": group}, refused},
		{"get_group_info", getGroupInfo, Args{"chat_jid": group}, refused},
		{"get_group_join_requests", getGroupJoinRequests, Args{"chat_jid": group}, refused},
		{"list_community_groups", listCommunityGroups, Args{"community_jid": group}, refused},
		{"get_channel_messages", getChannelMessages, Args{"channel_jid": group}, refused},
		{"get_presence", getPresence, Args{"jid": group}, refused},
		{"get_contact_info", getContactInfo, Args{"jid": group}, refused},
		{"request_chat_history", requestChatHistory, Args{"chat_jid": group}, refused},
		{"transcribe_voice_message", transcribeVoiceMessage, Args{"message_id": "A3", "chat_jid": group}, refused},
		{"view_media", viewMedia, Args{"message_id": "A3", "chat_jid": group}, `{"success": false, "message": "Message A3 has no media"}`},
		{"get_chat", getChat, Args{"chat_jid": group}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := handle(t, s, tc.h, tc.args)
			got := ""
			if m, ok := v.(*pyfmt.OMap); ok {
				got = pyfmt.Compact(m)
			}
			if got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
	if len(b.Calls()) != 0 {
		t.Errorf("bridge called: %s", b.Summary())
	}
}

func TestBridgeListsAreFilteredByTheReadAllowlist(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		h        handler
		key      string
		allow    string
		listBody string
		want     string
	}{
		{"blocked contacts", listBlockedContacts, "blocked", "+18046727669",
			`[{"jid": "` + group + `"}, {"jid": "18046727669@s.whatsapp.net"}]`, `[{"jid": "18046727669@s.whatsapp.net"}]`},
		{"blocked contacts with a bad allowlist", listBlockedContacts, "blocked", "x",
			`[{"jid": "` + group + `"}]`, `[{"jid": "` + group + `"}]`},
		{"channels", listChannels, "channels", group, `[{"jid": "1@newsletter"}, {"jid": "` + group + `"}, "odd"]`, `[{"jid": "` + group + `"}]`},
		// A misconfigured allowlist lets both lists through (PLAN.md §1.5 M1).
		{"channels with a bad allowlist", listChannels, "channels", "x", `[{"jid": "1@newsletter"}]`, `[{"jid": "1@newsletter"}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newStore(t)
			b := storetest.NewBridge(t, s.Fixture)
			b.Answer = func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
				w.Write([]byte(`{"success": true, "` + tc.key + `": ` + tc.listBody + `}`))
			}
			s.Config.ReadAllowlist = tc.allow
			got := handle(t, s, tc.h, Args{}).(*pyfmt.OMap)
			if pyfmt.Compact(got.Get(tc.key)) != tc.want {
				t.Errorf("got %s, want %s", pyfmt.Compact(got.Get(tc.key)), tc.want)
			}
		})
	}
}

func TestStatusesOfTheLastHours(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	recent := time.Now().UTC().Add(-2 * time.Hour).Format("2006-01-02 15:04:05+00:00")
	old := time.Now().UTC().Add(-30 * time.Hour).Format("2006-01-02 15:04:05+00:00")
	s.Exec("INSERT INTO chats (jid, name, last_message_time) VALUES ('status@broadcast', 'Status updates', ?)", recent)
	s.Exec("INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me) VALUES ('S1', 'status@broadcast', '5', 'New depot open', ?, 0)", recent)
	s.Exec("INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me) VALUES ('S0', 'status@broadcast', '5', 'Expired', ?, 0)", old)
	out := handle(t, s, listStatuses, Args{"hours": 24, "limit": 50}).(string)
	if ids := storetest.IDsIn(out); !reflect.DeepEqual(ids, []string{"S1"}) {
		t.Errorf("got %v in:\n%s", ids, out)
	}
}

func TestPostStatusWithMedia(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	b := storetest.NewBridge(t, s.Fixture)
	pic := writeFile(t, filepath.Join(t.TempDir(), "p.jpg"), []byte("jpg"))
	handle(t, s, postStatus, Args{"media_path": pic, "caption": "Depot"})
	if got := b.Summary(); got != jsonOf(t, `[["send", {"recipient": "status@broadcast", "media_path": "`+pic+`", "message": "Depot"}]]`) {
		t.Errorf("got %s", got)
	}
}

type fakeVideo struct {
	frame []byte
	err   error
}

func (f fakeVideo) FirstFramePNG(context.Context, string) ([]byte, error) { return f.frame, f.err }

func TestViewMedia(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T, path string, video mediaconv.VideoFramer) *mcp.ClientSession {
		t.Helper()
		s := newStore(t)
		s.Exec(`INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me, media_type)
				VALUES ('V1', '` + group + `', '5', '', '2026-10-27 17:00:00+03:00', 0, 'video');
			INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me, media_type)
				VALUES ('I1', '` + group + `', '5', '', '2026-10-27 17:00:00+03:00', 0, 'image');
			INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me, media_type)
				VALUES ('D9', '` + group + `', '5', '', '2026-10-27 17:00:00+03:00', 0, 'document')`)
		b := storetest.NewBridge(t, s.Fixture)
		b.Answer = func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
			if path == "" {
				w.Write([]byte(`{"success": false, "message": "expired"}`))
				return
			}
			w.Write([]byte(`{"success": true, "path": "` + path + `"}`))
		}
		s.media.Video = video
		return s.connectClient(t)
	}
	t.Run("the first frame of a video, held to the photo limits", func(t *testing.T) {
		t.Parallel()
		cs := setup(t, "/store/clip.mp4", fakeVideo{frame: encodePNG(t, 2400, 1600)})
		res := callTool(t, cs, "view_media", map[string]any{"message_id": "V1", "chat_jid": group})
		img, ok := res.Content[0].(*mcp.ImageContent)
		if !ok {
			t.Fatalf("got %+v", res.Content[0])
		}
		if img.MIMEType != "image/jpeg" || len(img.Data) > 1_000_000 {
			t.Errorf("got %s, %d bytes", img.MIMEType, len(img.Data))
		}
	})
	t.Run("an image", func(t *testing.T) {
		t.Parallel()
		pic := writeFile(t, filepath.Join(t.TempDir(), "p.png"), encodePNG(t, 64, 32))
		res := callTool(t, setup(t, pic, nil), "view_media", map[string]any{"message_id": "I1", "chat_jid": group})
		if img, ok := res.Content[0].(*mcp.ImageContent); !ok || img.MIMEType != "image/png" {
			t.Errorf("got %+v", res.Content[0])
		}
	})
	cases := []struct {
		name  string
		id    string
		path  string
		video mediaconv.VideoFramer
		text  string
		err   bool
	}{
		{"no frame", "V1", "/store/clip.mp4", fakeVideo{err: fmt.Errorf("%w: install ffmpeg", mediaconv.ErrVideoFrame)},
			"{\n  \"success\": false,\n  \"message\": \"Cannot show video V1: no video frame: install ffmpeg. The file is at /store/clip.mp4\"\n}", false},
		{"another video error", "V1", "/store/clip.mp4", fakeVideo{err: errors.New("boom")}, "Error executing tool view_media", true},
		{"a frame that is no image", "V1", "/store/clip.mp4", fakeVideo{frame: []byte("nope")}, "Error executing tool view_media", true},
		{"a missing image", "I1", "/nonexistent/p.png", nil, "Error executing tool view_media", true},
		{"a document", "D9", "/store/d.pdf", nil,
			"{\n  \"success\": false,\n  \"message\": \"Message D9 is a document, not an image or video; use download_media\"\n}", false},
		{"a failed download", "I1", "", nil, "{\n  \"success\": false,\n  \"message\": \"expired\"\n}", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := callTool(t, setup(t, tc.path, tc.video), "view_media", map[string]any{"message_id": tc.id, "chat_jid": group})
			if res.IsError != tc.err || textOf(res) != tc.text {
				t.Errorf("got %v %q, want %q", res.IsError, textOf(res), tc.text)
			}
		})
	}
}

func TestViewProfilePicture(t *testing.T) {
	t.Parallel()
	pic := writeFile(t, filepath.Join(t.TempDir(), "p.png"), encodePNG(t, 64, 32))
	cases := []struct {
		name, answer, text string
	}{
		{"the picture", `{"success": true, "picture_path": "` + pic + `"}`, ""},
		{"a refusal", `{"success": false, "message": "not on WhatsApp"}`, "{\n  \"success\": false,\n  \"message\": \"not on WhatsApp\"\n}"},
		{"nothing said", `{"success": true}`, "{\n  \"success\": false,\n  \"message\": \"No picture\"\n}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newStore(t)
			b := storetest.NewBridge(t, s.Fixture)
			b.Answer = func(w http.ResponseWriter, r *http.Request, _ map[string]any) { w.Write([]byte(tc.answer)) }
			res := callTool(t, s.connectClient(t), "view_profile_picture", map[string]any{"jid": "18046727669"})
			if tc.text == "" {
				if img, ok := res.Content[0].(*mcp.ImageContent); !ok || img.MIMEType != "image/png" {
					t.Errorf("got %+v", res.Content[0])
				}
				return
			}
			if got := textOf(res); got != tc.text {
				t.Errorf("got %q, want %q", got, tc.text)
			}
		})
	}
}

func TestExportChatDownloadsMedia(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	pic := writeFile(t, filepath.Join(t.TempDir(), "pic.jpg"), []byte("jpg"))
	b := storetest.NewBridge(t, s.Fixture)
	b.Answer = func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		w.Write([]byte(`{"success": true, "path": "` + pic + `"}`))
	}
	s.Exec("UPDATE messages SET deleted_at = NULL WHERE id = 'A3'")
	got := handle(t, s, exportChat, Args{"chat_jid": group, "format": "txt", "include_media": true}).(*pyfmt.OMap)
	if got.Get("success") != true || got.Get("media") != 1 || filepath.Ext(pyfmt.AsString(got.Get("path"))) != ".zip" {
		t.Errorf("got %s", pyfmt.Compact(got))
	}
	if want := jsonOf(t, `[["download", {"message_id": "A3", "chat_jid": "`+group+`"}]]`); b.Summary() != want {
		t.Errorf("bridge %s", b.Summary())
	}
}

func TestEventsFromHiddenChatsAreDropped(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	storetest.NewBridge(t, s.Fixture).SSE(`data: {"type": "message", "chat_jid": "other@g.us"}`, `data: {"type": "session", "state": "logged_out"}`,
		`data: {"type": "message", "chat_jid": "`+group+`"}`)
	s.Config.ReadAllowlist = group
	got := handle(t, s, waitForEvents, Args{"timeout_seconds": 1, "chat_jid": "", "max_events": 2}).(*pyfmt.OMap)
	want := `[{"type": "session", "state": "logged_out"}, {"type": "message", "chat_jid": "` + group + `"}]`
	if pyfmt.Compact(got.Get("events")) != want {
		t.Errorf("got %s", pyfmt.Compact(got))
	}
}

func TestListAccountsReportsEachBridge(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	home := storetest.NewBridge(t, nil)
	home.Answer = func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		w.Write([]byte(`{"connected": false, "session": {"state": "logged_out", "reason": "scan", "needs_action": true}}`))
	}
	work := t.TempDir()
	s.Config.Accounts = `{"home": {"store_dir": "` + s.Dir + `", "api_url": "` + home.URL + `"}, "work": {"store_dir": "` + work + `", "api_url": "` + storetest.Unreachable + `"}}`
	s.Config.DefaultAccount = "home"
	got := handle(t, s, listAccounts, Args{}).([]*pyfmt.OMap)
	if len(got) != 2 {
		t.Fatalf("got %d accounts", len(got))
	}
	if want := `{"name": "home", "default": true, "store_dir": "` + s.Dir + `", "api_url": "` + home.URL + `", "connected": false, "jid": null, ` +
		`"error": "WhatsApp session logged_out: scan. No messages are received or sent until this is fixed."}`; pyfmt.Compact(got[0]) != want {
		t.Errorf("home %s", pyfmt.Compact(got[0]))
	}
	if errText := pyfmt.AsString(got[1].Get("error")); got[1].Get("default") != false || !strings.HasPrefix(errText, "Bridge not reachable: ") {
		t.Errorf("work %s", pyfmt.Compact(got[1]))
	}
	s.Config.Accounts = "[1]"
	if _, err := listAccounts(t.Context(), s.call(Args{})); err == nil {
		t.Error("bad account list accepted")
	}
}

// The table is consistent: 59 tools, each named once, each with a handler.
func TestTableIsComplete(t *testing.T) {
	t.Parallel()
	if len(toolTable) != 59 {
		t.Errorf("%d tools", len(toolTable))
	}
	names := map[string]bool{}
	for _, spec := range toolTable {
		if names[spec.name] {
			t.Errorf("tool %s twice", spec.name)
		}
		names[spec.name] = true
		if spec.handle == nil {
			t.Errorf("tool without handler: %s", spec.name)
		}
	}
}
