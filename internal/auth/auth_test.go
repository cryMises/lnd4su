package auth

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// Proves Go's bcrypt accepts the $2a$ prefix, so the in-memory $2y$ -> $2a$
// swap in Verify() is sound. This is the pre-wiring check the spec asks for.
func TestBcryptAcceptsSwappedYY(t *testing.T) {
	const pw = "rahasia123"
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	y := "$2y$" + string(h)[4:]
	if !Verify(&Entry{Hash: y}, pw) {
		t.Fatal("$2y$ hash must verify after in-memory prefix swap")
	}
	if Verify(&Entry{Hash: y}, "salah") {
		t.Fatal("wrong password must not verify")
	}
}
