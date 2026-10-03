package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/domain"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// QuoteRepository implements repository.QuoteRepository using PostgreSQL.
type QuoteRepository struct {
	db *sql.DB
}

// NewQuoteRepository returns a new PostgreSQL repository instance.
func NewQuoteRepository(db *sql.DB) *QuoteRepository {
	return &QuoteRepository{db: db}
}

// CreateRequest inserts a new quote update request.
func (r *QuoteRepository) CreateRequest(ctx context.Context, req *domain.QuoteRequest) error {
	if req.ID == uuid.Nil {
		req.ID = uuid.New()
	}
	now := time.Now().UTC()
	if req.CreatedAt.IsZero() {
		req.CreatedAt = now
	}
	req.UpdatedAt = now

	query := `
		INSERT INTO quote_requests (id, currency, status, price, error_message, idempotency_key, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	_, err := r.db.ExecContext(ctx, query,
		req.ID,
		strings.ToUpper(strings.TrimSpace(req.Currency)),
		req.Status,
		req.Price,
		req.ErrorMessage,
		req.IdempotencyKey,
		req.CreatedAt,
		req.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create quote request: %w", err)
	}

	return nil
}

// GetRequestByID retrieves a quote update request by its UUID.
func (r *QuoteRepository) GetRequestByID(ctx context.Context, id uuid.UUID) (*domain.QuoteRequest, error) {
	query := `
		SELECT id, currency, status, price, error_message, idempotency_key, created_at, updated_at
		FROM quote_requests
		WHERE id = $1
	`
	var req domain.QuoteRequest
	var price sql.NullFloat64
	var errMsg, idempKey sql.NullString

	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&req.ID,
		&req.Currency,
		&req.Status,
		&price,
		&errMsg,
		&idempKey,
		&req.CreatedAt,
		&req.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrQuoteRequestNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get quote request by id: %w", err)
	}

	if price.Valid {
		p := price.Float64
		req.Price = &p
	}
	if errMsg.Valid {
		msg := errMsg.String
		req.ErrorMessage = &msg
	}
	if idempKey.Valid {
		k := idempKey.String
		req.IdempotencyKey = &k
	}

	return &req, nil
}

// GetRequestByIdempotencyKey retrieves a request by idempotency key.
func (r *QuoteRepository) GetRequestByIdempotencyKey(ctx context.Context, key string) (*domain.QuoteRequest, error) {
	query := `
		SELECT id, currency, status, price, error_message, idempotency_key, created_at, updated_at
		FROM quote_requests
		WHERE idempotency_key = $1
	`
	var req domain.QuoteRequest
	var price sql.NullFloat64
	var errMsg, idempKey sql.NullString

	err := r.db.QueryRowContext(ctx, query, key).Scan(
		&req.ID,
		&req.Currency,
		&req.Status,
		&price,
		&errMsg,
		&idempKey,
		&req.CreatedAt,
		&req.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrQuoteRequestNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get quote request by idempotency key: %w", err)
	}

	if price.Valid {
		p := price.Float64
		req.Price = &p
	}
	if errMsg.Valid {
		msg := errMsg.String
		req.ErrorMessage = &msg
	}
	if idempKey.Valid {
		k := idempKey.String
		req.IdempotencyKey = &k
	}

	return &req, nil
}

// FindInFlightRequest looks for an existing PENDING or PROCESSING request within the window.
func (r *QuoteRepository) FindInFlightRequest(ctx context.Context, currency string, window time.Duration) (*domain.QuoteRequest, error) {
	normalized := strings.ToUpper(strings.TrimSpace(currency))
	cutoff := time.Now().UTC().Add(-window)

	query := `
		SELECT id, currency, status, price, error_message, idempotency_key, created_at, updated_at
		FROM quote_requests
		WHERE currency = $1 AND status IN ('PENDING', 'PROCESSING') AND created_at >= $2
		ORDER BY created_at DESC
		LIMIT 1
	`
	var req domain.QuoteRequest
	var price sql.NullFloat64
	var errMsg, idempKey sql.NullString

	err := r.db.QueryRowContext(ctx, query, normalized, cutoff).Scan(
		&req.ID,
		&req.Currency,
		&req.Status,
		&price,
		&errMsg,
		&idempKey,
		&req.CreatedAt,
		&req.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrQuoteRequestNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find in-flight request: %w", err)
	}

	if price.Valid {
		p := price.Float64
		req.Price = &p
	}
	if errMsg.Valid {
		msg := errMsg.String
		req.ErrorMessage = &msg
	}
	if idempKey.Valid {
		k := idempKey.String
		req.IdempotencyKey = &k
	}

	return &req, nil
}

// UpdateRequestStatus updates status, price, and error_message.
func (r *QuoteRepository) UpdateRequestStatus(ctx context.Context, id uuid.UUID, status domain.RequestStatus, price *float64, errMessage *string) error {
	query := `
		UPDATE quote_requests
		SET status = $1, price = $2, error_message = $3, updated_at = NOW()
		WHERE id = $4
	`
	res, err := r.db.ExecContext(ctx, query, status, price, errMessage, id)
	if err != nil {
		return fmt.Errorf("failed to update quote request status: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return domain.ErrQuoteRequestNotFound
	}

	return nil
}

// CompleteRequestAndSaveLatest updates the request to COMPLETED and updates latest_quote in a single transaction.
func (r *QuoteRepository) CompleteRequestAndSaveLatest(ctx context.Context, id uuid.UUID, currency string, price float64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	normalized := strings.ToUpper(strings.TrimSpace(currency))
	now := time.Now().UTC()

	// 1. Update quote_requests
	reqQuery := `
		UPDATE quote_requests
		SET status = $1, price = $2, error_message = NULL, updated_at = $3
		WHERE id = $4
	`
	res, err := tx.ExecContext(ctx, reqQuery, domain.StatusCompleted, price, now, id)
	if err != nil {
		return fmt.Errorf("failed to update quote request to completed: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return domain.ErrQuoteRequestNotFound
	}

	// 2. Upsert into latest_quotes
	latestQuery := `
		INSERT INTO latest_quotes (currency, price, quote_request_id, updated_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (currency) DO UPDATE SET
			price = EXCLUDED.price,
			quote_request_id = EXCLUDED.quote_request_id,
			updated_at = EXCLUDED.updated_at
	`
	if _, err := tx.ExecContext(ctx, latestQuery, normalized, price, id, now); err != nil {
		return fmt.Errorf("failed to upsert latest quote: %w", err)
	}

	return tx.Commit()
}

// FailRequest updates status to FAILED with error message.
func (r *QuoteRepository) FailRequest(ctx context.Context, id uuid.UUID, errMessage string) error {
	query := `
		UPDATE quote_requests
		SET status = $1, error_message = $2, updated_at = NOW()
		WHERE id = $3
	`
	res, err := r.db.ExecContext(ctx, query, domain.StatusFailed, errMessage, id)
	if err != nil {
		return fmt.Errorf("failed to fail quote request: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return domain.ErrQuoteRequestNotFound
	}

	return nil
}

// GetLatestQuote retrieves the most recent quote for a currency.
func (r *QuoteRepository) GetLatestQuote(ctx context.Context, currency string) (*domain.LatestQuote, error) {
	normalized := strings.ToUpper(strings.TrimSpace(currency))
	query := `
		SELECT currency, price, quote_request_id, updated_at
		FROM latest_quotes
		WHERE currency = $1
	`
	var quote domain.LatestQuote
	var reqID uuid.NullUUID

	err := r.db.QueryRowContext(ctx, query, normalized).Scan(
		&quote.Currency,
		&quote.Price,
		&reqID,
		&quote.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrLatestQuoteNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get latest quote: %w", err)
	}

	if reqID.Valid {
		quote.QuoteRequestID = &reqID.UUID
	}

	return &quote, nil
}

// ListPendingRequests retrieves pending or processing requests to resume after restart.
func (r *QuoteRepository) ListPendingRequests(ctx context.Context, limit int) ([]*domain.QuoteRequest, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `
		SELECT id, currency, status, price, error_message, idempotency_key, created_at, updated_at
		FROM quote_requests
		WHERE status IN ('PENDING', 'PROCESSING')
		ORDER BY created_at ASC
		LIMIT $1
	`
	rows, err := r.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list pending requests: %w", err)
	}
	defer rows.Close()

	var result []*domain.QuoteRequest
	for rows.Next() {
		var req domain.QuoteRequest
		var price sql.NullFloat64
		var errMsg, idempKey sql.NullString

		if err := rows.Scan(
			&req.ID,
			&req.Currency,
			&req.Status,
			&price,
			&errMsg,
			&idempKey,
			&req.CreatedAt,
			&req.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan pending request: %w", err)
		}

		if price.Valid {
			p := price.Float64
			req.Price = &p
		}
		if errMsg.Valid {
			msg := errMsg.String
			req.ErrorMessage = &msg
		}
		if idempKey.Valid {
			k := idempKey.String
			req.IdempotencyKey = &k
		}

		result = append(result, &req)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}
