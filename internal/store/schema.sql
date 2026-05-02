CREATE TABLE IF NOT EXISTS repos (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    prefix             TEXT    NOT NULL UNIQUE,
    name               TEXT    NOT NULL,
    path               TEXT    NOT NULL UNIQUE,
    remote_url         TEXT    NOT NULL DEFAULT '',
    next_issue_number  INTEGER NOT NULL DEFAULT 1,
    created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS features (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    repo_id     INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    slug        TEXT    NOT NULL,
    title       TEXT    NOT NULL,
    description TEXT    NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(repo_id, slug)
);

CREATE TABLE IF NOT EXISTS issues (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    repo_id     INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    number      INTEGER NOT NULL,
    feature_id  INTEGER REFERENCES features(id) ON DELETE SET NULL,
    title       TEXT    NOT NULL,
    description TEXT    NOT NULL DEFAULT '',
    state       TEXT    NOT NULL CHECK (state IN
                  ('backlog','todo','in_progress','in_review','done','cancelled','duplicate')),
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(repo_id, number)
);

CREATE INDEX IF NOT EXISTS idx_issues_state ON issues(state);
CREATE INDEX IF NOT EXISTS idx_issues_feature ON issues(feature_id);

CREATE TABLE IF NOT EXISTS comments (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    issue_id   INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    author     TEXT    NOT NULL,
    body       TEXT    NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_comments_issue ON comments(issue_id);

CREATE TABLE IF NOT EXISTS issue_relations (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    from_issue_id INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    to_issue_id   INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    type          TEXT    NOT NULL CHECK (type IN ('blocks','relates_to','duplicate_of')),
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(from_issue_id, to_issue_id, type),
    CHECK (from_issue_id <> to_issue_id)
);

CREATE INDEX IF NOT EXISTS idx_relations_from ON issue_relations(from_issue_id);
CREATE INDEX IF NOT EXISTS idx_relations_to   ON issue_relations(to_issue_id);

CREATE TABLE IF NOT EXISTS issue_pull_requests (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    issue_id   INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    url        TEXT    NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(issue_id, url)
);

CREATE INDEX IF NOT EXISTS idx_prs_issue ON issue_pull_requests(issue_id);

CREATE TABLE IF NOT EXISTS attachments (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    issue_id   INTEGER REFERENCES issues(id)   ON DELETE CASCADE,
    feature_id INTEGER REFERENCES features(id) ON DELETE CASCADE,
    filename   TEXT    NOT NULL,
    content    TEXT    NOT NULL,
    size_bytes INTEGER NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK ((issue_id IS NULL) <> (feature_id IS NULL)),
    UNIQUE(issue_id,   filename),
    UNIQUE(feature_id, filename)
);

CREATE INDEX IF NOT EXISTS idx_attachments_issue   ON attachments(issue_id);
CREATE INDEX IF NOT EXISTS idx_attachments_feature ON attachments(feature_id);

CREATE TABLE IF NOT EXISTS issue_tags (
    issue_id INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    tag      TEXT    NOT NULL,
    PRIMARY KEY (issue_id, tag),
    CHECK (length(tag) > 0)
);

CREATE INDEX IF NOT EXISTS idx_issue_tags_tag ON issue_tags(tag);

CREATE TABLE IF NOT EXISTS documents (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    repo_id    INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    filename   TEXT    NOT NULL,
    type       TEXT    NOT NULL CHECK (type IN
                 ('user_docs','project_in_planning','project_in_progress',
                  'project_complete','vendor_docs','architecture','designs',
                  'testing_plans')),
    content    TEXT    NOT NULL,
    size_bytes INTEGER NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(repo_id, filename)
);

CREATE INDEX IF NOT EXISTS idx_documents_type ON documents(type);

CREATE TABLE IF NOT EXISTS document_links (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    document_id INTEGER NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    issue_id    INTEGER REFERENCES issues(id)   ON DELETE CASCADE,
    feature_id  INTEGER REFERENCES features(id) ON DELETE CASCADE,
    description TEXT    NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK ((issue_id IS NULL) <> (feature_id IS NULL))
);

CREATE UNIQUE INDEX IF NOT EXISTS uniq_doc_issue
    ON document_links(document_id, issue_id) WHERE issue_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uniq_doc_feature
    ON document_links(document_id, feature_id) WHERE feature_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_doc_links_issue   ON document_links(issue_id);
CREATE INDEX IF NOT EXISTS idx_doc_links_feature ON document_links(feature_id);
