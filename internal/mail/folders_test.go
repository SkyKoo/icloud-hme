package mail

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/backend/memory"
	imapclient "github.com/emersion/go-imap/client"
	imapserver "github.com/emersion/go-imap/server"
)

func TestCollectMessagesSortsAndKeepsFolderIdentity(t *testing.T) {
	now := time.Now()
	read := func(folder string) ([]Message, error) {
		if folder == FolderInbox {
			return []Message{
				{ID: "1", Date: now.Add(-time.Hour).Format(time.RFC3339)},
				{ID: "1", Date: now.Add(-time.Hour).Format(time.RFC3339)},
				{ID: "old", Date: now.AddDate(0, 0, -10).Format(time.RFC3339)},
			}, nil
		}
		return []Message{{ID: "1", Date: now.Format(time.RFC3339)}, {ID: "2", Date: now.Add(-2 * time.Hour).Format(time.RFC1123Z)}}, nil
	}
	messages, err := CollectMessages(FolderAll, 2, 7, read)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].Folder != FolderJunk || messages[1].Folder != FolderInbox || messages[0].ID != "1" || messages[1].ID != "1" {
		t.Fatalf("wrong combined page: %#v", messages)
	}
	messages, err = CollectMessages(FolderAll, 20, 7, read)
	if err != nil || len(messages) != 3 {
		t.Fatalf("date filtering or dedup failed: %#v, %v", messages, err)
	}
}

func TestCollectMessagesDoesNotHideFolderFailure(t *testing.T) {
	want := errors.New("junk unavailable")
	messages, err := CollectMessages(FolderAll, 20, 7, func(folder string) ([]Message, error) {
		if folder == FolderJunk {
			return nil, want
		}
		return []Message{{ID: "1"}}, nil
	})
	if !errors.Is(err, want) || messages != nil {
		t.Fatalf("partial results masked failure: %#v %v", messages, err)
	}
	called := false
	if _, err := CollectMessages("Trash", 20, 7, func(string) ([]Message, error) { called = true; return nil, nil }); err == nil || called {
		t.Fatal("invalid folder reached upstream")
	}
}

func TestJunkMailboxDiscovery(t *testing.T) {
	boxes := []*imap.MailboxInfo{{Name: "Spam"}, {Name: "provider-specific", Attributes: []string{`\Junk`}}}
	if got := junkMailbox(boxes); got != "provider-specific" {
		t.Fatalf("special-use flag ignored: %q", got)
	}
	if got := junkMailbox([]*imap.MailboxInfo{{Name: "垃圾邮件"}}); got != "垃圾邮件" {
		t.Fatal(got)
	}
	if got := junkMailbox([]*imap.MailboxInfo{{Name: "INBOX"}}); got != "" {
		t.Fatalf("must not use inbox as junk: %q", got)
	}
}

func TestIMAPFolderReadsAndDeleteDoNotCrossUIDs(t *testing.T) {
	be := memory.New()
	user, err := be.Login(nil, "username", "password")
	if err != nil {
		t.Fatal(err)
	}
	if err := user.CreateMailbox("Junk"); err != nil {
		t.Fatal(err)
	}
	for _, folder := range []string{"INBOX", "Junk"} {
		box, err := user.GetMailbox(folder)
		if err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf("From: sender@example.com\r\nTo: alias@icloud.com\r\nSubject: %s subject\r\nDate: %s\r\nContent-Type: text/plain\r\n\r\n%s body", folder, time.Now().Format(time.RFC1123Z), folder)
		box.(*memory.Mailbox).Messages = []*memory.Message{{Uid: 6, Date: time.Now(), Size: uint32(len(body)), Body: []byte(body)}}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := imapserver.New(be)
	server.AllowInsecureAuth = true
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { server.Close(); <-done })
	cli, err := imapclient.Dial(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cli.Logout() })
	if err := cli.Login("username", "password"); err != nil {
		t.Fatal(err)
	}
	c := &Client{cli: cli}
	messages, err := c.ListInbox(20, 7, FolderJunk)
	if err != nil || len(messages) != 1 || messages[0].Subject != "Junk subject" {
		t.Fatalf("junk listing: %#v %v", messages, err)
	}
	messages, err = c.FindByRecipient("alias@icloud.com", 20, 7, FolderJunk)
	if err != nil || len(messages) != 1 || messages[0].Subject != "Junk subject" {
		t.Fatalf("junk alias query: %#v %v", messages, err)
	}
	full, err := c.GetFull(6, FolderJunk)
	if err != nil || !strings.Contains(full.Body, "Junk body") {
		t.Fatalf("wrong detail: %#v %v", full, err)
	}
	if err := c.Delete(6, FolderJunk); err != nil {
		t.Fatal(err)
	}
	messages, err = c.ListInbox(20, 7, FolderInbox)
	if err != nil || len(messages) != 1 || messages[0].Subject != "INBOX subject" {
		t.Fatalf("inbox UID was affected: %#v %v", messages, err)
	}
	messages, err = c.ListInbox(20, 7, FolderJunk)
	if err != nil || len(messages) != 0 {
		t.Fatalf("junk UID not deleted: %#v %v", messages, err)
	}
	for _, folder := range []string{"INBOX", "Junk"} {
		box, _ := user.GetMailbox(folder)
		for _, message := range box.(*memory.Mailbox).Messages {
			for _, flag := range message.Flags {
				if flag == imap.SeenFlag {
					t.Fatal("preview marked a message read")
				}
			}
		}
	}
}
