package shared

import "github.com/google/uuid"

// ID は各集約・エンティティで共通して使う識別子。
type ID uuid.UUID

func NewID() ID {
	return ID(uuid.New())
}

func (id ID) String() string {
	return uuid.UUID(id).String()
}

// IsZero はIDが未設定（ゼロ値）かどうかを返す。
func (id ID) IsZero() bool {
	return uuid.UUID(id) == uuid.Nil
}
