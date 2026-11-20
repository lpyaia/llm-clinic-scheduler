-- +goose Up
ALTER TABLE clinic_documents
    ADD COLUMN topic text NOT NULL DEFAULT 'general',
    ADD COLUMN exam text NOT NULL DEFAULT '',
    ADD COLUMN procedure text NOT NULL DEFAULT '',
    ADD COLUMN doctor text NOT NULL DEFAULT '';

CREATE INDEX clinic_documents_source ON clinic_documents (source);
CREATE INDEX clinic_documents_topic ON clinic_documents (topic);

ALTER TABLE clinic_document_chunks ADD COLUMN page integer NOT NULL DEFAULT 0 CHECK (page >= 0);

-- +goose Down
ALTER TABLE clinic_document_chunks DROP COLUMN page;
DROP INDEX clinic_documents_topic;
DROP INDEX clinic_documents_source;
ALTER TABLE clinic_documents DROP COLUMN doctor, DROP COLUMN procedure, DROP COLUMN exam, DROP COLUMN topic;
