import assert from 'node:assert/strict';
import test from 'node:test';
import { endpointRequestURL, mergeEndpoints } from './channel-addresses.ts';
import {
  CHANNEL_QUERY_FULL_NODE_SELECTION,
  CHANNEL_QUERY_LIST_NODE_BASE_SELECTION,
  CHANNEL_QUERY_PROXY_SELECTION,
} from './channel-query-fields.ts';

test('default paths normalize v1 while explicit paths preserve the configured base', () => {
  assert.equal(endpointRequestURL('https://a.example.com', { apiFormat: 'openai/responses' }), 'https://a.example.com/v1/responses');
  assert.equal(endpointRequestURL('https://b.example.com/v1/', { apiFormat: 'openai/responses' }), 'https://b.example.com/v1/responses');
  assert.equal(
    endpointRequestURL('https://c.example.com/api', { apiFormat: 'openai/responses', path: '/custom' }),
    'https://c.example.com/api/custom'
  );
  assert.equal(
    endpointRequestURL('https://c.example.com/custom#', { apiFormat: 'openai/responses' }),
    'https://c.example.com/custom/responses'
  );
  assert.equal(endpointRequestURL('https://c.example.com/raw##', { apiFormat: 'openai/responses' }), 'https://c.example.com/raw');
});

test('overriding a default interface produces one editable row and preserves other interfaces', () => {
  assert.deepEqual(
    mergeEndpoints(
      [{ apiFormat: 'openai/responses' }, { apiFormat: 'anthropic/messages' }],
      [{ apiFormat: 'openai/responses', path: '/custom' }]
    ),
    [{ apiFormat: 'openai/responses', path: '/custom' }, { apiFormat: 'anthropic/messages' }]
  );
});

test('channel detail, list and proxy reads include the saved address settings', () => {
  for (const selection of [CHANNEL_QUERY_FULL_NODE_SELECTION, CHANNEL_QUERY_LIST_NODE_BASE_SELECTION, CHANNEL_QUERY_PROXY_SELECTION]) {
    const addresses = selection.match(/upstreamAddresses\s*\{([^}]+)\}/);
    assert.ok(addresses, 'editing after a list read must retain all saved addresses');
    assert.deepEqual(addresses[1].trim().split(/\s+/), ['id', 'name', 'url', 'priority', 'enabled']);
    assert.match(selection, /\baddressFailover\b/);
  }
});
