-- Core schema for Ask the Handbook (proposal.md §7, MVP subset).
-- Embedding dimension must match store.Dim (768 = nomic-embed-text).

CREATE TABLE audiences (
  id text PRIMARY KEY
);

CREATE TABLE documents (
  id                 bigserial PRIMARY KEY,
  source_id          text NOT NULL UNIQUE,
  title              text NOT NULL,
  owner              text NOT NULL DEFAULT '',
  current_version_id bigint
);

CREATE TABLE document_versions (
  id           bigserial PRIMARY KEY,
  document_id  bigint NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
  content_hash text NOT NULL,
  last_updated date NOT NULL,
  is_current   boolean NOT NULL DEFAULT false,
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX document_versions_one_current ON document_versions (document_id) WHERE is_current;

-- Default-deny: a document with no rows here is invisible to search.
CREATE TABLE document_audiences (
  document_id bigint NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
  audience_id text   NOT NULL REFERENCES audiences(id),
  PRIMARY KEY (document_id, audience_id)
);

CREATE TABLE chunks (
  id           text   NOT NULL,            -- stable: hash(source_id, heading_path, ordinal)
  version_id   bigint NOT NULL REFERENCES document_versions(id) ON DELETE CASCADE,
  document_id  bigint NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
  ordinal      int    NOT NULL,
  heading_path text[] NOT NULL,
  content      text   NOT NULL,
  chunk_type   text   NOT NULL CHECK (chunk_type IN ('prose', 'table', 'list', 'faq')),
  embedding    vector(768) NOT NULL,
  tsv          tsvector GENERATED ALWAYS AS (to_tsvector('english', content)) STORED,
  is_current   boolean NOT NULL DEFAULT false,
  PRIMARY KEY (version_id, id)
);
CREATE INDEX chunks_current_tsv ON chunks USING gin (tsv) WHERE is_current;
CREATE INDEX chunks_current_id ON chunks (id) WHERE is_current;

CREATE TABLE conversations (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_sub   text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE messages (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  user_sub        text NOT NULL,
  question        text NOT NULL,
  answer          text NOT NULL,
  outcome         text NOT NULL CHECK (outcome IN ('answered', 'declined_no_source', 'routed_to_human', 'clarification_needed')),
  model           text NOT NULL DEFAULT '',
  latency_ms      int  NOT NULL DEFAULT 0,
  input_tokens    int  NOT NULL DEFAULT 0,
  output_tokens   int  NOT NULL DEFAULT 0,
  created_at      timestamptz NOT NULL DEFAULT now()
);

-- Stores every retrieved chunk, not only cited ones (used_in_answer marks citations).
CREATE TABLE message_citations (
  message_id     uuid   NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  version_id     bigint NOT NULL,
  chunk_id       text   NOT NULL,
  rank           int    NOT NULL,
  score          double precision NOT NULL,
  used_in_answer boolean NOT NULL DEFAULT false,
  PRIMARY KEY (message_id, version_id, chunk_id),
  FOREIGN KEY (version_id, chunk_id) REFERENCES chunks(version_id, id) ON DELETE CASCADE
);

CREATE TABLE feedback (
  id         bigserial PRIMARY KEY,
  message_id uuid NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  user_sub   text NOT NULL,
  rating     smallint NOT NULL CHECK (rating IN (-1, 1)),
  comment    text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE ingestion_jobs (
  id          bigserial PRIMARY KEY,
  source_path text NOT NULL,
  status      text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'done', 'failed')),
  attempts    int  NOT NULL DEFAULT 0,
  run_after   timestamptz NOT NULL DEFAULT now(),
  locked_at   timestamptz,
  last_error  text NOT NULL DEFAULT ''
);
CREATE INDEX ingestion_jobs_queue ON ingestion_jobs (run_after) WHERE status = 'queued';
