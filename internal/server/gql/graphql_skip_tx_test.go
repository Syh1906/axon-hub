package gql

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

func TestSkipMutationTransactionMatchesFieldsForAnyOperationName(t *testing.T) {
	schema := gqlparser.MustLoadSchema(&ast.Source{Input: `
		type Query { ok: Boolean }
		type Mutation {
			testChannel(id: ID!): Boolean
			testChannelAPIKeys(id: ID!): Boolean
			testChannelAPIKey(id: ID!, key: String!): Boolean
			bulkImportChannels: Boolean
			refreshChannelUpstreams: Boolean
			verifyChannelUpstreamAccount: Boolean
			createChannel(name: String!): Boolean
		}
	`})

	cases := []struct {
		name  string
		query string
		skip  bool
	}{
		{"anonymous testChannel", `mutation { testChannel(id: "1") }`, true},
		{"named testChannelAPIKeys", `mutation TestChannelAPIKeys { testChannelAPIKeys(id: "1") }`, true},
		{"single key test", `mutation TestKey { testChannelAPIKey(id: "1", key: "k") }`, true},
		{"bulk import", `mutation Import { bulkImportChannels }`, true},
		{"refresh", `mutation Refresh { refreshChannelUpstreams }`, true},
		{"verify", `mutation { verifyChannelUpstreamAccount }`, true},
		{"ordinary mutation", `mutation { createChannel(name: "x") }`, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, errs := gqlparser.LoadQuery(schema, tc.query)
			require.Empty(t, errs)
			require.Equal(t, tc.skip, skipMutationTransaction(doc.Operations[0]))
		})
	}
}
