-- name: InsertRefreshToken :one
INSERT INTO refresh_tokens (user_id, token_hash, expires_at, user_agent, ip_address)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetRefreshTokenByHash :one
SELECT * FROM refresh_tokens
WHERE token_hash = $1
  AND revoked_at IS NULL
  AND expires_at > NOW()
LIMIT 1;

-- name: RevokeRefreshToken :exec
UPDATE refresh_tokens SET revoked_at = NOW() WHERE id = $1;

-- name: DeleteAllRefreshTokensForUser :exec
DELETE FROM refresh_tokens WHERE user_id = $1;

-- name: DeleteExpiredRefreshTokens :exec
DELETE FROM refresh_tokens WHERE expires_at < NOW();

-- name: ListActiveSessionsForUser :many
SELECT id, user_id, token_hash, expires_at, created_at, revoked_at, user_agent, ip_address
FROM refresh_tokens
WHERE user_id = $1
  AND revoked_at IS NULL
  AND expires_at > NOW()
ORDER BY created_at DESC;

-- name: RevokeSessionForUser :exec
UPDATE refresh_tokens SET revoked_at = NOW()
WHERE id = $1 AND user_id = $2;

-- name: InsertPasswordReset :one
INSERT INTO password_resets (user_id, token_hash, expires_at)
VALUES ($1, $2, NOW() + INTERVAL '1 hour')
RETURNING *;

-- name: GetPasswordResetByHash :one
SELECT * FROM password_resets
WHERE token_hash = $1
  AND used = FALSE
  AND expires_at > NOW()
LIMIT 1;

-- name: MarkPasswordResetUsed :exec
UPDATE password_resets SET used = TRUE WHERE id = $1;

-- name: DeleteExpiredPasswordResets :exec
DELETE FROM password_resets WHERE expires_at < NOW();