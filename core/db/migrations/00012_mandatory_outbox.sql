-- +goose Up
-- tx_dirty is unlogged and transaction-local by xid; rollbacks undo its marks.
-- +goose StatementBegin
DO $install$
DECLARE ns text := current_schema();
BEGIN
 EXECUTE format($fn$
 CREATE FUNCTION %1$I.licetum_mark_rows() RETURNS trigger
 LANGUAGE plpgsql SECURITY DEFINER SET search_path TO %1$I, pg_catalog, pg_temp AS $body$
 BEGIN
   IF EXISTS (SELECT 1 FROM licetum_rows) THEN
     INSERT INTO tx_dirty(xid) VALUES(pg_current_xact_id()) ON CONFLICT DO NOTHING;
   END IF;
   RETURN NULL;
 END $body$ $fn$, ns);
 EXECUTE format($fn$
 CREATE FUNCTION %1$I.licetum_mark_truncate() RETURNS trigger
 LANGUAGE plpgsql SECURITY DEFINER SET search_path TO %1$I, pg_catalog, pg_temp AS $body$
 BEGIN
   INSERT INTO tx_dirty(xid) VALUES(pg_current_xact_id()) ON CONFLICT DO NOTHING;
   RETURN NULL;
 END $body$ $fn$, ns);
 EXECUTE format($fn$
 CREATE FUNCTION %1$I.licetum_require_outbox() RETURNS trigger
 LANGUAGE plpgsql SECURITY DEFINER SET search_path TO %1$I, pg_catalog, pg_temp AS $body$
 BEGIN
   IF NOT EXISTS (SELECT 1 FROM outbox WHERE xid=NEW.xid) THEN
     RAISE EXCEPTION 'policy transaction %% requires an outbox row', NEW.xid
       USING ERRCODE='23514', CONSTRAINT='licetum_outbox_required';
   END IF;
   DELETE FROM tx_dirty WHERE xid=NEW.xid;
   RETURN NULL;
 END $body$ $fn$, ns);
 EXECUTE format($fn$
 CREATE FUNCTION %1$I.licetum_cover_table(target regclass) RETURNS void
 LANGUAGE plpgsql SECURITY DEFINER SET search_path TO %1$I, pg_catalog, pg_temp AS $body$
 DECLARE event text; trigger_name text; transition text;
 BEGIN
   FOREACH event IN ARRAY ARRAY['INSERT','UPDATE','DELETE'] LOOP
     trigger_name := 'licetum_outbox_' || lower(event);
     transition := CASE WHEN event='DELETE' THEN 'OLD' ELSE 'NEW' END;
     EXECUTE format('CREATE TRIGGER %%I AFTER %%s ON %%s REFERENCING %%s TABLE AS licetum_rows FOR EACH STATEMENT EXECUTE FUNCTION %1$I.licetum_mark_rows()',trigger_name,event,target,transition);
     EXECUTE format('ALTER TABLE %%s ENABLE ALWAYS TRIGGER %%I',target,trigger_name);
   END LOOP;
   EXECUTE format('CREATE TRIGGER licetum_outbox_truncate AFTER TRUNCATE ON %%s FOR EACH STATEMENT EXECUTE FUNCTION %1$I.licetum_mark_truncate()',target);
   EXECUTE format('ALTER TABLE %%s ENABLE ALWAYS TRIGGER licetum_outbox_truncate',target);
 END $body$ $fn$, ns);
END $install$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER licetum_outbox_required
AFTER INSERT ON tx_dirty DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION licetum_require_outbox();
ALTER TABLE tx_dirty ENABLE ALWAYS TRIGGER licetum_outbox_required;

-- +goose StatementBegin
DO $cover$
DECLARE target text;
BEGIN
 FOREACH target IN ARRAY ARRAY[
  'service','type_resource','type_resource_parent','synchronizer','access_group',
  'user_group','group_member','role','role_permission','assignment_rule','log_settings',
  'subject_directory','pep_client','pep_credential','pep_issuer'
 ] LOOP
  PERFORM licetum_cover_table(target::regclass);
 END LOOP;
END $cover$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION licetum_cover_table(regclass) FROM PUBLIC;
REVOKE ALL ON FUNCTION licetum_mark_rows(), licetum_mark_truncate(), licetum_require_outbox() FROM PUBLIC;

-- +goose Down
DROP FUNCTION licetum_cover_table(regclass);
DROP FUNCTION licetum_require_outbox() CASCADE;
DROP FUNCTION licetum_mark_rows() CASCADE;
DROP FUNCTION licetum_mark_truncate() CASCADE;
