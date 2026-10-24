package imap

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/emersion/go-imap"
	imapbackend "github.com/emersion/go-imap/backend"

	"github.com/emersion/hydroxide/config"
	"github.com/emersion/hydroxide/events"
	"github.com/emersion/hydroxide/imap/database"
	"github.com/emersion/hydroxide/protonmail"
)

// newTestUser builds a user against a fake Proton API. Every handler must
// answer GET /events/* (the receiver polls it) and GET /messages (sync).
func newTestUser(t *testing.T, handler http.HandlerFunc) *user {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	config.SetConfigHome(t.TempDir())
	db, err := database.Open("test.db")
	if err != nil {
		t.Fatal(err)
	}
	be := &backend{eventsManager: events.NewManager(), updates: make(chan imapbackend.Update)}
	go func() {
		for range be.updates {
		}
	}()
	u := &user{
		username:   "alice",
		backend:    be,
		c:          &protonmail.Client{RootURL: srv.URL},
		u:          &protonmail.User{Name: "alice"},
		db:         db,
		eventSent:  make(chan struct{}),
		numClients: 1,
		mailboxes:  make(map[string]*mailbox),
		flags:      make(map[string]string),
	}
	for _, data := range systemMailboxes {
		u.mailboxes[data.label], err = newMailbox(data.name, data.label, data.attrs, u)
		if err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan struct{})
	u.done = done
	ch := make(chan *protonmail.Event)
	go u.receiveEvents(be.updates, ch)
	u.eventsReceiver = be.eventsManager.Register(u.c, "alice", ch, done)
	t.Cleanup(func() { close(done); db.Close() })
	return u
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func listHandler(msgs []*protonmail.Message) func(w http.ResponseWriter, r *http.Request) bool {
	return func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case strings.HasPrefix(r.URL.Path, "/events/"):
			writeJSON(w, map[string]interface{}{"Code": 1000, "EventID": "e1"})
		case r.Method == http.MethodGet && r.URL.Path == "/messages":
			var page []*protonmail.Message
			for _, m := range msgs {
				if hasLabel(m, r.URL.Query().Get("Label")) {
					page = append(page, m)
				}
			}
			writeJSON(w, map[string]interface{}{"Code": 1000, "Total": len(page), "Messages": page})
		default:
			return false
		}
		return true
	}
}

func TestMoveFromAllMailSkipsUnlabel(t *testing.T) {
	msg := &protonmail.Message{ID: "m1", LabelIDs: []string{protonmail.LabelInbox, protonmail.LabelAllMail}, Sender: &protonmail.MessageAddress{}}
	var labels, unlabels int32
	base := listHandler([]*protonmail.Message{msg})
	u := newTestUser(t, func(w http.ResponseWriter, r *http.Request) {
		if base(w, r) {
			return
		}
		switch r.URL.Path {
		case "/messages/label":
			atomic.AddInt32(&labels, 1)
			writeJSON(w, map[string]interface{}{"Code": 1001})
		case "/messages/unlabel":
			atomic.AddInt32(&unlabels, 1)
			writeJSON(w, map[string]interface{}{"Code": 2011, "Error": "That label cannot be applied or removed"})
		default:
			http.NotFound(w, r)
		}
	})

	seqSet, _ := imap.ParseSeqSet("1")
	if err := u.mailboxes[protonmail.LabelAllMail].MoveMessages(false, seqSet, "Trash"); err != nil {
		t.Fatalf("MoveMessages from All Mail: %v", err)
	}
	if labels != 1 || unlabels != 0 {
		t.Fatalf("label calls = %d, unlabel calls = %d; want 1 and 0", labels, unlabels)
	}
}

func TestListMessagesAnswersEverySeqNum(t *testing.T) {
	msgs := []*protonmail.Message{
		{ID: "m1", LabelIDs: []string{protonmail.LabelInbox}, Unread: 1, Sender: &protonmail.MessageAddress{}},
		{ID: "m2", LabelIDs: []string{protonmail.LabelInbox}, Sender: &protonmail.MessageAddress{}},
	}
	var gets int32
	base := listHandler(msgs)
	u := newTestUser(t, func(w http.ResponseWriter, r *http.Request) {
		if base(w, r) {
			return
		}
		for _, m := range msgs {
			if r.Method == http.MethodGet && r.URL.Path == "/messages/"+m.ID {
				atomic.AddInt32(&gets, 1)
				writeJSON(w, map[string]interface{}{"Code": 1000, "Message": m})
				return
			}
		}
		http.NotFound(w, r)
	})

	mbox := u.mailboxes[protonmail.LabelInbox]
	if err := mbox.init(); err != nil {
		t.Fatal(err)
	}
	// A refresh event empties the message cache; the mailbox bucket stays.
	if err := u.db.ResetMessages(); err != nil {
		t.Fatal(err)
	}
	// Proton's count at login is stale by the time the client fetches.
	mbox.total = 1

	fetch := func() []*imap.Message {
		seqSet, _ := imap.ParseSeqSet("1:*")
		ch := make(chan *imap.Message)
		errc := make(chan error, 1)
		go func() { errc <- mbox.ListMessages(false, seqSet, []imap.FetchItem{imap.FetchUid, imap.FetchFlags}, ch) }()
		var got []*imap.Message
		for m := range ch {
			got = append(got, m)
		}
		if err := <-errc; err != nil {
			t.Fatal(err)
		}
		return got
	}

	got := fetch()
	if len(got) != 2 {
		t.Fatalf("FETCH 1:* returned %d messages, want 2", len(got))
	}
	if got[0].Uid != 1 || got[1].Uid != 2 {
		t.Fatalf("uids = %d, %d; want 1, 2", got[0].Uid, got[1].Uid)
	}
	if len(got[0].Flags) != 0 || len(got[1].Flags) != 1 || got[1].Flags[0] != imap.SeenFlag {
		t.Fatalf("flags = %v, %v; want unread then \\Seen", got[0].Flags, got[1].Flags)
	}
	if gets != 2 {
		t.Fatalf("GET /messages/<id> calls = %d, want 2", gets)
	}
	// The fetched messages are cached: the next FETCH needs no API call.
	fetch()
	if gets != 2 {
		t.Fatalf("GET /messages/<id> calls after second FETCH = %d, want 2", gets)
	}
}
