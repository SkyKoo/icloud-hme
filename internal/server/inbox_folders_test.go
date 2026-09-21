package server

import (
	"net/http"
	"testing"
)

func TestInboxFolderValidationAndMessageRouting(t *testing.T) {
	f := &fakeBackend{}
	_, ts := newTestServer(f)
	defer ts.Close()
	session, csrf := login(t, ts, "admin-pass-2026-strong")
	for _, tc := range []struct {
		method, path string
		status       int
		folder       string
	}{
		{"GET", "/api/inbox?account_id=acc_1", 200, "all"},
		{"GET", "/api/inbox?account_id=acc_1&folder=junk", 200, "junk"},
		{"GET", "/api/inbox?account_id=acc_1&folder=inbox", 200, "inbox"},
		{"GET", "/api/inbox?account_id=acc_1&folder=Trash", 400, ""},
		{"GET", "/api/inbox/6?account_id=acc_1&folder=all", 400, ""},
		{"DELETE", "/api/inbox/6?account_id=acc_1&folder=all", 400, ""},
		{"GET", "/api/inbox/6?account_id=acc_1&folder=junk", 200, "junk"},
		{"DELETE", "/api/inbox/6?account_id=acc_1&folder=junk", 200, "junk"},
	} {
		req, _ := http.NewRequest(tc.method, ts.URL+tc.path, nil)
		req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
		req.Header.Set("X-CSRF-Token", csrf)
		status, body, _ := do(t, req)
		if status != tc.status {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, status, body)
		}
		if tc.status == 200 && tc.method == "GET" && len(tc.path) > 10 && tc.path[:11] == "/api/inbox?" && f.listInboxQuery.Folder != tc.folder {
			t.Fatalf("wrong folder: %#v", f.listInboxQuery)
		}
	}
	if f.detailFolder != "junk" || f.deletedFolder != "junk" {
		t.Fatalf("message folder lost: %q %q", f.detailFolder, f.deletedFolder)
	}
}
