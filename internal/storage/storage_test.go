package storage

import "testing"

func TestHashAPIKeyDeterministicAndNonPlaintext(t *testing.T) {
	first := HashAPIKey("secret")
	second := HashAPIKey("secret")
	other := HashAPIKey("different")
	if first != second {
		t.Fatal("same key produced different hashes")
	}
	if first == other {
		t.Fatal("different keys produced same hash")
	}
	if string(first[:]) == "secret" {
		t.Fatal("hash contains plaintext key")
	}
}
