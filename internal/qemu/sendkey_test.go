package qemu

import "testing"

// TestPasswordKeysHexAlphabet pins the format sendkey expects: one key name
// per character, in order, for the alphabet RandomConsolePassword actually
// produces (0-9, a-f): every character in it is a bare QEMU key name, so the
// sequence should just be the characters themselves.
func TestPasswordKeysHexAlphabet(t *testing.T) {
	got, err := passwordKeys("a1b2c3")
	if err != nil {
		t.Fatalf("passwordKeys(hex password) returned error: %v", err)
	}
	want := []string{"a", "1", "b", "2", "c", "3"}
	if len(got) != len(want) {
		t.Fatalf("passwordKeys(\"a1b2c3\") = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("passwordKeys(\"a1b2c3\")[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestPasswordKeysRefusesOutOfAlphabet proves the safety rule: a password
// with any character outside the bare-key-name allowlist (digits and
// lowercase letters) must be refused outright, not sent partially or typed
// wrong. An uppercase letter needs shift, and punctuation is not guaranteed
// to be a bare key name, and both are out of scope on purpose.
func TestPasswordKeysRefusesOutOfAlphabet(t *testing.T) {
	cases := []string{"Secret1", "hunter2!", "pass word", "café"}
	for _, pw := range cases {
		if _, err := passwordKeys(pw); err == nil {
			t.Errorf("passwordKeys(%q) = nil error, want a refusal", pw)
		}
	}
}
