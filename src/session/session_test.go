package session

import (
	"testing"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	s := NewSigner("super-secret")
	token := s.Sign("hello-state")
	got, ok := s.Verify(token)
	if !ok {
		t.Fatalf("Verify(%q) failed for a token we just signed", token)
	}
	if got != "hello-state" {
		t.Errorf("value = %q, want hello-state", got)
	}
}

func TestVerifyRejectsTamperAndWrongKey(t *testing.T) {
	s := NewSigner("super-secret")
	token := s.Sign("hello-state")

	if _, ok := s.Verify(token + "x"); ok {
		t.Error("tampered token was accepted")
	}
	if _, ok := s.Verify("hello-state"); ok {
		t.Error("unsigned value was accepted")
	}
	if _, ok := NewSigner("other-secret").Verify(token); ok {
		t.Error("token signed with a different key was accepted")
	}
}
