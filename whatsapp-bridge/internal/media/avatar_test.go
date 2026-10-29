package media

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestAvatar(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/gone" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte("jpeg"))
	}))
	t.Cleanup(server.Close)
	alice := types.NewJID("2000", types.DefaultUserServer)

	cases := []struct {
		name string
		info *types.ProfilePictureInfo
		err  error
		want string // error; "" for success
	}{
		{"cached under avatars", &types.ProfilePictureInfo{URL: server.URL + "/pic"}, nil, ""},
		{"hidden picture", &types.ProfilePictureInfo{}, nil, "no profile picture, or it is hidden from you"},
		{"lookup fails", nil, errors.New("not authorized"), "not authorized"},
		{"CDN error", &types.ProfilePictureInfo{URL: server.URL + "/gone"}, nil, "picture download answered HTTP 404"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s, fake := newTestService(t)
			fake.PictureFunc = func(_ context.Context, jid types.JID) (*types.ProfilePictureInfo, error) {
				if jid != alice {
					t.Errorf("picture of %s", jid)
				}
				return c.info, c.err
			}
			path, err := s.Avatar(t.Context(), alice)
			if c.want != "" {
				if err == nil || err.Error() != c.want {
					t.Errorf("err %v, want %q", err, c.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if want := filepath.Join(s.Root, "avatars", "2000@s.whatsapp.net.jpg"); path != want {
				t.Errorf("path %s, want %s", path, want)
			}
			if data, _ := os.ReadFile(path); string(data) != "jpeg" {
				t.Errorf("file %q", data)
			}
		})
	}
}

func TestAvatarPathStaysInStore(t *testing.T) {
	t.Parallel()
	path := AvatarPath(types.NewJID("../../etc/passwd", types.DefaultUserServer))
	if filepath.Dir(path) != filepath.Join(DefaultRoot, "avatars") || strings.Contains(path, "..") {
		t.Errorf("AvatarPath escapes store/avatars: %s", path)
	}
}
