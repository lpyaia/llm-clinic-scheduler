-- +goose Up
CREATE TABLE conversations (
    id uuid PRIMARY KEY,
    patient_id uuid NOT NULL REFERENCES patients(id),
    state jsonb NOT NULL CHECK (jsonb_typeof(state) = 'object'),
    messages jsonb NOT NULL CHECK (jsonb_typeof(messages) = 'array'),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX conversations_patient ON conversations (patient_id);

-- +goose Down
DROP TABLE conversations;
