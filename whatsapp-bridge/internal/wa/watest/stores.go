package watest

import (
	"context"
	"sync"

	"go.mau.fi/whatsmeow/types"
)

// LIDMap is an in-memory wa.LIDStore.
type LIDMap struct {
	mu    sync.Mutex
	toPN  map[types.JID]types.JID
	toLID map[types.JID]types.JID
}

// NewLIDMap is an empty map.
func NewLIDMap() *LIDMap {
	return &LIDMap{toPN: map[types.JID]types.JID{}, toLID: map[types.JID]types.JID{}}
}

// Put records that lid and pn are the same account.
func (m *LIDMap) Put(lid, pn types.JID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.toPN[lid.ToNonAD()] = pn.ToNonAD()
	m.toLID[pn.ToNonAD()] = lid.ToNonAD()
}

// GetPNForLID implements wa.LIDStore; an unknown LID gives the empty JID.
func (m *LIDMap) GetPNForLID(_ context.Context, lid types.JID) (types.JID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.toPN[lid.ToNonAD()], nil
}

// GetLIDForPN implements wa.LIDStore; an unknown number gives the empty JID.
func (m *LIDMap) GetLIDForPN(_ context.Context, pn types.JID) (types.JID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.toLID[pn.ToNonAD()], nil
}

// ContactBook is an in-memory wa.ContactStore.
type ContactBook struct {
	mu       sync.Mutex
	contacts map[types.JID]types.ContactInfo
}

// NewContactBook is an empty address book.
func NewContactBook() *ContactBook {
	return &ContactBook{contacts: map[types.JID]types.ContactInfo{}}
}

// Put stores a contact; Found is set.
func (b *ContactBook) Put(jid types.JID, info types.ContactInfo) {
	b.mu.Lock()
	defer b.mu.Unlock()
	info.Found = true
	b.contacts[jid.ToNonAD()] = info
}

// GetContact implements wa.ContactStore; an unknown user is not Found.
func (b *ContactBook) GetContact(_ context.Context, user types.JID) (types.ContactInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.contacts[user.ToNonAD()], nil
}
