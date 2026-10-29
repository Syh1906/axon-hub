import type { SelfUsageIdentity } from './api';

export function sameUsageScope(a: SelfUsageIdentity, b: SelfUsageIdentity) {
  return a.scopeID === b.scopeID && a.apiKeyName === b.apiKeyName && a.groupName === b.groupName;
}
