-- Project names become daemon-unique after duplicate rows are renamed by the
-- matching migration data step.
DROP INDEX IF EXISTS idx_project_name;
CREATE UNIQUE INDEX idx_project_name ON project(name);
