CREATE TABLE IF NOT EXISTS teams (
    name TEXT PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS users (
    user_id TEXT PRIMARY KEY,
    username TEXT NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    team_name TEXT NOT NULL REFERENCES teams(name) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS pull_requests (
    pr_id TEXT PRIMARY KEY,
    pr_name TEXT NOT NULL,
    author_id TEXT NOT NULL REFERENCES users(user_id),
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    merged_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS assigned_reviewers (
    pr_id TEXT NOT NULL REFERENCES pull_requests(pr_id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(user_id) ON DELETE CASCADE,
    PRIMARY KEY(pr_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_users_team_name ON users(team_name);
CREATE INDEX IF NOT EXISTS idx_pull_requests_author_id ON pull_requests(author_id);
CREATE INDEX IF NOT EXISTS idx_assigned_reviewers_pr_id ON assigned_reviewers(pr_id);
CREATE INDEX IF NOT EXISTS idx_assigned_reviewers_user_id ON assigned_reviewers(user_id);
