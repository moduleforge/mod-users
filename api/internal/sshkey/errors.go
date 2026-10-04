package sshkey

import "errors"

// ErrInvalid indicates the submitted authorized_keys line could not be
// parsed, carried options, encoded more than one key, or exceeded the
// maximum input size. Maps to detail code users.ssh_key_invalid.
var ErrInvalid = errors.New("sshkey: invalid key or input")

// ErrUnsupportedType indicates the key's algorithm is not on the D6
// allow-list (for example ssh-dss or a *-cert-v01@openssh.com certificate).
// Maps to detail code users.ssh_key_type_unsupported.
var ErrUnsupportedType = errors.New("sshkey: unsupported key type")

// ErrTooWeak indicates an ssh-rsa key with a modulus under 2048 bits. Maps to
// detail code users.ssh_key_too_weak.
var ErrTooWeak = errors.New("sshkey: key too weak")

// ErrLabelTooLong indicates a caller-supplied label exceeds 100 runes. Maps
// to detail code users.ssh_key_label_too_long.
var ErrLabelTooLong = errors.New("sshkey: label too long")

// ErrLabelInvalidChars indicates a caller-supplied label contains a control
// or bidi formatting character. Maps to detail code
// users.ssh_key_label_invalid.
var ErrLabelInvalidChars = errors.New("sshkey: label contains invalid characters")
