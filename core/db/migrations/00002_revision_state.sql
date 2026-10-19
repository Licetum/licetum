-- +goose Up
-- The revision of the policy lives next to the data it numbers, and changes in the same transaction as the data: it is
-- replicated in the same stream and cannot reach a replica before the data does.
--   scope 'rules'            the rules, memberships, groups, roles, access groups, services, types and synchronizers
--   scope 'data:<service>'   the resource data of the types of a service (bumped by a synchronization that changed it)
CREATE TABLE revision_state (
    scope    text   PRIMARY KEY,
    revision bigint NOT NULL DEFAULT 0
);

INSERT INTO revision_state (scope) VALUES ('rules');

-- +goose Down
DROP TABLE revision_state;
