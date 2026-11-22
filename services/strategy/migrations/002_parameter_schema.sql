ALTER TABLE strategy_versions
    ADD COLUMN parameter_schema JSONB NOT NULL DEFAULT '[]'::jsonb;
