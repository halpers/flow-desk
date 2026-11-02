package model

import (
	"database/sql"
	"errors"
)

var ErrVersionConflict = errors.New("conditional database update did not affect one row")

func requireOne(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrVersionConflict
	}
	return nil
}
