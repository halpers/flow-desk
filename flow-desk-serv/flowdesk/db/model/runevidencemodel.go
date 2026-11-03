package model

import (
	"context"
	"fmt"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

var _ RunEvidenceModel = (*customRunEvidenceModel)(nil)

type (
	// RunEvidenceModel is an interface to be customized, add more methods here,
	// and implement the added methods in customRunEvidenceModel.
	RunEvidenceModel interface {
		runEvidenceModel
		withSession(session sqlx.Session) RunEvidenceModel
		InsertTx(context.Context, sqlx.Session, *RunEvidence) error
		FindOneForRunTx(context.Context, sqlx.Session, string, string, string) (*RunEvidence, error)
	}

	customRunEvidenceModel struct {
		*defaultRunEvidenceModel
	}
)

// NewRunEvidenceModel returns a model for the database table.
func NewRunEvidenceModel(conn sqlx.SqlConn) RunEvidenceModel {
	return &customRunEvidenceModel{
		defaultRunEvidenceModel: newRunEvidenceModel(conn),
	}
}
func (m *customRunEvidenceModel) InsertTx(ctx context.Context, tx sqlx.Session, row *RunEvidence) error {
	_, err := m.withSession(tx).Insert(ctx, row)
	return err
}
func (m *customRunEvidenceModel) FindOneForRunTx(ctx context.Context, tx sqlx.Session, tenant, run, id string) (*RunEvidence, error) {
	var row RunEvidence
	err := tx.QueryRowCtx(ctx, &row, fmt.Sprintf("select %s from %s where tenant_id=$1 and run_id=$2 and id=$3", runEvidenceRows, m.table), tenant, run, id)
	return &row, err
}

func (m *customRunEvidenceModel) withSession(session sqlx.Session) RunEvidenceModel {
	return NewRunEvidenceModel(sqlx.NewSqlConnFromSession(session))
}
