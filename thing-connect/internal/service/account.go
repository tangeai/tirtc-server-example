package service

import (
	"context"
	"errors"
)

type Account struct {
	ID    int64  `json:"user_id" db:"id"`
	Email string `json:"email" db:"email"`
}
type AccountReader interface {
	ReadAccount(context.Context, int64) (Account, error)
}
type AccountService struct{ reader AccountReader }

func NewAccountService(reader AccountReader) *AccountService { return &AccountService{reader: reader} }
func (s *AccountService) Current(ctx context.Context, id int64) (Account, error) {
	if id <= 0 {
		return Account{}, errors.New("账号无效")
	}
	return s.reader.ReadAccount(ctx, id)
}
