-- +goose Up
CREATE TABLE patients (
    id uuid PRIMARY KEY,
    full_name text NOT NULL CHECK (length(trim(full_name)) BETWEEN 1 AND 200),
    phone text NOT NULL UNIQUE CHECK (phone ~ '^\+[1-9][0-9]{7,14}$')
);

CREATE TABLE sessions (
    token_hash text PRIMARY KEY CHECK (length(token_hash) = 64),
    patient_id uuid NOT NULL REFERENCES patients(id),
    expires_at timestamptz NOT NULL
);

CREATE INDEX sessions_expiry ON sessions(expires_at);

CREATE TABLE specialties (id uuid PRIMARY KEY, name text NOT NULL UNIQUE);

CREATE TABLE doctors (
    id uuid PRIMARY KEY, name text NOT NULL,
    specialty_id uuid NOT NULL REFERENCES specialties(id)
);

CREATE INDEX doctors_specialty ON doctors(specialty_id);

CREATE TABLE slots (
    id uuid PRIMARY KEY,
    doctor_id uuid NOT NULL REFERENCES doctors(id),
    starts_at timestamptz NOT NULL,
    duration_minutes integer NOT NULL DEFAULT 30 CHECK (duration_minutes = 30),
    CONSTRAINT slots_aligned CHECK (mod(extract(epoch FROM starts_at), 1800) = 0),
    UNIQUE(doctor_id, starts_at)
);

CREATE INDEX slots_start ON slots(starts_at);

CREATE TABLE appointments (
    id uuid PRIMARY KEY,
    patient_id uuid NOT NULL REFERENCES patients(id),
    slot_id uuid NOT NULL REFERENCES slots(id),
    category text NOT NULL CHECK (category IN ('consultation','exam','procedure')),
    status text NOT NULL CHECK (status IN ('scheduled','cancelled','completed')),
    version integer NOT NULL DEFAULT 1 CHECK (version >= 1)
);

CREATE UNIQUE INDEX appointments_slot_reserved ON appointments(slot_id) WHERE status IN ('scheduled','completed');

CREATE INDEX appointments_patient ON appointments(patient_id);

CREATE TABLE pending_actions (
    id uuid PRIMARY KEY,
    patient_id uuid NOT NULL REFERENCES patients(id),
    operation text NOT NULL CHECK (operation IN ('create_appointment','reschedule_appointment','cancel_appointment')),
    slot_id uuid REFERENCES slots(id),
    appointment_id uuid REFERENCES appointments(id),
    category text,
    expected_version integer NOT NULL DEFAULT 0,
    status text NOT NULL CHECK (status IN ('pending','succeeded','failed')),
    failure_code text NOT NULL DEFAULT '',
    summary text NOT NULL,
    result jsonb,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL CHECK (expires_at > created_at),
    CHECK ((status = 'succeeded' AND result IS NOT NULL) OR (status <> 'succeeded' AND result IS NULL)),
    CHECK (
        (operation = 'create_appointment' AND slot_id IS NOT NULL AND appointment_id IS NULL AND category IN ('consultation','exam','procedure') AND category IS NOT NULL)
        OR (operation = 'reschedule_appointment' AND slot_id IS NOT NULL AND appointment_id IS NOT NULL AND category IS NULL)
        OR (operation = 'cancel_appointment' AND slot_id IS NULL AND appointment_id IS NOT NULL AND category IS NULL)
    )
);

CREATE INDEX pending_actions_patient ON pending_actions(patient_id);

-- +goose Down
DROP TABLE pending_actions;
DROP TABLE appointments;
DROP TABLE slots;
DROP TABLE doctors;
DROP TABLE specialties;
DROP TABLE sessions;
DROP TABLE patients;
