package health

import (
	"context"
	"errors"
	"testing"
	"time"

	"cloudferry/internal/store"
)

type fakeRC struct{ err error }

func (f fakeRC) Call(context.Context, string, any, any) error { return f.err }

func TestCheckMarksBrokenCredentials(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.AddAccount(store.Account{ID: "a1", Remote: "cf_a1", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("add: %v", err)
	}

	checker := New(st, fakeRC{err: errors.New("error listing: [401 - UnauthorizedError]")}, time.Minute)
	checker.CheckAll(context.Background())

	account, _ := st.Account("a1")
	if account.Status != store.AccountStatusAuth {
		t.Fatalf("status = %q", account.Status)
	}

	checker = New(st, fakeRC{}, time.Minute)
	checker.CheckAll(context.Background())
	account, _ = st.Account("a1")
	if account.Status != store.AccountStatusOK {
		t.Fatalf("status after recovery = %q", account.Status)
	}
}

func TestCheckIgnoresTemporaryErrors(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.AddAccount(store.Account{ID: "a1", Remote: "cf_a1", Status: store.AccountStatusOK, CreatedAt: time.Now()}); err != nil {
		t.Fatalf("add: %v", err)
	}
	checker := New(st, fakeRC{err: errors.New("i/o timeout")}, time.Minute)
	checker.CheckAll(context.Background())

	account, _ := st.Account("a1")
	if account.Status != store.AccountStatusOK {
		t.Fatalf("status = %q, want ok", account.Status)
	}
}
