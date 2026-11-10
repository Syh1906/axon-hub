import { lazy } from 'react';

const AboutSettings = lazy(() => import('./components/about-settings').then((module) => ({ default: module.AboutSettings })));
const BackupSettings = lazy(() => import('./components/backup-settings').then((module) => ({ default: module.BackupSettings })));
const BrandSettings = lazy(() => import('./components/brand-settings').then((module) => ({ default: module.BrandSettings })));
const ClaudeCodeSettings = lazy(() => import('./components/claude-code-settings').then((module) => ({ default: module.ClaudeCodeSettings })));
const CodexCompatibilitySettings = lazy(() => import('./components/codex-compatibility-settings').then((module) => ({ default: module.CodexCompatibilitySettings })));
const DiagnosticsSettings = lazy(() => import('./components/diagnostics-settings').then((module) => ({ default: module.DiagnosticsSettings })));
const ForwardingSettings = lazy(() => import('./components/forwarding-settings').then((module) => ({ default: module.ForwardingSettings })));
const GeneralSettings = lazy(() => import('./components/general-settings').then((module) => ({ default: module.GeneralSettings })));
const ProxyPresetsSettings = lazy(() => import('./components/proxy-presets-settings').then((module) => ({ default: module.ProxyPresetsSettings })));
const QuotaSettings = lazy(() => import('./components/quota-settings').then((module) => ({ default: module.QuotaSettings })));
const RetrySettings = lazy(() => import('./components/retry-settings').then((module) => ({ default: module.RetrySettings })));
const SecuritySettings = lazy(() => import('./components/security-settings').then((module) => ({ default: module.SecuritySettings })));
const StorageSettings = lazy(() => import('./components/storage-settings').then((module) => ({ default: module.StorageSettings })));
const WebhookSettings = lazy(() => import('./components/webhook-settings').then((module) => ({ default: module.WebhookSettings })));

// System settings are split into a few pages, each listed under the admin
// "Settings" sidebar group and showing its settings as tabs. `general` lives at
// /system, the others at /system/<page>.
export type SystemPageKey = 'general' | 'requests' | 'integrations' | 'operations';

export type SystemTabKey =
  | 'general'
  | 'security'
  | 'brand'
  | 'about'
  | 'retry'
  | 'forwarding'
  | 'proxy'
  | 'quota'
  | 'webhook'
  | 'claude-code'
  | 'codex'
  | 'storage'
  | 'diagnostics'
  | 'backup';

export interface SystemTabDef {
  key: SystemTabKey;
  labelKey: string;
  component: React.ComponentType;
  ownerOnly?: boolean;
}

export interface SystemPageDef {
  titleKey: string;
  descriptionKey: string;
  tabs: SystemTabDef[];
}

export const SYSTEM_PAGES: Record<SystemPageKey, SystemPageDef> = {
  general: {
    titleKey: 'system.title',
    descriptionKey: 'system.description',
    tabs: [
      { key: 'general', labelKey: 'system.tabs.general', component: GeneralSettings },
      { key: 'security', labelKey: 'system.tabs.security', component: SecuritySettings },
      { key: 'brand', labelKey: 'system.tabs.brand', component: BrandSettings },
      { key: 'about', labelKey: 'system.tabs.about', component: AboutSettings },
    ],
  },
  requests: {
    titleKey: 'system.pages.requests.title',
    descriptionKey: 'system.pages.requests.description',
    tabs: [
      { key: 'retry', labelKey: 'system.tabs.retry', component: RetrySettings },
      { key: 'forwarding', labelKey: 'system.tabs.forwarding', component: ForwardingSettings },
      { key: 'proxy', labelKey: 'system.tabs.proxy', component: ProxyPresetsSettings },
      { key: 'quota', labelKey: 'system.tabs.quota', component: QuotaSettings },
    ],
  },
  integrations: {
    titleKey: 'system.pages.integrations.title',
    descriptionKey: 'system.pages.integrations.description',
    tabs: [
      { key: 'webhook', labelKey: 'system.tabs.webhook', component: WebhookSettings },
      { key: 'claude-code', labelKey: 'system.tabs.claudeCode', component: ClaudeCodeSettings },
      { key: 'codex', labelKey: 'system.tabs.codex', component: CodexCompatibilitySettings },
    ],
  },
  operations: {
    titleKey: 'system.pages.operations.title',
    descriptionKey: 'system.pages.operations.description',
    tabs: [
      { key: 'storage', labelKey: 'system.tabs.storage', component: StorageSettings },
      { key: 'diagnostics', labelKey: 'system.tabs.diagnostics', component: DiagnosticsSettings, ownerOnly: true },
      { key: 'backup', labelKey: 'system.tabs.backup', component: BackupSettings, ownerOnly: true },
    ],
  },
};

export function isSystemSubPageKey(value: string | undefined): value is Exclude<SystemPageKey, 'general'> {
  return value !== undefined && value !== 'general' && Object.prototype.hasOwnProperty.call(SYSTEM_PAGES, value);
}
