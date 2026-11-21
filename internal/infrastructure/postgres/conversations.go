package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

var _ agent.ConversationStore = (*Store)(nil)

// storedConversation versions the durable format independently of the model contract.
// Rescheduling's internal identifiers are intentionally absent from its model-facing JSON.
type storedConversation struct {
	Version              int
	State                agent.Conversation
	ReschedulingDoctorID uuid.UUID
	ReschedulingSlotID   uuid.UUID
}

// Acquire holds a session advisory lock, not a transaction, across the turn.
// Saves commit independently so a crash cannot restore consumed proposal authority.
// All instances use the database/schema/conversation key; Release returns the session.
func (s *Store) Acquire(
	ctx context.Context, patientID, id uuid.UUID, access agent.ConversationAccess,
) (agent.LockedConversation, error) {
	if patientID == uuid.Nil || id == uuid.Nil {
		return nil, domain.ErrNotFound
	}

	conn, err := s.conversationDB.Conn(ctx)
	if err != nil {
		return nil, err
	}

	locked := &lockedPostgresConversation{conn: conn, patientID: patientID, id: id}

	success := false
	defer func() {
		if !success {
			locked.Release()
		}
	}()

	var owned bool

	err = conn.QueryRowContext(ctx,
		`SELECT hashtextextended('clinic-conversation:' || current_database() || ':' ||
		current_schema() || ':' || $1, 0),
		EXISTS (SELECT 1 FROM conversations WHERE id = $1::uuid AND patient_id = $2)`, id.String(), patientID,
	).Scan(&locked.key, &owned)
	if err != nil {
		return nil, err
	}

	if !access.CreateConversation && !owned {
		return nil, domain.ErrNotFound
	}

	if access.Wait {
		_, err = conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", locked.key)
		locked.held = err == nil
	} else {
		err = conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", locked.key).Scan(&locked.held)
	}

	if err != nil {
		// Cancellation may race with lock acquisition. Never pool an uncertain session.
		locked.discard()

		return nil, err
	}

	if !locked.held {
		return nil, agent.ErrConversationBusy
	}

	var state, messages []byte

	err = conn.QueryRowContext(ctx,
		"SELECT state, messages FROM conversations WHERE id = $1 AND patient_id = $2", id, patientID,
	).Scan(&state, &messages)
	if errors.Is(err, sql.ErrNoRows) && access.CreateConversation {
		var exists bool

		err = conn.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM conversations WHERE id = $1)", id).
			Scan(&exists)
		if err == nil && exists {
			err = domain.ErrNotFound
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		err = domain.ErrNotFound
	} else if err == nil {
		var stored storedConversation

		err = json.Unmarshal(state, &stored)
		if err == nil && stored.Version != 1 {
			err = fmt.Errorf("unsupported conversation format: %d", stored.Version)
		}

		if err == nil {
			err = json.Unmarshal(messages, &stored.State.History)
		}

		if err == nil {
			if stored.State.Rescheduling != nil {
				stored.State.Rescheduling.DoctorID = stored.ReschedulingDoctorID
				stored.State.Rescheduling.SlotID = stored.ReschedulingSlotID
			}

			locked.state, locked.saved = stored.State, true
		}
	}

	if err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	success = true

	return locked, nil
}

type lockedPostgresConversation struct {
	conn      *sql.Conn
	patientID uuid.UUID
	id        uuid.UUID
	key       int64
	held      bool
	saved     bool
	state     agent.Conversation
}

func (l *lockedPostgresConversation) Snapshot() agent.Conversation {
	return l.state.Clone()
}

func (l *lockedPostgresConversation) Save(ctx context.Context, state agent.Conversation) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if l.conn == nil || !l.held {
		return errors.New("conversation access released")
	}

	stored := storedConversation{Version: 1, State: state}
	stored.State.History = nil
	if state.Rescheduling != nil {
		stored.ReschedulingDoctorID = state.Rescheduling.DoctorID
		stored.ReschedulingSlotID = state.Rescheduling.SlotID
	}

	encoded, err := json.Marshal(stored)
	if err != nil {
		return err
	}

	history := state.History
	if history == nil {
		history = []agent.Message{}
	}

	messages, err := json.Marshal(history)
	if err != nil {
		return err
	}

	query := `UPDATE conversations SET state = $3, messages = $4, updated_at = now()
		WHERE id = $1 AND patient_id = $2`
	if !l.saved {
		query = `INSERT INTO conversations (id, patient_id, state, messages) VALUES ($1, $2, $3, $4)
			ON CONFLICT (id) DO NOTHING`
	}

	result, err := l.conn.ExecContext(ctx, query, l.id, l.patientID, string(encoded), string(messages))
	if err != nil {
		return err
	}

	count, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if count != 1 {
		return domain.ErrNotFound
	}

	l.state, l.saved = state.Clone(), true

	return nil
}

func (l *lockedPostgresConversation) discard() {
	_ = l.conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = l.conn.Close()
	l.conn = nil
}

func (l *lockedPostgresConversation) Release() {
	if l.conn == nil {
		return
	}

	if l.held {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var unlocked bool

		err := l.conn.QueryRowContext(ctx, "SELECT pg_advisory_unlock($1)", l.key).Scan(&unlocked)
		if err != nil || !unlocked {
			l.discard()

			return
		}
	}

	_ = l.conn.Close()
	l.conn = nil
}
