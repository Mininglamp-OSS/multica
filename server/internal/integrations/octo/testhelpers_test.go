package octo

import "github.com/jackc/pgx/v5/pgtype"

// validUUID builds a deterministic, valid pgtype.UUID from a single byte so
// tests can fabricate distinct ids without a database. The byte fills all 16
// bytes, which is enough for equality/identity assertions.
func validUUID(b byte) pgtype.UUID {
	var u pgtype.UUID
	for i := range u.Bytes {
		u.Bytes[i] = b
	}
	u.Valid = true
	return u
}
