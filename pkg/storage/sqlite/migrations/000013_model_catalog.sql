ALTER TABLE llm_provider_model ADD COLUMN base_url TEXT NOT NULL DEFAULT '';
ALTER TABLE llm_provider_model ADD COLUMN headers_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE llm_provider_model ADD COLUMN max_output_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE llm_provider_model ADD COLUMN display_name TEXT NOT NULL DEFAULT '';

CREATE TABLE llm_catalog_default (
    singleton INTEGER PRIMARY KEY CHECK(singleton = 1),
    provider_id TEXT NOT NULL,
    model_id TEXT NOT NULL,
    updated_at INTEGER NOT NULL,
    FOREIGN KEY(provider_id) REFERENCES llm_provider(id) ON DELETE CASCADE,
    FOREIGN KEY(model_id) REFERENCES llm_model(id) ON DELETE CASCADE
);
