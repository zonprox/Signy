package fsm

import (
	"sync"

	"github.com/zonprox/Signy/internal/models"
)

// Store manages user session state with thread-safe in-memory storage.
type Store struct {
	mu       sync.RWMutex
	sessions map[int64]*models.UserSession
}

// NewStore creates a new FSM store.
func NewStore() *Store {
	return &Store{
		sessions: make(map[int64]*models.UserSession),
	}
}

// Get returns the current session for a user. Returns a zero session if none exists.
func (s *Store) Get(userID int64) *models.UserSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[userID]
	if !ok {
		return &models.UserSession{UserID: userID, State: models.StateIdle}
	}
	return sess
}

// Set stores a session for a user.
func (s *Store) Set(userID int64, session *models.UserSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[userID] = session
}

// Clear resets a user's session to idle.
func (s *Store) Clear(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, userID)
}

// SetState updates the state of a user's session.
func (s *Store) SetState(userID int64, state models.UserState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[userID]
	if !ok {
		sess = &models.UserSession{UserID: userID}
		s.sessions[userID] = sess
	}
	sess.State = state
}

// GetState returns the current state for a user.
func (s *Store) GetState(userID int64) models.UserState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[userID]
	if !ok {
		return models.StateIdle
	}
	return sess.State
}

// Transition validates and performs a state transition.
func (s *Store) Transition(userID int64, from, to models.UserState) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[userID]
	if !ok {
		if from != models.StateIdle {
			return false
		}
		sess = &models.UserSession{UserID: userID}
		s.sessions[userID] = sess
	}
	if sess.State != from {
		return false
	}
	sess.State = to
	return true
}
