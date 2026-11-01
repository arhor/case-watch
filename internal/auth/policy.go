// Package auth implements ADR 0005's shared startup eligibility policy.
package auth

import "errors"

type Policy struct {
	allowAll bool
	allowed  map[int64]struct{}
}

func New(allowAll bool, ids []int64) *Policy {
	p := &Policy{allowAll: allowAll, allowed: make(map[int64]struct{}, len(ids))}
	for _, id := range ids {
		if id > 0 {
			p.allowed[id] = struct{}{}
		}
	}
	return p
}

// Allows applies equally to commands, checks, and delivery. It grants no access
// to another owner's data. Invalid numeric identities are never eligible.
func (p *Policy) Allows(id int64) bool {
	if id <= 0 {
		return false
	}
	_, listed := p.allowed[id]
	return p.allowAll || listed
}

// Identity carries only transport-extracted values, never command arguments.
// SenderIsBot rejects bot senders; ChatType must be Telegram's ordinary private
// chat type. Private chat and sender IDs must identify the same positive user.
type Identity struct {
	SenderID, ChatID int64
	ChatType         string
	SenderIsBot      bool
}

type Owner struct{ id int64 }

func (o Owner) ID() int64 { return o.id }

func (p *Policy) Authenticate(identity Identity) (Owner, error) {
	if identity.ChatType != "private" || identity.SenderIsBot || identity.SenderID <= 0 || identity.ChatID != identity.SenderID || !p.Allows(identity.SenderID) {
		return Owner{}, errors.New("access denied")
	}
	return Owner{id: identity.SenderID}, nil
}

// RequireOwner is also required at application/storage boundaries; routing alone
// does not enforce ownership. The zero Owner never grants access.
func (o Owner) RequireOwner(storedOwnerID int64) error {
	if o.id <= 0 || o.id != storedOwnerID {
		return errors.New("access denied")
	}
	return nil
}
