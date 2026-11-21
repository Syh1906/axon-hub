import assert from 'node:assert/strict';
import test from 'node:test';
import { adaptResults, appendProbeEndpoints, capabilityEnabled, toggleCapability, IMAGE_TOOL, WEB_SEARCH } from './capability-probes.ts';

test('新适配默认关闭，反复启停或重测时只变更对应功能', () => {
  const configured = ['openai/responses'];
  const search = 'openai/alpha_search';
  const results = [search, IMAGE_TOOL, ...configured].map(apiFormat => ({ apiFormat, supported: true }));
  const first = adaptResults({ settings: {}, additions: [] }, [...results, { apiFormat: WEB_SEARCH, supported: false }], configured);
  assert.equal(capabilityEnabled(search, first, configured), false);
  assert.equal(capabilityEnabled(IMAGE_TOOL, first, configured), false);
  assert.equal(capabilityEnabled(configured[0], first, configured), true);
  assert.ok(!first.settings.adaptedCapabilities.includes(WEB_SEARCH));
  const enabled = toggleCapability(search, first, configured);
  const retest = adaptResults(enabled, [{ apiFormat: search, supported: false }, ...results], configured);
  assert.equal(capabilityEnabled(search, retest, configured), true);
  assert.equal(capabilityEnabled(IMAGE_TOOL, retest, configured), false);
  assert.deepEqual(appendProbeEndpoints([], retest.additions), [{ apiFormat: search }]);
  const off = toggleCapability(search, retest, configured);
  assert.deepEqual(off.additions, []);
  assert.ok(off.settings.disabledEndpoints.includes(search));
  assert.equal(capabilityEnabled(search, off, [search]), false);
});

test('首次探测保留渠道已有工具的启停选择', () => {
  const results = [IMAGE_TOOL, WEB_SEARCH].map(apiFormat => ({apiFormat, supported:true}));
  for (const enabled of [true, false]) {
    const edits = {settings:{allowImageGenerationTool:enabled,allowWebSearchTool:enabled},additions:[]};
    const detected = adaptResults(edits, results, ['openai/responses']);
    assert.equal(capabilityEnabled(IMAGE_TOOL,detected,[]),enabled);
    assert.equal(capabilityEnabled(WEB_SEARCH,detected,[]),enabled);
  }
});
