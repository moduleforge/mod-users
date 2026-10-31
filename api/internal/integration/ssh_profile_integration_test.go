//go:build integration

package integration

// Party-aware SSH profile derivation: SSHKeyResolver.AuthContextForOwner over
// the real database functions. The owner of the repository picks the profile:
// an active party profile when the key satisfies the party's gate, otherwise
// the personal profile unless the key is confined.

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

	"github.com/moduleforge/mod-users/api/internal/service"
	"github.com/moduleforge/mod-users/api/internal/sshkey"
)

// sshMember is a bring-your-own user with a registered SSH key, a personal
// profile and an active profile in a party.
type sshMember struct {
	userID, personal, partyProfile, credID int64
	key                                    ssh.PublicKey
	cred                                   service.SSHCredential
}

func (s *stack) newSSHMember(t *testing.T, party int64) sshMember {
	t.Helper()
	a := s.register(t)
	m := sshMember{userID: s.principalID(t, a.Email)}
	m.personal = s.personalProfileID(t, m.userID)
	m.partyProfile, _ = s.seedPartyProfile(t, m.userID, party, "active")
	var line string
	m.key, line = newSSHKey(t)
	view := s.do(t, http.MethodPost, "/v1/me/credentials/ssh-keys", a.tk.access(), map[string]any{"public_key": line}).
		wantStatus(t, http.StatusCreated).obj(t)
	m.credID = s.credentialID(t, view["uuid"].(string))
	m.cred = s.resolveSSH(t, m.key)
	return m
}

func (s *stack) resolveSSH(t *testing.T, key ssh.PublicKey) service.SSHCredential {
	t.Helper()
	cred, err := service.NewSSHKeyResolver(s.q, s.coreQ).ResolveCredential(context.Background(), key)
	if err != nil {
		t.Fatalf("ResolveCredential: %v", err)
	}
	return cred
}

func (s *stack) sshContext(cred service.SSHCredential, owner *int64) (int64, error) {
	ac, err := service.NewSSHKeyResolver(s.q, s.coreQ).AuthContextForOwner(context.Background(), cred, owner)
	return ac.ProfileID, err
}

func (s *stack) setSSHPolicy(t *testing.T, party int64, rule string, provider *string, minACR int) {
	t.Helper()
	s.exec(t, "party policy",
		`INSERT INTO mod_users.party_authn_policies (party_id, credential_rule, required_oidc_provider_id, min_acr)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (party_id) DO UPDATE SET credential_rule = EXCLUDED.credential_rule,
		   required_oidc_provider_id = EXCLUDED.required_oidc_provider_id, min_acr = EXCLUDED.min_acr`,
		party, rule, provider, minACR)
}

// wantNotAuthorized fails unless err is ErrSSHKeyNotAuthorized naming party
// (the zero uuid when partyID is 0).
func (s *stack) wantNotAuthorized(t *testing.T, err error, partyID int64) {
	t.Helper()
	if !errors.Is(err, service.ErrSSHKeyNotAuthorized) {
		t.Fatalf("err = %v, want ErrSSHKeyNotAuthorized", err)
	}
	var na *service.SSHKeyNotAuthorizedError
	if !errors.As(err, &na) {
		t.Fatalf("err = %v carries no SSHKeyNotAuthorizedError", err)
	}
	want := uuid.Nil
	if partyID != 0 {
		want = parseUUID(s.entityUUID(t, partyID))
	}
	if na.PartyUUID != want {
		t.Errorf("party uuid = %s, want %s", na.PartyUUID, want)
	}
}

