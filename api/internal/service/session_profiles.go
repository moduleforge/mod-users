package service

import "context"

// Profiles lists the principal's non-ended profiles with their availability to
// the session, including the invite-only app gate of the session's app
// (GET /v1/me/profiles). A revoked or expired session is ErrSessionRevoked.
func (s *SessionService) Profiles(ctx context.Context, sessionID int64) ([]ProfileView, error) {
	sess, err := s.liveSession(ctx, s.q, sessionID)
	if err != nil {
		return nil, err
	}
	sc, err := s.loadSessionContext(ctx, s.pool, sess)
	if err != nil {
		return nil, err
	}
	return s.sessionViews(ctx, s.pool, sc)
}
