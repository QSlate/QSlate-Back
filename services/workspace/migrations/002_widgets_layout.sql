ALTER TABLE dashboards
  ADD COLUMN widgets JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(widgets) = 'array'),
  ADD COLUMN layout JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(layout) = 'array');
