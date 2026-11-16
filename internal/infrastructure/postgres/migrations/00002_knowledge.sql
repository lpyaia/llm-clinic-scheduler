-- +goose Up
CREATE EXTENSION IF NOT EXISTS vector WITH SCHEMA public;

CREATE TABLE clinic_documents (
    id uuid PRIMARY KEY,
    source text NOT NULL CHECK (btrim(source) <> ''),
    content text NOT NULL CHECK (btrim(content) <> ''),
    provider text NOT NULL CHECK (btrim(provider) <> ''),
    model text NOT NULL CHECK (btrim(model) <> ''),
    dimensions integer NOT NULL CHECK (dimensions BETWEEN 1 AND 16000),
    chunk_count integer NOT NULL CHECK (chunk_count BETWEEN 1 AND 256),
    UNIQUE (id, dimensions)
);

CREATE TABLE clinic_document_chunks (
    document_id uuid NOT NULL,
    ordinal integer NOT NULL CHECK (ordinal > 0),
    content text NOT NULL CHECK (btrim(content) <> ''),
    start_line integer NOT NULL CHECK (start_line > 0),
    end_line integer NOT NULL CHECK (end_line >= start_line),
    dimensions integer NOT NULL,
    embedding public.vector NOT NULL,
    PRIMARY KEY (document_id, ordinal),
    FOREIGN KEY (document_id, dimensions) REFERENCES clinic_documents (id, dimensions) ON DELETE CASCADE,
    CHECK (public.vector_dims(embedding) = dimensions),
    CHECK (public.vector_norm(embedding) > 0)
);

-- +goose Down
DROP TABLE clinic_document_chunks;
DROP TABLE clinic_documents;
-- Keep the shared vector extension: other schemas may still depend on it.
