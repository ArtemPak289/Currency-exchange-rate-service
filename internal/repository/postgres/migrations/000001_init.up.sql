CREATE TABLE IF NOT EXISTS quote_requests (
    id UUID PRIMARY KEY,
    currency VARCHAR(16) NOT NULL,
    status VARCHAR(20) NOT NULL,
    price DOUBLE PRECISION NULL,
    error_message TEXT NULL,
    idempotency_key VARCHAR(128) NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_quote_requests_currency_status ON quote_requests(currency, status);
CREATE UNIQUE INDEX IF NOT EXISTS idx_quote_requests_idempotency_key ON quote_requests(idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_quote_requests_status ON quote_requests(status) WHERE status IN ('PENDING', 'PROCESSING');

CREATE TABLE IF NOT EXISTS latest_quotes (
    currency VARCHAR(16) PRIMARY KEY,
    price DOUBLE PRECISION NOT NULL,
    quote_request_id UUID REFERENCES quote_requests(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
