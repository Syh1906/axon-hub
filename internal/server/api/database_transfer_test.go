package api

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestMigrationDownloadTicketPurposeExpiryAndSignature(t *testing.T) {
	ticket := migrationTicket{Job: "test-job", Digest: "test-digest", User: 1, Expires: time.Now().Add(time.Minute).Unix(), Purpose: "database-transfer-download"}
	encoded, err := signMigrationTicket(ticket, "test-only-key")
	require.NoError(t, err)
	parsed, ok := verifyMigrationTicket(encoded, "test-only-key")
	require.True(t, ok)
	require.Equal(t, ticket, parsed)
	_, ok = verifyMigrationTicket(encoded, "other-test-key")
	require.False(t, ok)
	_, ok = verifyMigrationTicket(encoded+"x", "test-only-key")
	require.False(t, ok)
	for _, invalid := range []migrationTicket{
		{User: 1, Expires: time.Now().Add(-time.Minute).Unix(), Purpose: ticket.Purpose},
		{User: 1, Expires: time.Now().Add(time.Hour).Unix(), Purpose: ticket.Purpose},
		{User: 1, Expires: ticket.Expires, Purpose: "login"},
	} {
		encoded, err = signMigrationTicket(invalid, "test-only-key")
		require.NoError(t, err)
		_, ok = verifyMigrationTicket(encoded, "test-only-key")
		require.False(t, ok)
	}
}
