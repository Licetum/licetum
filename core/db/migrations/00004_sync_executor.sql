-- Support tables of the synchronization pipeline.
--
--   sync_demand:   when somebody last asked for the data of a type; the worker's schedule keeps the types in demand fresh.
--   sync_executor: the heartbeat of the workers of the DBOS engine (SYNC__ENGINE=dbos). DBOS does not hand the interrupted
--                  workflows of a dead executor to a live one, so the workers watch each other's heartbeat and do it.

-- +goose Up
CREATE TABLE sync_demand (
    type_resource_id uuid NOT NULL,
    last_asked_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT sync_demand_pkey PRIMARY KEY (type_resource_id),
    CONSTRAINT sync_demand_type_resource_id_fkey FOREIGN KEY (type_resource_id) REFERENCES type_resource(id) ON DELETE CASCADE
);

CREATE TABLE sync_executor (
    executor_id text NOT NULL,
    heartbeat_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT sync_executor_pkey PRIMARY KEY (executor_id)
);

-- +goose Down
DROP TABLE IF EXISTS sync_executor;
DROP TABLE IF EXISTS sync_demand;
