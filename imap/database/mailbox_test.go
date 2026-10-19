package database

import (
	"path/filepath"
	"reflect"
	"testing"

	bolt "go.etcd.io/bbolt"

	"github.com/emersion/hydroxide/protonmail"
)

func TestMailboxPrune(t *testing.T) {
	db, err := bolt.Open(filepath.Join(t.TempDir(), "test.db"), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	u := &User{db}
	mbox, err := u.Mailbox("0")
	if err != nil {
		t.Fatal(err)
	}

	ids := []string{"a", "b", "c", "d", "e"}
	var msgs []*protonmail.Message
	for _, id := range ids {
		msgs = append(msgs, &protonmail.Message{ID: id})
	}
	if err := mbox.Sync(msgs); err != nil {
		t.Fatal(err)
	}

	uids := make(map[string]uint32)
	for _, id := range ids {
		_, uid, err := mbox.FromApiID(id)
		if err != nil {
			t.Fatal(err)
		}
		uids[id] = uid
	}

	keep := map[string]struct{}{"a": {}, "c": {}, "e": {}}
	seqNums, err := mbox.Prune(keep)
	if err != nil {
		t.Fatal(err)
	}
	if want := []uint32{4, 2}; !reflect.DeepEqual(seqNums, want) {
		t.Errorf("Prune() = %v, want %v", seqNums, want)
	}

	var got []string
	err = mbox.ForEach(func(seqNum, uid uint32, apiID string) error {
		got = append(got, apiID)
		if uid != uids[apiID] {
			t.Errorf("UID of %s = %d, want %d", apiID, uid, uids[apiID])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "c", "e"}; !reflect.DeepEqual(got, want) {
		t.Errorf("remaining = %v, want %v", got, want)
	}

	// A second sync must not renumber what is already there.
	if err := mbox.Sync([]*protonmail.Message{{ID: "a"}, {ID: "f"}}); err != nil {
		t.Fatal(err)
	}
	if _, uid, err := mbox.FromApiID("a"); err != nil || uid != uids["a"] {
		t.Errorf("UID of a after resync = %d, %v, want %d", uid, err, uids["a"])
	}
	if _, uid, err := mbox.FromApiID("f"); err != nil || uid != 6 {
		t.Errorf("UID of f = %d, %v, want 6", uid, err)
	}
}