func TestSSHProfile_PartyOwner(t *testing.T) {
	party := env.seedParty(t, "SSH Party "+uniq("p"))

	t.Run("any policy yields the party profile", func(t *testing.T) {
		m := env.newSSHMember(t, party)
		got, err := env.sshContext(m.cred, &party)
		if err != nil || got != m.partyProfile {
			t.Fatalf("profile = %d, %v; want party profile %d", got, err, m.partyProfile)
		}
		ac, _ := service.NewSSHKeyResolver(env.q, env.coreQ).AuthContextForOwner(context.Background(), m.cred, &party)
		if ac.PrincipalID != m.userID || ac.CredentialID != m.credID || ac.SessionID != 0 || ac.ACR != 1 || ac.AuthTime.IsZero() {
			t.Errorf("AuthContext = %+v", ac)
		}
	})

	t.Run("authorized policy", func(t *testing.T) {
		p := env.seedParty(t, "SSH Authorized "+uniq("p"))
		env.setSSHPolicy(t, p, "authorized", nil, 1)
		m := env.newSSHMember(t, p)
		_, err := env.sshContext(m.cred, &p)
		env.wantNotAuthorized(t, err, p)

		env.exec(t, "authorize key",
			`INSERT INTO mod_users.credential_party_authorizations (credential_id, party_id, authorized_by_profile_id) VALUES ($1, $2, $3)`,
			m.credID, p, m.partyProfile)
		if got, err := env.sshContext(m.cred, &p); err != nil || got != m.partyProfile {
			t.Fatalf("authorized key: profile = %d, %v; want %d", got, err, m.partyProfile)
		}
	})

	t.Run("provider policy refuses an ssh key", func(t *testing.T) {
		p := env.seedParty(t, "SSH Provider "+uniq("p"))
		prov := env.addProviderRow(t, "ssh-"+uniq("idp"))
		env.setSSHPolicy(t, p, "provider", &prov, 1)
		m := env.newSSHMember(t, p)
		_, err := env.sshContext(m.cred, &p)
		env.wantNotAuthorized(t, err, p)
	})

	t.Run("min_acr above the key refuses", func(t *testing.T) {
		p := env.seedParty(t, "SSH ACR "+uniq("p"))
		env.setSSHPolicy(t, p, "any", nil, 2)
		m := env.newSSHMember(t, p)
		_, err := env.sshContext(m.cred, &p)
		env.wantNotAuthorized(t, err, p)
	})

	t.Run("suspended or ended profile falls to personal", func(t *testing.T) {
		for _, status := range []string{"suspended", "ended"} {
			m := env.newSSHMember(t, party)
			if status == "ended" {
				env.exec(t, "end profile", `UPDATE profiles SET status = 'ended', ended_at = now(), end_reason = 'left' WHERE entity_id = $1`, m.partyProfile)
			} else {
				env.exec(t, "suspend profile", `UPDATE profiles SET status = 'suspended' WHERE entity_id = $1`, m.partyProfile)
			}
			if got, err := env.sshContext(m.cred, &party); err != nil || got != m.personal {
				t.Errorf("%s: profile = %d, %v; want personal %d", status, got, err, m.personal)
			}
		}
	})

	t.Run("owner that is not a party or the principal itself falls to personal", func(t *testing.T) {
		m := env.newSSHMember(t, party)
		other := env.seedParty(t, "SSH Other "+uniq("p"))
		for name, owner := range map[string]*int64{"nil": nil, "unrelated party": &other, "own user": &m.userID} {
			if got, err := env.sshContext(m.cred, owner); err != nil || got != m.personal {
				t.Errorf("%s: profile = %d, %v; want personal %d", name, got, err, m.personal)
			}
		}
	})
}

func TestSSHProfile_Confined(t *testing.T) {
	party := env.seedParty(t, "SSH Confine "+uniq("p"))
	other := env.seedParty(t, "SSH Confine Other "+uniq("p"))

	t.Run("confined key without an owner is refused", func(t *testing.T) {
		m := env.newSSHMember(t, party)
		env.exec(t, "confine", `UPDATE mod_users.credentials SET confined_to_party_id = $2 WHERE id = $1`, m.credID, party)
		m.cred = env.resolveSSH(t, m.key)
		_, err := env.sshContext(m.cred, nil)
		env.wantNotAuthorized(t, err, party)
		_, err = env.sshContext(m.cred, &other)
		env.wantNotAuthorized(t, err, party)
	})

	t.Run("confined key against its own party yields the party profile", func(t *testing.T) {
		m := env.newSSHMember(t, party)
		env.exec(t, "confine", `UPDATE mod_users.credentials SET confined_to_party_id = $2 WHERE id = $1`, m.credID, party)
		m.cred = env.resolveSSH(t, m.key)
		if got, err := env.sshContext(m.cred, &party); err != nil || got != m.partyProfile {
			t.Fatalf("profile = %d, %v; want %d", got, err, m.partyProfile)
		}
	})

	t.Run("confined key against another party it holds a profile in is refused", func(t *testing.T) {
		m := env.newSSHMember(t, party)
		env.seedPartyProfile(t, m.userID, other, "active")
		env.exec(t, "confine", `UPDATE mod_users.credentials SET confined_to_party_id = $2 WHERE id = $1`, m.credID, party)
		m.cred = env.resolveSSH(t, m.key)
		_, err := env.sshContext(m.cred, &other)
		env.wantNotAuthorized(t, err, other)
	})
}

func TestSSHProfile_ManagedUserWithoutPersonalProfile(t *testing.T) {
	party := env.seedParty(t, "SSH Managed "+uniq("p"))
	other := env.seedParty(t, "SSH Managed Other "+uniq("p"))
	userID := env.seedManagedUser(t, party, uniq("managed")+"@example.test", testPassword)
	profileID, _ := env.seedPartyProfile(t, userID, party, "active")

	key, _ := newSSHKey(t)
	var credID int64
	if err := env.pool.QueryRow(context.Background(),
		`INSERT INTO mod_users.credentials (principal_id, kind) VALUES ($1, 'ssh_key') RETURNING id`, userID).Scan(&credID); err != nil {
		t.Fatalf("insert credential: %v", err)
	}
	env.exec(t, "ssh key row",
		`INSERT INTO mod_users.cred_ssh_key (credential_id, fingerprint_sha256, key_type, public_key) VALUES ($1, $2, $3, $4)`,
		credID, sshkey.Fingerprint(key), key.Type(), sshkey.Canonical(key))
	cred := env.resolveSSH(t, key)

	if got, err := env.sshContext(cred, &party); err != nil || got != profileID {
		t.Fatalf("own party: profile = %d, %v; want %d", got, err, profileID)
	}
	for name, owner := range map[string]*int64{"nil": nil, "unrelated party": &other} {
		_, err := env.sshContext(cred, owner)
		if !errors.Is(err, service.ErrSSHKeyNotAuthorized) {
			t.Errorf("%s: err = %v, want ErrSSHKeyNotAuthorized", name, err)
		}
	}
}
