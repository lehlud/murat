package pgp

import (
	"strings"
	"testing"
)

func TestClearSignedTextExtractsBody(t *testing.T) {
	input := "noise\n-----BEGIN PGP SIGNED MESSAGE-----\nHash: SHA512\n\nHello\n- - dash line\n\n-----BEGIN PGP SIGNATURE-----\nabc\n-----END PGP SIGNATURE-----"
	got, ok := clearSignedText(input)
	if !ok {
		t.Fatal("clearSignedText() failed")
	}
	want := "Hello\n- dash line"
	if got != want {
		t.Fatalf("clearSignedText() = %q, want %q", got, want)
	}
}

func TestProcessTextShowsClearSignedBodyOnVerifyFailure(t *testing.T) {
	input := "-----BEGIN PGP SIGNED MESSAGE-----\nHash: SHA512\n\nHello\n\n-----BEGIN PGP SIGNATURE-----\ninvalid\n-----END PGP SIGNATURE-----"
	got, status, processed := ProcessText(input)
	if !processed {
		t.Fatal("ProcessText() did not process clearsigned text")
	}
	if got != "Hello" {
		t.Fatalf("ProcessText() text = %q", got)
	}
	if !strings.HasPrefix(status, "pgp:") {
		t.Fatalf("ProcessText() status = %q", status)
	}
}

func TestIsPublicKeyAttachment(t *testing.T) {
	if !IsPublicKeyAttachment("key.asc", "text/plain", []byte("x")) {
		t.Fatal(".asc key not detected")
	}
	if !IsPublicKeyAttachment("key.txt", "application/pgp-keys", []byte("x")) {
		t.Fatal("pgp-keys content type not detected")
	}
	if !IsPublicKeyAttachment("key.txt", "text/plain", []byte("-----BEGIN PGP PUBLIC KEY BLOCK-----")) {
		t.Fatal("armored key block not detected")
	}
	if IsPublicKeyAttachment("note.txt", "text/plain", []byte("hello")) {
		t.Fatal("plain text detected as key")
	}
}

func TestSignatureIDFromStatus(t *testing.T) {
	id := signatureIDFromStatus("[GNUPG:] VALIDSIG 5D4C73B5C49BC3D970832EBC43008C9A747C03F3 2026-06-18")
	if id.fingerprint != "5D4C73B5C49BC3D970832EBC43008C9A747C03F3" {
		t.Fatalf("fingerprint = %q", id.fingerprint)
	}
	if id.keyID != "43008C9A747C03F3" {
		t.Fatalf("keyID = %q", id.keyID)
	}
}

func TestSignatureIDMatchesFingerprintByKeyID(t *testing.T) {
	left := signatureID{fingerprint: "5D4C73B5C49BC3D970832EBC43008C9A747C03F3"}
	right := signatureID{keyID: "43008C9A747C03F3"}
	if !left.matches(right) {
		t.Fatalf("%#v should match %#v", left, right)
	}
}

func TestTrustName(t *testing.T) {
	if got := trustName("u"); got != "ultimate trust" {
		t.Fatalf("trustName(u) = %q", got)
	}
}

func TestSignedMIMEEntityWrapsOriginalEntity(t *testing.T) {
	entity := "Content-Type: text/plain; charset=utf-8\r\n\r\nhello"
	got := signedMIMEEntity(entity, "-----BEGIN PGP SIGNATURE-----\nabc\n-----END PGP SIGNATURE-----")
	for _, want := range []string{"Content-Type: multipart/signed", "protocol=\"application/pgp-signature\"", entity, "signature.asc", "-----BEGIN PGP SIGNATURE-----"} {
		if !strings.Contains(got, want) {
			t.Fatalf("signed MIME missing %q:\n%s", want, got)
		}
	}
}

func TestEncryptedMIMEEntityWrapsArmoredMessage(t *testing.T) {
	got := encryptedMIMEEntity("-----BEGIN PGP MESSAGE-----\nabc\n-----END PGP MESSAGE-----")
	for _, want := range []string{"Content-Type: multipart/encrypted", "protocol=\"application/pgp-encrypted\"", "Version: 1", "encrypted.asc", "-----BEGIN PGP MESSAGE-----"} {
		if !strings.Contains(got, want) {
			t.Fatalf("encrypted MIME missing %q:\n%s", want, got)
		}
	}
}
