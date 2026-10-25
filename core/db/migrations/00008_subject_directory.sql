-- +goose Up
-- The subject directory (ADR 0001, 8.8): the people of a service, as the application sent them on purpose. It replaces the
-- registry of known subjects (known_subject), which every verified token filled and which was global. Additive here: the
-- registry stays until the switch of the directory step.
--
-- subject_directory is what the readers see. subject_directory_seen keeps apart the time an entry was last sent, so that
-- sending the same entry again changes no row of the directory (and, later, projects nothing). The retention of a service is its
-- own setting for now (subject_directory_settings); where it lives in the end is decided with the schema of P3.
-- A directory write takes no revision: no decision reads the directory.

CREATE TABLE subject_directory (
    service_id    uuid                     NOT NULL REFERENCES service (id) ON DELETE CASCADE,
    subject_type  text                     NOT NULL,
    subject_id    text                     NOT NULL,
    display_name  text                     NOT NULL DEFAULT '',
    properties    jsonb                    NOT NULL DEFAULT '{}'::jsonb,
    updated_at    timestamp with time zone NOT NULL DEFAULT now(),
    PRIMARY KEY (service_id, subject_type, subject_id)
);

-- The keyset scan of the subject search and the list: bytewise (COLLATE "C"), so the pages do not depend on the collation of
-- the database. The primary key is in the collation of the database and cannot serve it.
CREATE INDEX ix_subject_directory_scan ON subject_directory USING btree (service_id, subject_type, subject_id COLLATE "C");

CREATE TABLE subject_directory_seen (
    service_id    uuid                     NOT NULL,
    subject_type  text                     NOT NULL,
    subject_id    text                     NOT NULL,
    seen_at       timestamp with time zone NOT NULL DEFAULT now(),
    PRIMARY KEY (service_id, subject_type, subject_id),
    FOREIGN KEY (service_id, subject_type, subject_id)
        REFERENCES subject_directory (service_id, subject_type, subject_id) ON DELETE CASCADE
);

-- Retention reads the entries by when they were sent.
CREATE INDEX ix_subject_directory_seen_at ON subject_directory_seen USING btree (service_id, seen_at);

CREATE TABLE subject_directory_settings (
    service_id      uuid    PRIMARY KEY REFERENCES service (id) ON DELETE CASCADE,
    -- NULL: no retention, an entry is kept until it is deleted.
    retention_days  integer CHECK (retention_days >= 1)
);

-- +goose Down
DROP TABLE subject_directory_settings;
DROP TABLE subject_directory_seen;
DROP TABLE subject_directory;
