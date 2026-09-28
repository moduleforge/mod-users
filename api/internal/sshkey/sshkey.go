// Package sshkey implements mod-users' single, fixed SSH public-key
// acceptance policy (design note D6) and the canonicalization helpers that
// registration and resolution both depend on to derive byte-identical
// values from the same key. It is pure Go and has no database dependency.
package sshkey

import (
	"crypto/rsa"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// maxInputBytes caps the raw authorized_keys line accepted by Parse (D6).
const maxInputBytes = 16 * 1024

// minRSABits is the minimum accepted ssh-rsa modulus size (D6).
const minRSABits = 2048

// acceptedKeyTypes is the fixed D6 allow-list of PublicKey.Type() values
// this package accepts: Ed25519, the FIDO sk- variants, ECDSA P-256/384/521,
// and ssh-rsa (subject to the minRSABits check below). Every other type --
// including ssh-dss (DSA) and any *-cert-v01@openssh.com certificate type --
// is refused as unsupported by default, since it is simply absent from this
// list. The policy is fixed in code, not configurable (design note D6).
var acceptedKeyTypes = map[string]bool{
	ssh.KeyAlgoED25519:    true,
	ssh.KeyAlgoSKED25519:  true,
	ssh.KeyAlgoECDSA256:   true,
	ssh.KeyAlgoECDSA384:   true,
	ssh.KeyAlgoECDSA521:   true,
	ssh.KeyAlgoSKECDSA256: true,
	ssh.KeyAlgoRSA:        true,
}

// Parsed is the result of successfully parsing and validating one
// authorized_keys line.
type Parsed struct {
	// Key is the parsed public key.
	Key ssh.PublicKey
	// Type is Key.Type(), e.g. "ssh-ed25519".
	Type string
	// Canonical is the canonical authorized_keys text for Key: type plus
	// base64, with no comment and no options.
	Canonical string
	// Fingerprint is Key's SHA256 fingerprint, in "SHA256:<base64>" form.
	Fingerprint string
	// Comment is the trimmed comment field from the input line, if any.
	Comment string
}

// Parse parses and validates one user-submitted authorized_keys line against
// mod-users' fixed SSH key acceptance policy (design note D6). It rejects
// input over 16 KiB, input carrying authorized_keys options, input encoding
// more than one key, and any key whose type is not on the D6 allow-list;
// ssh-rsa keys under 2048 bits are refused as too weak. Callers should
// compare the returned error with errors.Is against ErrInvalid,
// ErrUnsupportedType, or ErrTooWeak. Error messages never echo the submitted
// key material.
func Parse(line string) (Parsed, error) {
	if len(line) > maxInputBytes {
		return Parsed{}, fmt.Errorf("sshkey: input exceeds %d bytes: %w", maxInputBytes, ErrInvalid)
	}

	key, comment, options, rest, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return Parsed{}, fmt.Errorf("sshkey: unparseable authorized_keys line: %w", ErrInvalid)
	}
	if len(options) > 0 {
		return Parsed{}, fmt.Errorf("sshkey: authorized_keys options are not accepted: %w", ErrInvalid)
	}
	if strings.TrimSpace(string(rest)) != "" {
		return Parsed{}, fmt.Errorf("sshkey: input encodes more than one key: %w", ErrInvalid)
	}

	if err := checkPolicy(key); err != nil {
		return Parsed{}, err
	}

	return Parsed{
		Key:         key,
		Type:        key.Type(),
		Canonical:   Canonical(key),
		Fingerprint: Fingerprint(key),
		Comment:     strings.TrimSpace(comment),
	}, nil
}

// checkPolicy enforces the D6 allow-list and the ssh-rsa minimum key size
// against an already-parsed key.
func checkPolicy(key ssh.PublicKey) error {
	keyType := key.Type()
	if !acceptedKeyTypes[keyType] {
		return fmt.Errorf("sshkey: key type %q is not accepted: %w", keyType, ErrUnsupportedType)
	}
	if keyType != ssh.KeyAlgoRSA {
		return nil
	}

	cryptoKey, ok := key.(ssh.CryptoPublicKey)
	if !ok {
		return fmt.Errorf("sshkey: ssh-rsa key does not expose its modulus: %w", ErrInvalid)
	}
	rsaKey, ok := cryptoKey.CryptoPublicKey().(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("sshkey: ssh-rsa key does not decode to an RSA public key: %w", ErrInvalid)
	}
	if rsaKey.N.BitLen() < minRSABits {
		return fmt.Errorf("sshkey: ssh-rsa modulus is %d bits, minimum is %d: %w", rsaKey.N.BitLen(), minRSABits, ErrTooWeak)
	}
	return nil
}

// Canonical returns the canonical authorized_keys text for key: type plus
// base64, with no comment and no options. Parse uses this helper
// internally; registration (task 003's Register) and resolution (task 003's
// ResolveActor) call it directly so all three derive byte-identical values
// from one implementation.
func Canonical(key ssh.PublicKey) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
}

// Fingerprint returns key's SHA256 fingerprint, in "SHA256:<base64>" form.
// See Canonical for why registration and resolution share this helper.
func Fingerprint(key ssh.PublicKey) string {
	return ssh.FingerprintSHA256(key)
}
