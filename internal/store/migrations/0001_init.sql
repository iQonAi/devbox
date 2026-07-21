-- 0001_init: initial schema design

CREATE TABLE repos (
id                INTEGER PRIMARY KEY,
name              TEXT UNIQUE NOT NULL,
github_owner      TEXT NOT NULL,
github_repo       TEXT NOT NULL,
default_branch    TEXT NOT NULL,
mirror_path       TEXT,
token_ref         TEXT NOT NULL
);

CREATE TABLE tasks (
   id TEXT PRIMARY KEY,
   repo_id INTEGER NOT NULL REFERENCES repos(id),
   source TEXT NOT NULL,
   issue_number INTEGER,
   prompt_path TEXT,
   agent TEXT,
   branch TEXT,
   host_worktree TEXT,
   container_id TEXT,
   state TEXT,
   commit_count INTEGER,
   exit_code INTEGER,
   pr_url TEXT,
   summary TEXT,
   error TEXT,
   created_at TIMESTAMP NOT NULL,
   started_at TIMESTAMP,
   finished_at TIMESTAMP
);


CREATE TABLE task_events (
id INTEGER PRIMARY KEY,
task_id TEXT NOT NULL REFERENCES tasks(id),
   ts TIMESTAMP NOT NULL,
   type TEXT NOT NULL,
   message TEXT
);

CREATE TABLE artifacts (
id INTEGER PRIMARY KEY,
task_id TEXT NOT NULL REFERENCES tasks(id),
   kind TEXT NOT NULL,
   path TEXT NOT NULL
);

CREATE INDEX idx_task_repo_id  ON tasks(repo_id);
CREATE INDEX idx_task_events_task ON task_events(task_id);
CREATE INDEX idx_artifacts_task ON artifacts(task_id);
