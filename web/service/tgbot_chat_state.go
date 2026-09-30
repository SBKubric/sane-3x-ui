package service

import (
	"sync"

	"github.com/mymmrac/telego"
)

// The chat state (#200, docs/spec/users.md §10): the text a chat waits for,
// such as a search or a step of «➕ New user». The bot serves its updates
// from a pool of goroutines, so two chats — two admins at once — use the
// store at the same time; each chat has its own state, as it has its own
// screen (botScreens) and draft (usersSessions).

// chatStateStore is the chats' states, safe for concurrent use.
type chatStateStore struct {
	mu sync.Mutex
	m  map[int64]string
}

// userStates is the state of every chat that waits for a text.
var userStates = &chatStateStore{m: map[int64]string{}}

// get is the state the chat waits in; false when it waits for no text.
func (s *chatStateStore) get(chatId int64) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.m[chatId]
	return state, ok
}

// set makes the chat wait in state.
func (s *chatStateStore) set(chatId int64, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[chatId] = state
}

// clear makes the chat wait for no text.
func (s *chatStateStore) clear(chatId int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, chatId)
}

// answerChatState hands a text to the flow the chat waits in; false when
// the chat waits for no text.
func (t *Tgbot) answerChatState(message *telego.Message) bool {
	state, waiting := userStates.get(message.Chat.ID)
	if !waiting {
		return false
	}
	if !t.answerNotifyText(message, state) { // the notification channel (#202)
		t.answerUsersText(message, state)
	}
	return true
}
