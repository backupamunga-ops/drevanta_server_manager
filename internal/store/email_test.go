package store

// v0.4.16: email column, case-insensitive uniqueness, identifier lookup.

import (
	"context"
	"errors"
	"testing"
)

func TestEmailUniquenessAndIdentifierLookup(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	a, err := st.CreateUser(ctx, "admin", "hash-a", "Op@Example.com")
	if err != nil {
		t.Fatal(err)
	}
	if a.Email != "Op@Example.com" {
		t.Fatalf("email = %q", a.Email)
	}

	// A different account cannot take the same email (case-insensitive).
	b, err := st.CreateUser(ctx, "second", "hash-b", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserEmail(ctx, b.ID, "op@example.com"); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("duplicate email err = %v, want ErrEmailTaken", err)
	}

	// A different email binds fine.
	if err := st.SetUserEmail(ctx, b.ID, "Second@Example.com"); err != nil {
		t.Fatal(err)
	}

	// Lookup by username...
	if u, err := st.UserByIdentifier(ctx, "admin"); err != nil || u.ID != a.ID {
		t.Fatalf("by username: %v %v", u, err)
	}
	// ...by email case-insensitively...
	if u, err := st.UserByIdentifier(ctx, "OP@example.com"); err != nil || u.ID != a.ID {
		t.Fatalf("by email: %v %v", u, err)
	}
	// ...and by the second account's email.
	if u, err := st.UserByIdentifier(ctx, "second@example.com"); err != nil || u.ID != b.ID {
		t.Fatalf("by second email: %v %v", u, err)
	}

	// Unknown identifier -> ErrNotFound.
	if _, err := st.UserByIdentifier(ctx, "ghost@example.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown identifier err = %v, want ErrNotFound", err)
	}

	// Clearing one email frees it for another account.
	if err := st.SetUserEmail(ctx, b.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserEmail(ctx, b.ID, "second@example.com"); err != nil {
		t.Fatalf("rebind after clear: %v", err)
	}

	// Password hash replacement.
	if err := st.UpdatePasswordHash(ctx, a.ID, "hash-a2"); err != nil {
		t.Fatal(err)
	}
	u, err := st.UserByID(ctx, a.ID)
	if err != nil || u.PasswordHash != "hash-a2" {
		t.Fatalf("password update: %v %v", u, err)
	}
}
