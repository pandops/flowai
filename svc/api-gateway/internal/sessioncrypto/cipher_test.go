package sessioncrypto

import (
	"bytes"
	"testing"
)

func TestCipherRoundTripAndAssociatedData(t *testing.T) {
	t.Parallel()
	cipher, err := New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := cipher.Encrypt([]byte("provider-token"), []byte("session-a"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(envelope, []byte("provider-token")) {
		t.Fatal("plaintext appears in envelope")
	}
	plaintext, err := cipher.Decrypt(envelope, []byte("session-a"))
	if err != nil || string(plaintext) != "provider-token" {
		t.Fatalf("plaintext = %q, err = %v", plaintext, err)
	}
	if _, err := cipher.Decrypt(envelope, []byte("session-b")); err == nil {
		t.Fatal("wrong associated data accepted")
	}
}
