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
