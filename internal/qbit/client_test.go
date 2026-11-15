package qbit

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientAuthAndAdd(t *testing.T) {
	var logins int
	var added urlCheck
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case r.URL.Path == "/api/v2/auth/login":
			logins++
			if !strings.Contains(string(body), "username=user") || !strings.Contains(string(body), "password=secret") {
				http.Error(w, "bad", http.StatusUnauthorized)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "abc", Path: "/"})
			_, _ = w.Write([]byte("Ok."))
		case r.URL.Path == "/api/v2/torrents/createCategory":
			if cookie(r) != "abc" {
				http.Error(w, "no cookie", http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/api/v2/torrents/add":
			if cookie(r) != "abc" {
				http.Error(w, "no cookie", http.StatusForbidden)
				return
			}
			added.raw = string(body)
			_, _ = w.Write([]byte("Ok."))
		case r.URL.Path == "/api/v2/torrents/info":
			_, _ = w.Write([]byte(`[{"hash":"abc","name":"Title","progress":1,"state":"stalledUP","save_path":"/data/downloads/animarr","amount_left":0,"completion_on":1700000000}]`))
		case r.URL.Path == "/api/v2/torrents/files":
			_, _ = w.Write([]byte(`[{"name":"Title - 07.mkv","size":10,"progress":1}]`))
		case r.URL.Path == "/api/v2/app/version":
			_, _ = w.Write([]byte("v4.6.7"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, err := New(srv.URL, "user", "secret")
	if err != nil {
		t.Fatal(err)
	}
	ver, err := c.Test(context.Background())
	if err != nil || ver != "v4.6.7" {
		t.Fatalf("version %q %v", ver, err)
	}
	hash, err := c.Add(context.Background(), AddRequest{
		URL:      "magnet:?xt=urn:btih:abc",
		Category: "animarr",
		SavePath: "/data/downloads/animarr",
		Tags:     []string{"animarr", "ep-7"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if hash != "abc" {
		t.Fatalf("hash %q", hash)
	}
	if logins < 1 {
		t.Fatal("expected login")
	}
	if !strings.Contains(added.raw, "urls=magnet") || !strings.Contains(added.raw, "category=animarr") || !strings.Contains(added.raw, "savepath=") || !strings.Contains(added.raw, "tags=animarr") {
		t.Fatalf("form %s", added.raw)
	}
	got, err := c.Get(context.Background(), "ABC")
	if err != nil {
		t.Fatal(err)
	}
	if !Complete(got) {
		t.Fatalf("not complete %+v", got)
	}
	files, err := c.Files(context.Background(), "abc")
	if err != nil || len(files) != 1 || files[0].Name != "Title - 07.mkv" {
		t.Fatalf("files %+v %v", files, err)
	}
}

func TestNoAuthSkipsLogin(t *testing.T) {
	var logins int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			logins++
		}
		if r.URL.Path == "/api/v2/app/version" {
			_, _ = w.Write([]byte("v5"))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	c, err := New(srv.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	ver, err := c.Test(context.Background())
	if err != nil || ver != "v5" {
		t.Fatalf("%s %v", ver, err)
	}
	if logins != 0 {
		t.Fatalf("logins %d", logins)
	}
}

func TestMissingTorrent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	c, err := New(srv.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Get(context.Background(), "nope")
	if err != ErrNotFound {
		t.Fatalf("%v", err)
	}
}

func TestFailedState(t *testing.T) {
	if !Failed(Torrent{State: "missingFiles"}) {
		t.Fatal("expected failed")
	}
	if Failed(Torrent{State: "downloading"}) || Complete(Torrent{State: "downloading", Progress: 0.4}) {
		t.Fatal("downloading is not terminal")
	}
}

func TestAddResponseShapes(t *testing.T) {
	const existing = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const fresh = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	cases := []struct {
		name     string
		status   int
		body     string
		present  string
		magnet   string
		wantErr  string
		wantHash string
	}{
		{name: "ok", body: "Ok.", magnet: "magnet:?xt=urn:btih:" + fresh, wantHash: fresh},
		{
			name:     "json success",
			body:     `{"added_torrent_ids":["` + fresh + `"],"failure_count":0,"pending_count":0,"success_count":1}`,
			magnet:   "magnet:?xt=urn:btih:" + existing,
			wantHash: fresh,
		},
		{
			name:     "pending",
			body:     `{"added_torrent_ids":[],"failure_count":0,"pending_count":1,"success_count":0}`,
			magnet:   "magnet:?xt=urn:btih:" + fresh,
			wantHash: fresh,
		},
		{name: "fails", body: "Fails.", magnet: "magnet:?xt=urn:btih:" + fresh, wantErr: "Fails."},
		{
			name:    "json failure",
			body:    `{"added_torrent_ids":[],"failure_count":1,"pending_count":0,"success_count":0}`,
			magnet:  "magnet:?xt=urn:btih:" + fresh,
			wantErr: "failure_count",
		},
		{
			name: "already exists text", body: "Fails.", present: existing,
			magnet: "magnet:?xt=urn:btih:" + existing, wantHash: existing,
		},
		{
			name: "already exists json", present: existing,
			body:     `{"added_torrent_ids":[],"failure_count":1,"pending_count":0,"success_count":0}`,
			magnet:   "magnet:?xt=urn:btih:" + existing,
			wantHash: existing,
		},
		{
			name: "already exists conflict", status: http.StatusConflict, present: existing,
			body: "torrent already exists", magnet: "magnet:?xt=urn:btih:" + existing, wantHash: existing,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var infoCategory string
			var sawInfo bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/torrents/add":
					if tc.status != 0 {
						w.WriteHeader(tc.status)
					}
					_, _ = w.Write([]byte(tc.body))
				case "/api/v2/torrents/info":
					sawInfo = true
					infoCategory = r.URL.Query().Get("category")
					if tc.present != "" && strings.EqualFold(r.URL.Query().Get("hashes"), tc.present) {
						_, _ = w.Write([]byte(`[{"hash":"` + tc.present + `","name":"have","state":"downloading"}]`))
						return
					}
					_, _ = w.Write([]byte(`[]`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			c, err := New(srv.URL, "", "")
			if err != nil {
				t.Fatal(err)
			}
			hash, err := c.Add(context.Background(), AddRequest{URL: tc.magnet})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if hash != tc.wantHash {
				t.Fatalf("hash %q want %s", hash, tc.wantHash)
			}
			if tc.present != "" {
				if !sawInfo {
					t.Fatal("expected a lookup of the existing torrent")
				}
				if infoCategory != "" {
					t.Fatalf("lookup limited to category %q", infoCategory)
				}
			}
		})
	}
}

type urlCheck struct{ raw string }

func cookie(r *http.Request) string {
	c, err := r.Cookie("SID")
	if err != nil {
		return ""
	}
	return c.Value
}
