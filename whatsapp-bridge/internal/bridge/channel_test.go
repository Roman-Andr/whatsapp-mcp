package bridge

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
)

const subscribedChannel = `{"id":"123@newsletter","state":{"type":"ACTIVE"},"thread_metadata":{"creation_time":"1697988341",
	"name":{"id":"1","text":"Plow news","update_time":"1697988341053264"},"invite":"abc","verification":"VERIFIED"},
	"viewer_metadata":{"role":"SUBSCRIBER"}}`

func TestStatusChatName(t *testing.T) {
	t.Parallel()
	if name := newTestBridge(t).chatName(t.Context(), types.StatusBroadcastJID, nil, "79966764464"); name != statusChatName {
		t.Errorf("status chat named %q", name)
	}
}

func TestSubscribedNewslettersPayload(t *testing.T) {
	t.Parallel()
	if got := string(subscribedNewslettersPayload()); got != `{"variables":{"fetch_wamo_sub":false,"fetch_status_metadata":false}}` {
		t.Errorf("payload %s", got)
	}
}

func TestParseSubscribedNewsletters(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		result   string
		channels int
		err      []string // substrings of the error; nil for none
	}{
		{"partial result with errors", `{"data":{"xwa2_newsletter_subscribed":[` + subscribedChannel + `,null]},
			"errors":[{"extensions":{"error_code":500,"severity":"CRITICAL"},"message":"Internal Server Error","path":["xwa2_newsletter_subscribed"]}]}`, 1, nil},
		{"error result keeps the detail", `{"data":null,"errors":[{"extensions":{"error_code":400,"severity":"CRITICAL","debug_info":"missing variable"},"message":"Bad Request"}]}`,
			0, []string{"400 Bad Request (CRITICAL)", `"debug_info":"missing variable"`}},
		{"unreadable errors shown verbatim", `{"errors":[{"message":"Bad Request","path":["xwa2_newsletter_subscribed",0]}]}`,
			0, []string{`"path":["xwa2_newsletter_subscribed",0]`}},
		{"empty", `{"data":{"xwa2_newsletter_subscribed":[]}}`, 0, nil},
		{"not JSON", `<html>`, 0, []string{"failed to unmarshal graphql response", "response <html>"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			list, err := parseSubscribedNewsletters([]byte(c.result))
			if c.err == nil {
				if err != nil || len(list) != c.channels {
					t.Errorf("%v %v", list, err)
				}
				return
			}
			for _, want := range c.err {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("err %v lacks %q", err, want)
				}
			}
		})
	}
}

func TestDescribeChannel(t *testing.T) {
	t.Parallel()
	list, err := parseSubscribedNewsletters([]byte(`{"data":{"xwa2_newsletter_subscribed":[` + subscribedChannel + `]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if c := describeChannel(list[0]); c.JID != "123@newsletter" || c.Name != "Plow news" || !c.Verified || c.Role != "subscriber" || c.InviteLink != "https://whatsapp.com/channel/abc" {
		t.Errorf("%+v", c)
	}
}

func TestReadSubscribedNewsletters(t *testing.T) {
	t.Parallel()
	result := func(attrs waBinary.Attrs, content any) *waBinary.Node {
		return &waBinary.Node{Tag: "iq", Content: []waBinary.Node{{Tag: "result", Attrs: attrs, Content: content}}}
	}
	cases := []struct {
		name string
		node *waBinary.Node
		err  error
		want string // error; "" for success
	}{
		{"result", result(nil, []byte(`{"data":{"xwa2_newsletter_subscribed":[]}}`)), nil, ""},
		{"no result", &waBinary.Node{Tag: "iq"}, nil, "mex response has no result"},
		{"not bytes", result(nil, "text"), nil, "unexpected content type string in mex response"},
		{"argo", result(waBinary.Attrs{"format": "argo"}, []byte{}), nil, "mex response is argo-encoded, which the bridge cannot decode"},
		{"query failed", nil, errors.New("timed out"), "timed out"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := readSubscribedNewsletters(c.node, c.err)
			if (c.want == "") != (err == nil) || (err != nil && err.Error() != c.want) {
				t.Errorf("err %v, want %q", err, c.want)
			}
		})
	}
}

func TestChannelsThroughSendIQ(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t, allowRead(t, "123@newsletter"))
	b.fake.SendIQFunc = func(_ context.Context, q whatsmeow.DangerousInfoQuery) (*waBinary.Node, error) {
		if q.Namespace != "w:mex" || q.Content.([]waBinary.Node)[0].Attrs["query_id"] != subscribedNewslettersQueryID {
			t.Errorf("query %+v", q)
		}
		hidden := strings.Replace(subscribedChannel, "123@newsletter", "456@newsletter", 1)
		body := `{"data":{"xwa2_newsletter_subscribed":[` + subscribedChannel + `,` + hidden + `]}}`
		return &waBinary.Node{Tag: "iq", Content: []waBinary.Node{{Tag: "result", Content: []byte(body)}}}, nil
	}
	list, err := b.Channels(t.Context())
	if err != nil || len(list) != 1 || list[0].JID != "123@newsletter" {
		t.Errorf("channels %+v %v", list, err)
	}

	b.fake.SendIQFunc = func(context.Context, whatsmeow.DangerousInfoQuery) (*waBinary.Node, error) {
		return &waBinary.Node{Tag: "iq"}, nil
	}
	if _, err := b.Channels(t.Context()); err == nil {
		t.Fatal("no error")
	}
	if logs := b.logs.String(); !strings.Contains(logs, `msg="channel list query failed"`) || !strings.Contains(logs, "answer=<iq/>") {
		t.Errorf("failure not logged with the answer:\n%s", logs)
	}
}

func TestTruncateForLog(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", maxLoggedResponse+5)
	if got := truncateForLog(long); !strings.HasSuffix(got, "… (8197 bytes)") || len(got) > maxLoggedResponse+20 {
		t.Errorf("truncated to %d bytes: %q", len(got), got[len(got)-20:])
	}
	if got := truncateForLog([]byte("short")); got != "short" {
		t.Errorf("got %q", got)
	}
}
