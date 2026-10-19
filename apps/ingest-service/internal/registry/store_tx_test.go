package registry

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

// These tests need no database: they run the store's transaction handling
// against a fake that records how the transaction ends.

// fakeTx stands in for pgx.Tx. Like pgx's, its Rollback after the transaction
// has ended is a no-op that reports ErrTxClosed. Any other method panics: the
// embedded Tx is nil.
type fakeTx struct {
	pgx.Tx
	closed    bool
	commitErr error
	commits   int
	rollbacks int
}

func (t *fakeTx) Commit(context.Context) error {
	if t.closed {
		return pgx.ErrTxClosed
	}
	t.closed = true
	t.commits++
	return t.commitErr
}

func (t *fakeTx) Rollback(context.Context) error {
	if t.closed {
		return pgx.ErrTxClosed
	}
	t.closed = true
	t.rollbacks++
	return nil
}

// fakeDB hands out one fakeTx and remembers the options it began with.
type fakeDB struct {
	beginErr  error
	commitErr error
	opts      pgx.TxOptions
	tx        *fakeTx
}

func (db *fakeDB) BeginTx(_ context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	db.opts = opts
	if db.beginErr != nil {
		return nil, db.beginErr
	}
	db.tx = &fakeTx{commitErr: db.commitErr}
	return db.tx, nil
}

// The port guard needs its read to start after the lock wait, which only
// READ COMMITTED gives, whatever the server's default isolation level is.
func TestInTxPinsReadCommitted(t *testing.T) {
	db := &fakeDB{}
	if err := runInTx(context.Background(), db, func(pgx.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if db.opts.IsoLevel != pgx.ReadCommitted {
		t.Errorf("the transaction began with isolation %q, want %q", db.opts.IsoLevel, pgx.ReadCommitted)
	}
}

// A transaction ends one way: committed when fn succeeds, rolled back when it
// fails or panics. A panic must not leave it open: it may hold the port lock,
// and its connection would never go back to the pool.
func TestInTxCommitsOrRollsBack(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")

	t.Run("commits when fn succeeds", func(t *testing.T) {
		db := &fakeDB{}
		if err := runInTx(ctx, db, func(pgx.Tx) error { return nil }); err != nil {
			t.Fatal(err)
		}
		if db.tx.commits != 1 || db.tx.rollbacks != 0 {
			t.Errorf("commits %d, rollbacks %d, want 1 and 0", db.tx.commits, db.tx.rollbacks)
		}
	})

	t.Run("rolls back and returns fn's error", func(t *testing.T) {
		db := &fakeDB{}
		if err := runInTx(ctx, db, func(pgx.Tx) error { return boom }); err != boom {
			t.Fatalf("err = %v, want %v as it is", err, boom)
		}
		if db.tx.commits != 0 || db.tx.rollbacks != 1 {
			t.Errorf("commits %d, rollbacks %d, want 0 and 1", db.tx.commits, db.tx.rollbacks)
		}
	})

	t.Run("rolls back when fn panics, and the panic goes on", func(t *testing.T) {
		db := &fakeDB{}
		var recovered any
		func() {
			defer func() { recovered = recover() }()
			_ = runInTx(ctx, db, func(pgx.Tx) error { panic("guard failed") })
		}()
		if recovered != "guard failed" {
			t.Fatalf("recovered %v, want the panic to reach the caller", recovered)
		}
		if db.tx.commits != 0 || db.tx.rollbacks != 1 {
			t.Errorf("commits %d, rollbacks %d, want 0 and 1: the panic left the transaction open", db.tx.commits, db.tx.rollbacks)
		}
	})

	t.Run("returns a failed commit without a second ending", func(t *testing.T) {
		db := &fakeDB{commitErr: boom}
		if err := runInTx(ctx, db, func(pgx.Tx) error { return nil }); err != boom {
			t.Fatalf("err = %v, want %v as it is", err, boom)
		}
		if db.tx.commits != 1 || db.tx.rollbacks != 0 {
			t.Errorf("commits %d, rollbacks %d, want 1 and 0", db.tx.commits, db.tx.rollbacks)
		}
	})

	t.Run("runs nothing when the transaction cannot begin", func(t *testing.T) {
		db := &fakeDB{beginErr: boom}
		ran := false
		err := runInTx(ctx, db, func(pgx.Tx) error { ran = true; return nil })
		if !errors.Is(err, boom) || ran {
			t.Errorf("err = %v, fn ran = %v, want the begin error and no run", err, ran)
		}
	})
}
