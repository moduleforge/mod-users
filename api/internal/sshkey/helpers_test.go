package sshkey

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// authorizedKeysLine builds a "<type> <base64> [comment]" authorized_keys
// line for pub, mirroring what a user would submit.
func authorizedKeysLine(t *testing.T, pub ssh.PublicKey, comment string) string {
	t.Helper()
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	if comment == "" {
		return line
	}
	return line + " " + comment
}

func genEd25519(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("wrap ed25519 key: %v", err)
	}
	return sshPub
}

func genECDSA(t *testing.T, curve elliptic.Curve) ssh.PublicKey {
	t.Helper()
	priv, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatalf("generate ecdsa key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("wrap ecdsa key: %v", err)
	}
	return sshPub
}

func genRSA(t *testing.T, bits int) ssh.PublicKey {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("wrap rsa key: %v", err)
	}
	return sshPub
}

// The following are literal authorized_keys fixture lines for key types this
// package must refuse but that crypto/... cannot conveniently generate via
// ssh.NewPublicKey (the FIDO sk- variants) or that are deliberately excluded
// from the D6 allow-list (DSA, certificates). Each was generated once with
// golang.org/x/crypto/ssh (matching the version pinned in api/go.mod) and
// verified to round-trip through ssh.ParseAuthorizedKey before being pasted
// here as a fixed literal, so the fixture carries no live private key
// material and needs no regeneration at test time.

const dsaFixtureLine = "ssh-dss AAAAB3NzaC1kc3MAAACBAIibNa/UzcRaMAlmSFZULXNL6OJ4TmjixAy6xGBQKa9yNgeyxQH7ui0N+yafI4r9XpjJl2FSgyMVrET6B8CIzRig8VV8AKgSywPPGBWqbKAk75Zbm/orPj96Q1C34qz+5t2tYiVqmmzbn0BQjTLid3LbimpexgJdXsFgNbXXppl1AAAAFQCHFKTNWmU/9vZ0LbZi8FMtYZgkQQAAAIAikNXAq3tVinv2GwwuZS2/TSlqZivr+iLq6JdM53H/gOz3RWC5B0otnCPUw5GG+VDBuPqW87aCV145YdyQ8S+hgqPSEJPi/R6TMJB6l0B7bNxdob17nZr75N1dzpIOgUrPK8wdko81hzrJ/PvVjInhXWOt+Dr6z5AE7Bg8CZgwpgAAAIAEUF2nIU6i6XWfFxLt0hY3nnU5YAqjowyAz9XPK1aAWMNg0ilmXTN30IjrSrnoJgvQmMiyE5gm4d50/Voa73CHm8zGOssffoGE35kC4Ws2o0vyA4oTBYSgzEpLh7Pxs01HpEHF2mqy/ldidlpWbg4U5uMgj3x3sznSPxEJGDODNw== test-dsa"

const skEd25519FixtureLine = "sk-ssh-ed25519@openssh.com AAAAGnNrLXNzaC1lZDI1NTE5QG9wZW5zc2guY29tAAAAIE+/3KmoOIDfLsjcWgLwNOWfUB6kJqJDqNEBty6G6F56AAAABHNzaDo= test-sk-ed25519"

const skECDSAFixtureLine = "sk-ecdsa-sha2-nistp256@openssh.com AAAAInNrLWVjZHNhLXNoYTItbmlzdHAyNTZAb3BlbnNzaC5jb20AAAAIbmlzdHAyNTYAAABBBIQ2O0QJUy982SoiEiN1TAVCt/EiqpTikuDzxWaYVoH8OC0k0JEDdFSQLzNmHWgAlHR/K/fVzCDGyijQpqzxmdEAAAAEc3NoOg== test-sk-ecdsa"

// certFixtureLine is an ssh-ed25519-cert-v01@openssh.com user certificate,
// built with ssh.Certificate and signed by a throwaway, discarded ed25519 CA
// key generated solely to produce this fixture.
const certFixtureLine = "ssh-ed25519-cert-v01@openssh.com AAAAIHNzaC1lZDI1NTE5LWNlcnQtdjAxQG9wZW5zc2guY29tAAAAIBqA28u3hCSI/0tk8vYmausg8t3lsu+36ucaCR3E4qR5AAAAIGNsoHCbbwj13OAHnf9tSkEVS2q2Bl/U4lYjVXQsNXTuAAAAAAAAAAEAAAABAAAADHRlc3QtZml4dHVyZQAAAA0AAAAJdGVzdC11c2VyAAAAAAAAAAD//////////wAAAAAAAAAAAAAAAAAAADMAAAALc3NoLWVkMjU1MTkAAAAg/bzFDqADLNGx+kWgUpeopWJ2ZwPkHRRdaJ0zsE87nVUAAABTAAAAC3NzaC1lZDI1NTE5AAAAQCx+3Y2PIg84oMfjIa/L13myFCXJoQ89rUZ3uvk5nyGMb/vB9bMNnK0lIfc9sawCipfN4+ncjwgY1XpkeC6FJQs= test-cert"
