-- +goose Up
DROP INDEX uq_service_slug;
ALTER TABLE service ADD CONSTRAINT uq_service_slug UNIQUE(slug);
-- +goose StatementBegin
CREATE FUNCTION licetum_immutable_service_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.slug IS DISTINCT FROM OLD.slug THEN
  RAISE EXCEPTION 'service slug cannot be changed' USING ERRCODE='23514',CONSTRAINT='ck_service_slug_immutable';
 END IF;
 IF NEW.identity_domain_id IS DISTINCT FROM OLD.identity_domain_id THEN
  RAISE EXCEPTION 'service identity_domain_id cannot be changed in P3' USING ERRCODE='23514',CONSTRAINT='ck_service_domain_immutable';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER service_identity_immutable BEFORE UPDATE ON service FOR EACH ROW EXECUTE FUNCTION licetum_immutable_service_identity();
ALTER TABLE service ENABLE ALWAYS TRIGGER service_identity_immutable;

-- +goose Down
DROP TRIGGER service_identity_immutable ON service;
DROP FUNCTION licetum_immutable_service_identity();
ALTER TABLE service DROP CONSTRAINT uq_service_slug;
CREATE UNIQUE INDEX uq_service_slug ON service(slug) WHERE deleted_at IS NULL;
