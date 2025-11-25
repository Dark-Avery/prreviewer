-- Удаляем CHECK constraint из pull_requests (валидация перенесена на уровень приложения)
ALTER TABLE pull_requests DROP CONSTRAINT IF EXISTS pull_requests_status_check;

-- Добавляем индексы для производительности
CREATE INDEX IF NOT EXISTS idx_users_team_name ON users(team_name);
CREATE INDEX IF NOT EXISTS idx_users_team_active ON users(team_name, is_active) WHERE is_active = true;
CREATE INDEX IF NOT EXISTS idx_pull_requests_status ON pull_requests(status);
CREATE INDEX IF NOT EXISTS idx_pull_requests_author ON pull_requests(author_id);
CREATE INDEX IF NOT EXISTS idx_assigned_reviewers_pr ON assigned_reviewers(pr_id);
CREATE INDEX IF NOT EXISTS idx_assigned_reviewers_user ON assigned_reviewers(user_id);

