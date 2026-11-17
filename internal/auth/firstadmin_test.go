package auth

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/henry40408/bkd/internal/dbtest"
)

func TestTheFirstAccountIsAnAdmin(t *testing.T) {
	pool := dbtest.Open(t)
	if exists, err := UsersExist(bg, pool); exists || err != nil {
		t.Fatalf("empty database: %v, %v", exists, err)
	}

	id, created, err := CreateFirstAdmin(bg, pool, FirstAdmin{Username: "henry", PasswordHash: "hash"})
	if err != nil || !created {
		t.Fatalf("created = %v, err = %v", created, err)
	}
	if exists, err := UsersExist(bg, pool); !exists || err != nil {
		t.Fatalf("after setup: %v, %v", exists, err)
	}
	var isAdmin bool
	if err := pool.QueryRow("SELECT is_admin FROM users WHERE id = ?1", id).Scan(&isAdmin); err != nil {
		t.Fatal(err)
	}
	if !isAdmin {
		t.Fatal("first account is not an admin")
	}
}

func TestNothingIsCreatedOnceAUserExists(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.InsertUser(t, pool, "henry")

	_, created, err := CreateFirstAdmin(bg, pool, FirstAdmin{Username: "mei", PasswordHash: "hash"})
	if created || err != nil {
		t.Fatalf("created = %v, err = %v", created, err)
	}
	if count := countRows(t, pool, "SELECT count(*) FROM users"); count != 1 {
		t.Fatalf("%d users, want 1", count)
	}
}

func TestConcurrentAttemptsCreateExactlyOneAdmin(t *testing.T) {
	pool := dbtest.Open(t)
	var winners atomic.Int32
	var group sync.WaitGroup
	for _, name := range []string{"user0", "user1", "user2", "user3", "user4", "user5", "user6", "user7"} {
		group.Add(1)
		go func() {
			defer group.Done()
			_, created, err := CreateFirstAdmin(bg, pool, FirstAdmin{Username: name, PasswordHash: "hash"})
			if err != nil {
				t.Error(err)
			}
			if created {
				winners.Add(1)
			}
		}()
	}
	group.Wait()
	if winners.Load() != 1 {
		t.Fatalf("%d winners, want 1", winners.Load())
	}
	if count := countRows(t, pool, "SELECT count(*) FROM users"); count != 1 {
		t.Fatalf("%d users, want 1", count)
	}
}
